// Package config loads the release configuration shared by all service roles.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

const (
	defaultRedisURL          = "redis://redis:6379/0"
	defaultLLMBaseURL        = ""
	defaultLLMModel          = "internal-default"
	defaultWorkspaceMCPURL   = "http://workspace-mcp:8081/mcp"
	defaultWorkerID          = "worker-local"
	defaultWorkerConcurrency = 10
)

// Config contains the environment-derived configuration shared by service roles.
type Config struct {
	MySQLDSN             string
	APIMySQLDSN          string
	WorkerMySQLDSN       string
	MigrationMySQLDSN    string
	MaintenanceMySQLDSN  string
	RedisURL             string
	LLMBaseURL           string
	LLMAPIKey            string
	LLMModel             string
	ReplayEncryptionKey  string
	ReplayActiveKeyID    string
	ReplayEncryptionKeys string
	WorkspaceMCPURL      string
	WorkerID             string
	WorkerConcurrency    int
}

// Load reads the process configuration and validates values required by every role.
func Load() (Config, error) {
	config := Config{
		MySQLDSN:             os.Getenv("MYSQL_DSN"),
		RedisURL:             valueOrDefault("REDIS_URL", defaultRedisURL),
		LLMBaseURL:           valueOrDefault("LLM_BASE_URL", defaultLLMBaseURL),
		LLMAPIKey:            os.Getenv("LLM_API_KEY"),
		LLMModel:             valueOrDefault("LLM_MODEL", defaultLLMModel),
		ReplayEncryptionKey:  os.Getenv("REPLAY_ENCRYPTION_KEY"),
		ReplayActiveKeyID:    os.Getenv("REPLAY_ENCRYPTION_ACTIVE_KEY_ID"),
		ReplayEncryptionKeys: os.Getenv("REPLAY_ENCRYPTION_KEYS"),
		WorkspaceMCPURL:      valueOrDefault("WORKSPACE_MCP_URL", defaultWorkspaceMCPURL),
		WorkerID:             valueOrDefault("WORKER_ID", defaultWorkerID),
	}

	if config.MySQLDSN == "" {
		return Config{}, errors.New("MYSQL_DSN is required")
	}
	config.APIMySQLDSN = valueOrDefault("API_MYSQL_DSN", config.MySQLDSN)
	config.WorkerMySQLDSN = valueOrDefault("WORKER_MYSQL_DSN", config.MySQLDSN)
	config.MigrationMySQLDSN = valueOrDefault("MIGRATION_MYSQL_DSN", config.MySQLDSN)
	config.MaintenanceMySQLDSN = valueOrDefault("MAINTENANCE_MYSQL_DSN", config.MySQLDSN)

	concurrency, err := concurrencyFromEnv("WORKER_CONCURRENCY", defaultWorkerConcurrency)
	if err != nil {
		return Config{}, err
	}
	config.WorkerConcurrency = concurrency

	return config, nil
}

func valueOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}

func concurrencyFromEnv(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if value < 1 {
		return 0, fmt.Errorf("%s must be >= 1", key)
	}
	return value, nil
}
