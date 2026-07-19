package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsMissingMySQLDSN(t *testing.T) {
	t.Setenv("MYSQL_DSN", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want an error for missing MYSQL_DSN")
	}
	if !strings.Contains(err.Error(), "MYSQL_DSN") {
		t.Fatalf("Load() error = %q, want it to mention MYSQL_DSN", err)
	}
}

func TestLoadUsesConnectionDefaults(t *testing.T) {
	t.Setenv("MYSQL_DSN", "demo:demo@tcp(mysql:3306)/agent_platform")
	t.Setenv("REDIS_URL", "")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("WORKSPACE_MCP_URL", "")
	t.Setenv("WORKER_ID", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.RedisURL != "redis://redis:6379/0" {
		t.Errorf("Load() RedisURL = %q, want default", got.RedisURL)
	}
	if got.WorkspaceMCPURL != "http://workspace-mcp:8081/mcp" {
		t.Errorf("Load() WorkspaceMCPURL = %q, want default", got.WorkspaceMCPURL)
	}
	if got.WorkerID != "worker-local" {
		t.Errorf("Load() WorkerID = %q, want default", got.WorkerID)
	}
	if got.LLMModel != "internal-default" {
		t.Errorf("Load() LLMModel = %q, want default", got.LLMModel)
	}
}
