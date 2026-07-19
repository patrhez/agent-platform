package hadashboard

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type commandResult struct {
	output []byte
	err    error
}

type fakeCommandRunner struct {
	results  []commandResult
	commands [][]string
}

func (runner *fakeCommandRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	runner.commands = append(runner.commands, append([]string{name}, arguments...))
	if len(runner.results) == 0 {
		return nil, errors.New("unexpected command")
	}
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result.output, result.err
}

func TestKubectlClientListsPods(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{results: []commandResult{{output: podListFixture()}}}
	client := &KubectlClient{
		namespace:   "agent-platform-ha",
		contextName: "k3d-agent-platform-ha",
		runner:      runner,
	}

	pods, err := client.ListPods(context.Background())
	if err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("ListPods() count = %d, want 2", len(pods))
	}
	if pods[0].Component != "api" || !pods[0].Ready || !pods[0].Terminable {
		t.Fatalf("ListPods() api Pod = %+v", pods[0])
	}
	if pods[1].Component != "web" || pods[1].Terminable {
		t.Fatalf("ListPods() web Pod = %+v", pods[1])
	}
}

func TestDecodePodsOmitsOneShotInfrastructureJobs(t *testing.T) {
	t.Parallel()

	contents := []byte(`{"items":[{"metadata":{"name":"database-migrate-123",` +
		`"labels":{"app.kubernetes.io/component":"migrate"},` +
		`"creationTimestamp":"2026-07-19T00:00:00Z"},` +
		`"spec":{},"status":{"phase":"Succeeded"}}]}`)

	pods, err := decodePods(contents, time.Now())
	if err != nil {
		t.Fatalf("decodePods() error = %v", err)
	}
	if len(pods) != 0 {
		t.Fatalf("decodePods() count = %d, want no migration Jobs", len(pods))
	}
}

func TestKubectlClientTerminatesKnownAPIPodWithoutShell(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{results: []commandResult{{output: podListFixture()}, {output: []byte("deleted")}}}
	client := &KubectlClient{
		namespace:   "agent-platform-ha",
		contextName: "k3d-agent-platform-ha",
		runner:      runner,
	}

	if err := client.Terminate(context.Background(), "api-7d9c"); err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}
	want := []string{
		"kubectl",
		"--context",
		"k3d-agent-platform-ha",
		"delete",
		"pod",
		"api-7d9c",
		"--namespace",
		"agent-platform-ha",
		"--grace-period=0",
		"--force",
		"--wait=false",
	}
	if got := runner.commands[1]; !slices.Equal(got, want) {
		t.Fatalf("terminate command = %q, want %q", got, want)
	}
}

func TestKubectlClientRejectsNonTerminablePod(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{results: []commandResult{{output: podListFixture()}}}
	client := &KubectlClient{
		namespace:   "agent-platform-ha",
		contextName: "k3d-agent-platform-ha",
		runner:      runner,
	}

	err := client.Terminate(context.Background(), "web-5b6f")
	if err == nil {
		t.Fatal("Terminate() error = nil, want rejection")
	}
	if len(runner.commands) != 1 {
		t.Fatalf("command count = %d, want validation command only", len(runner.commands))
	}
}

func podListFixture() []byte {
	createdAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	return []byte(`{"items":[` +
		`{"metadata":{"name":"web-5b6f","labels":{"app.kubernetes.io/component":"web"},` +
		`"creationTimestamp":"` + createdAt + `"},"spec":{"nodeName":"agent-1"},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],` +
		`"containerStatuses":[{"restartCount":0}]}},` +
		`{"metadata":{"name":"api-7d9c","labels":{"app.kubernetes.io/component":"api"},` +
		`"creationTimestamp":"` + createdAt + `"},"spec":{"nodeName":"agent-0"},` +
		`"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],` +
		`"containerStatuses":[{"restartCount":1}]}}]}`)
}
