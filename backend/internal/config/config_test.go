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

func TestLoadWorkerConcurrencyDefault(t *testing.T) {
	t.Setenv("MYSQL_DSN", "demo:demo@tcp(mysql:3306)/agent_platform")
	t.Setenv("WORKER_CONCURRENCY", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.WorkerConcurrency != 10 {
		t.Errorf("WorkerConcurrency = %d, want 10", got.WorkerConcurrency)
	}
}

func TestLoadUsesRoleSpecificDatabaseDSNs(t *testing.T) {
	t.Setenv("MYSQL_DSN", "shared")
	t.Setenv("API_MYSQL_DSN", "api")
	t.Setenv("WORKER_MYSQL_DSN", "worker")
	t.Setenv("MIGRATION_MYSQL_DSN", "migration")
	t.Setenv("MAINTENANCE_MYSQL_DSN", "maintenance")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.APIMySQLDSN != "api" || got.WorkerMySQLDSN != "worker" ||
		got.MigrationMySQLDSN != "migration" || got.MaintenanceMySQLDSN != "maintenance" {
		t.Fatal("role-specific database DSNs were not selected")
	}
}

func TestLoadWorkerConcurrencyExplicit(t *testing.T) {
	t.Setenv("MYSQL_DSN", "demo:demo@tcp(mysql:3306)/agent_platform")
	t.Setenv("WORKER_CONCURRENCY", "3")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.WorkerConcurrency != 3 {
		t.Errorf("WorkerConcurrency = %d, want 3", got.WorkerConcurrency)
	}
}

func TestLoadWorkerConcurrencyRejectsInvalid(t *testing.T) {
	t.Setenv("MYSQL_DSN", "demo:demo@tcp(mysql:3306)/agent_platform")

	cases := []struct {
		name  string
		value string
	}{
		{name: "zero", value: "0"},
		{name: "negative", value: "-1"},
		{name: "non-integer", value: "abc"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("WORKER_CONCURRENCY", testCase.value)
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() error = nil, want error for WORKER_CONCURRENCY=%q", testCase.value)
			}
		})
	}
}
