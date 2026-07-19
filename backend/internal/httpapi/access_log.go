package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/logging"
	"go.uber.org/zap"
)

const maxLoggedBodyBytes = 4 << 10

type accessLogResponseWriter struct {
	http.ResponseWriter
	status      int
	body        bytes.Buffer
	truncated   bool
	skipBody    bool
	contentType string
}

func (writer *accessLogResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.contentType = writer.Header().Get("Content-Type")
	writer.skipBody = shouldSkipResponseBody(writer.contentType)
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *accessLogResponseWriter) Write(contents []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	writer.captureBody(contents)
	return writer.ResponseWriter.Write(contents)
}

func (writer *accessLogResponseWriter) Flush() {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *accessLogResponseWriter) captureBody(contents []byte) {
	if writer.skipBody || len(contents) == 0 {
		return
	}
	remaining := maxLoggedBodyBytes - writer.body.Len()
	if remaining <= 0 {
		writer.truncated = true
		return
	}
	if len(contents) > remaining {
		_, _ = writer.body.Write(contents[:remaining])
		writer.truncated = true
		return
	}
	_, _ = writer.body.Write(contents)
}

func (writer *accessLogResponseWriter) loggedBody() string {
	if writer.skipBody || writer.body.Len() == 0 {
		return ""
	}
	body := writer.body.String()
	if writer.truncated {
		return body + "...(truncated)"
	}
	return body
}

func accessLog(next http.Handler, logger logging.Logger) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestBody, request := captureRequestBody(request)
		writer := &accessLogResponseWriter{ResponseWriter: responseWriter}
		next.ServeHTTP(writer, request)
		if writer.status == 0 {
			writer.status = http.StatusOK
		}

		duration := time.Since(startedAt).Round(time.Millisecond)
		fields := []zap.Field{
			zap.String("method", request.Method),
			zap.String("path", request.URL.Path),
			zap.Int("status", writer.status),
			zap.Duration("duration", duration),
			zap.Int64("duration_ms", duration.Milliseconds()),
		}
		if requestBody != "" {
			fields = append(fields, zap.String("request_body", requestBody))
		}
		if responseBody := writer.loggedBody(); responseBody != "" {
			fields = append(fields, zap.String("response_body", responseBody))
		}
		logger.Info(request.Context(), "http_request", fields...)
	})
}

func captureRequestBody(request *http.Request) (string, *http.Request) {
	if request.Body == nil || request.Method == http.MethodGet || request.Method == http.MethodHead {
		return "", request
	}
	contents, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil {
		request.Body = io.NopCloser(bytes.NewReader(nil))
		return "", request
	}
	request.Body = io.NopCloser(bytes.NewReader(contents))
	if len(contents) == 0 {
		return "", request
	}
	if len(contents) > maxLoggedBodyBytes {
		return string(contents[:maxLoggedBodyBytes]) + "...(truncated)", request
	}
	return string(contents), request
}

func shouldSkipResponseBody(contentType string) bool {
	normalized := strings.ToLower(contentType)
	return strings.Contains(normalized, "text/event-stream") ||
		strings.Contains(normalized, "application/octet-stream") ||
		strings.HasPrefix(normalized, "image/") ||
		strings.HasPrefix(normalized, "audio/") ||
		strings.HasPrefix(normalized, "video/")
}
