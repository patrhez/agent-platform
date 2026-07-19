// Package hadashboard provides a localhost-only control surface for Kubernetes HA experiments.
package hadashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

const platformSelector = "app.kubernetes.io/part-of=agent-platform"

var terminableComponents = []string{"api", "worker"}
var displayedComponents = []string{"api", "mcp", "web", "worker"}

// Pod is the safe subset of Kubernetes Pod state displayed by the dashboard.
type Pod struct {
	Name       string `json:"name"`
	Component  string `json:"component"`
	Phase      string `json:"phase"`
	Ready      bool   `json:"ready"`
	Restarts   int32  `json:"restarts"`
	Node       string `json:"node"`
	AgeSeconds int64  `json:"ageSeconds"`
	Terminable bool   `json:"terminable"`
}

// Kubernetes exposes the operations used by the dashboard.
type Kubernetes interface {
	ListPods(context.Context) ([]Pod, error)
	Logs(context.Context, string, int) (string, error)
	Terminate(context.Context, string) error
}

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, arguments...).CombinedOutput()
}

// KubectlClient invokes kubectl without a shell and validates every Pod against the configured namespace.
type KubectlClient struct {
	namespace   string
	contextName string
	runner      commandRunner
}

// NewKubectlClient creates a client for one fixed Kubernetes namespace.
func NewKubectlClient(namespace string, contextName string) (*KubectlClient, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil, fmt.Errorf("Kubernetes namespace is required")
	}
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return nil, fmt.Errorf("Kubernetes context is required")
	}
	return &KubectlClient{namespace: namespace, contextName: contextName, runner: execRunner{}}, nil
}

// ListPods returns Agent Platform Pods in a stable display order.
func (client *KubectlClient) ListPods(ctx context.Context) ([]Pod, error) {
	output, err := client.runner.Run(
		ctx,
		"kubectl",
		"--context",
		client.contextName,
		"get",
		"pods",
		"--namespace",
		client.namespace,
		"--selector",
		platformSelector,
		"--output",
		"json",
	)
	if err != nil {
		return nil, commandError("list Kubernetes Pods", output, err)
	}
	pods, err := decodePods(output, time.Now())
	if err != nil {
		return nil, fmt.Errorf("decode Kubernetes Pods: %w", err)
	}
	slices.SortFunc(pods, func(left Pod, right Pod) int {
		if left.Component != right.Component {
			return strings.Compare(left.Component, right.Component)
		}
		return strings.Compare(left.Name, right.Name)
	})
	return pods, nil
}

// Logs returns recent logs for a Pod that currently belongs to this deployment.
func (client *KubectlClient) Logs(ctx context.Context, podName string, tailLines int) (string, error) {
	if err := client.validatePod(ctx, podName, false); err != nil {
		return "", err
	}
	if tailLines < 1 || tailLines > 1000 {
		return "", fmt.Errorf("log tail must be between 1 and 1000 lines")
	}
	output, err := client.runner.Run(
		ctx,
		"kubectl",
		"--context",
		client.contextName,
		"logs",
		podName,
		"--namespace",
		client.namespace,
		"--tail",
		strconv.Itoa(tailLines),
		"--timestamps=true",
	)
	if err != nil {
		return "", commandError("read Kubernetes Pod logs", output, err)
	}
	return string(output), nil
}

// Terminate forcefully removes one API or Worker Pod so its Deployment can replace it.
func (client *KubectlClient) Terminate(ctx context.Context, podName string) error {
	if err := client.validatePod(ctx, podName, true); err != nil {
		return err
	}
	output, err := client.runner.Run(
		ctx,
		"kubectl",
		"--context",
		client.contextName,
		"delete",
		"pod",
		podName,
		"--namespace",
		client.namespace,
		"--grace-period=0",
		"--force",
		"--wait=false",
	)
	if err != nil {
		return commandError("terminate Kubernetes Pod", output, err)
	}
	return nil
}

func (client *KubectlClient) validatePod(ctx context.Context, podName string, requireTerminable bool) error {
	pods, err := client.ListPods(ctx)
	if err != nil {
		return err
	}
	for _, pod := range pods {
		if pod.Name != podName {
			continue
		}
		if requireTerminable && !pod.Terminable {
			return fmt.Errorf("Pod %s is not an API or Worker Pod", podName)
		}
		return nil
	}
	return fmt.Errorf("Pod %s does not belong to the Agent Platform namespace", podName)
}

type podList struct {
	Items []struct {
		Metadata struct {
			Name              string            `json:"name"`
			Labels            map[string]string `json:"labels"`
			CreationTimestamp time.Time         `json:"creationTimestamp"`
		} `json:"metadata"`
		Spec struct {
			NodeName string `json:"nodeName"`
		} `json:"spec"`
		Status struct {
			Phase      string `json:"phase"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
			ContainerStatuses []struct {
				RestartCount int32 `json:"restartCount"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func decodePods(contents []byte, now time.Time) ([]Pod, error) {
	var list podList
	if err := json.Unmarshal(contents, &list); err != nil {
		return nil, err
	}
	pods := make([]Pod, 0, len(list.Items))
	for _, item := range list.Items {
		component := item.Metadata.Labels["app.kubernetes.io/component"]
		if !slices.Contains(displayedComponents, component) {
			continue
		}
		pod := Pod{
			Name:       item.Metadata.Name,
			Component:  component,
			Phase:      item.Status.Phase,
			Ready:      podReady(item.Status.Conditions),
			Node:       item.Spec.NodeName,
			AgeSeconds: max(0, int64(now.Sub(item.Metadata.CreationTimestamp).Seconds())),
			Terminable: slices.Contains(terminableComponents, component),
		}
		for _, status := range item.Status.ContainerStatuses {
			pod.Restarts += status.RestartCount
		}
		pods = append(pods, pod)
	}
	return pods, nil
}

func podReady(conditions []struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}) bool {
	for _, condition := range conditions {
		if condition.Type == "Ready" {
			return condition.Status == "True"
		}
	}
	return false
}

func commandError(operation string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %s: %w", operation, message, err)
}
