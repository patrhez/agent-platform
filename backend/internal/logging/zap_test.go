package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv(envLogDir, "")
	t.Setenv(envLogLevel, "")
	t.Setenv(envLogMaxAgeDays, "")

	config, err := ConfigFromEnv("api")
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if config.Dir != defaultLogDir {
		t.Errorf("Dir = %q, want %q", config.Dir, defaultLogDir)
	}
	if config.Level != zapcore.InfoLevel {
		t.Errorf("Level = %v, want info", config.Level)
	}
	if config.MaxAgeDays != defaultMaxAgeDays {
		t.Errorf("MaxAgeDays = %d, want %d", config.MaxAgeDays, defaultMaxAgeDays)
	}
}

func TestOpenWritesJSONWithTraceID(t *testing.T) {
	dir := t.TempDir()
	logger, closeFn, err := Open(Config{
		Role:       "api",
		Dir:        dir,
		Level:      zapcore.InfoLevel,
		MaxAgeDays: 7,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = closeFn()
	})

	ctx := WithTraceID(context.Background(), "trace-1")
	logger.Info(ctx, "logging smoke", zap.String("run_id", "run-1"))
	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	link := filepath.Join(dir, "agent-api.log")
	file, err := os.Open(link)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", link, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("expected one log line: %v", scanner.Err())
	}
	var payload map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
		t.Fatalf("decode log JSON: %v; contents=%q", err, scanner.Text())
	}
	if payload["msg"] != "logging smoke" {
		t.Errorf("msg = %v, want logging smoke", payload["msg"])
	}
	if payload["role"] != "api" {
		t.Errorf("role = %v, want api", payload["role"])
	}
	if payload["trace_id"] != "trace-1" {
		t.Errorf("trace_id = %v, want trace-1", payload["trace_id"])
	}
	if payload["run_id"] != "run-1" {
		t.Errorf("run_id = %v, want run-1", payload["run_id"])
	}
}
