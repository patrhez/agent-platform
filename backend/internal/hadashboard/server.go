package hadashboard

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"time"
)

const actionHeaderName = "X-HA-Dashboard-Action"

//go:embed static/*
var staticFiles embed.FS

// NewHandler creates the local HA dashboard HTTP handler.
func NewHandler(kubernetes Kubernetes) (http.Handler, error) {
	if kubernetes == nil {
		return nil, errors.New("Kubernetes client is required")
	}
	assets, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, err
	}
	server := &server{kubernetes: kubernetes}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/pods", server.listPods)
	mux.HandleFunc("GET /api/pods/{podName}/logs", server.logs)
	mux.HandleFunc("POST /api/pods/{podName}/terminate", server.terminate)
	mux.Handle("/", http.FileServer(http.FS(assets)))
	return mux, nil
}

type server struct {
	kubernetes Kubernetes
}

func (server *server) listPods(responseWriter http.ResponseWriter, request *http.Request) {
	ctx, cancel := contextWithTimeout(request, 5*time.Second)
	defer cancel()
	pods, err := server.kubernetes.ListPods(ctx)
	if err != nil {
		writeJSONError(responseWriter, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"pods": pods, "observedAt": time.Now().UTC()})
}

func (server *server) logs(responseWriter http.ResponseWriter, request *http.Request) {
	tailLines := 200
	if value := request.URL.Query().Get("tail"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeJSONError(responseWriter, http.StatusBadRequest, errors.New("tail must be an integer"))
			return
		}
		tailLines = parsed
	}
	ctx, cancel := contextWithTimeout(request, 10*time.Second)
	defer cancel()
	logs, err := server.kubernetes.Logs(ctx, request.PathValue("podName"), tailLines)
	if err != nil {
		writeJSONError(responseWriter, http.StatusBadRequest, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]string{"logs": logs})
}

func (server *server) terminate(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Header.Get(actionHeaderName) != "terminate" {
		writeJSONError(responseWriter, http.StatusForbidden, errors.New("missing HA action confirmation header"))
		return
	}
	ctx, cancel := contextWithTimeout(request, 10*time.Second)
	defer cancel()
	podName := request.PathValue("podName")
	if err := server.kubernetes.Terminate(ctx, podName); err != nil {
		writeJSONError(responseWriter, http.StatusBadRequest, err)
		return
	}
	writeJSON(responseWriter, http.StatusAccepted, map[string]string{"pod": podName, "status": "terminating"})
}

func contextWithTimeout(request *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), timeout)
}

func writeJSON(responseWriter http.ResponseWriter, status int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(status)
	// A write failure means the local browser disconnected after the response was committed.
	_ = json.NewEncoder(responseWriter).Encode(value)
}

func writeJSONError(responseWriter http.ResponseWriter, status int, err error) {
	writeJSON(responseWriter, status, map[string]string{"error": err.Error()})
}
