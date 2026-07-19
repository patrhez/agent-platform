package hadashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeKubernetes struct {
	pods       []Pod
	logs       string
	terminated string
}

func (kubernetes *fakeKubernetes) ListPods(context.Context) ([]Pod, error) {
	return kubernetes.pods, nil
}

func (kubernetes *fakeKubernetes) Logs(context.Context, string, int) (string, error) {
	return kubernetes.logs, nil
}

func (kubernetes *fakeKubernetes) Terminate(_ context.Context, podName string) error {
	kubernetes.terminated = podName
	return nil
}

func TestHandlerServesPodState(t *testing.T) {
	t.Parallel()

	handler, err := NewHandler(&fakeKubernetes{pods: []Pod{{Name: "api-7d9c", Ready: true}}})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/pods", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, "api-7d9c") {
		t.Fatalf("body = %q, want Pod name", body)
	}
}

func TestHandlerRequiresExplicitTerminationHeader(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	handler, err := NewHandler(kubernetes)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/pods/api-7d9c/terminate", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if kubernetes.terminated != "" {
		t.Fatalf("terminated Pod = %q, want none", kubernetes.terminated)
	}
}

func TestHandlerTerminatesSelectedPod(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	handler, err := NewHandler(kubernetes)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/pods/api-7d9c/terminate", nil)
	request.Header.Set(actionHeaderName, "terminate")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if kubernetes.terminated != "api-7d9c" {
		t.Fatalf("terminated Pod = %q, want %q", kubernetes.terminated, "api-7d9c")
	}
}
