package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/patrhez/agent-platform/backend/internal/logging"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestAccessLogRecordsSafeRequestMetadata(t *testing.T) {
	payload := serveAccessLog(t, http.MethodGet, "/healthz?secret=hidden", nil, func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.WriteHeader(http.StatusNoContent)
	})

	if payload["msg"] != "http_request" {
		t.Errorf("msg = %v, want http_request", payload["msg"])
	}
	if payload["method"] != "GET" || payload["path"] != "/healthz" {
		t.Errorf("access log = %#v, want method/path only", payload)
	}
	if _, found := payload["duration_ms"]; !found {
		t.Errorf("access log missing duration_ms: %#v", payload)
	}
	if _, found := payload["secret"]; found {
		t.Errorf("access log contains query secret: %#v", payload)
	}
}

func TestAccessLogRecordsRequestAndResponseBodies(t *testing.T) {
	payload := serveAccessLog(
		t,
		http.MethodPost,
		"/api/v1/conversations",
		[]byte(`{"content":"hello world"}`),
		func(responseWriter http.ResponseWriter, request *http.Request) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("handler read body: %v", err)
			}
			if string(body) != `{"content":"hello world"}` {
				t.Fatalf("handler body = %q, want original JSON", body)
			}
			responseWriter.Header().Set("Content-Type", "application/json")
			responseWriter.WriteHeader(http.StatusAccepted)
			_, _ = responseWriter.Write([]byte(`{"runId":"run-1"}`))
		},
	)

	if payload["request_body"] != `{"content":"hello world"}` {
		t.Errorf("request_body = %v", payload["request_body"])
	}
	if payload["response_body"] != `{"runId":"run-1"}` {
		t.Errorf("response_body = %v", payload["response_body"])
	}
	if payload["status"] != float64(http.StatusAccepted) {
		t.Errorf("status = %v, want %d", payload["status"], http.StatusAccepted)
	}
}

func TestAccessLogSkipsSSEResponseBody(t *testing.T) {
	payload := serveAccessLog(t, http.MethodGet, "/api/v1/runs/run-1/events", nil, func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Content-Type", "text/event-stream")
		responseWriter.WriteHeader(http.StatusOK)
		_, _ = responseWriter.Write([]byte("event: progress.updated\ndata: {}\n\n"))
	})

	if _, found := payload["response_body"]; found {
		t.Errorf("SSE response_body should be omitted: %#v", payload)
	}
}

func serveAccessLog(
	t *testing.T,
	method string,
	path string,
	body []byte,
	handler http.HandlerFunc,
) map[string]any {
	t.Helper()
	var output bytes.Buffer
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(&output),
		zapcore.InfoLevel,
	)
	logger := &testZapLogger{base: zap.New(core)}
	wrapped := accessLog(handler, logger)

	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	wrapped.ServeHTTP(httptest.NewRecorder(), request)

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &payload); err != nil {
		t.Fatalf("decode access log: %v; contents=%q", err, output.String())
	}
	return payload
}

type testZapLogger struct {
	base *zap.Logger
}

func (logger *testZapLogger) Debug(_ context.Context, msg string, fields ...zap.Field) {
	logger.base.Debug(msg, fields...)
}
func (logger *testZapLogger) Info(_ context.Context, msg string, fields ...zap.Field) {
	logger.base.Info(msg, fields...)
}
func (logger *testZapLogger) Warn(_ context.Context, msg string, fields ...zap.Field) {
	logger.base.Warn(msg, fields...)
}
func (logger *testZapLogger) Error(_ context.Context, msg string, fields ...zap.Field) {
	logger.base.Error(msg, fields...)
}
func (logger *testZapLogger) With(fields ...zap.Field) logging.Logger {
	return &testZapLogger{base: logger.base.With(fields...)}
}
func (logger *testZapLogger) Sync() error { return logger.base.Sync() }
