// Package config loads the release configuration shared by all service roles.
package config

import (
	"errors"
	"os"
)

const (
	defaultRedisURL        = "redis://redis:6379/0"
	defaultLLMBaseURL      = ""
	defaultLLMModel        = "internal-default"
	defaultWorkspaceMCPURL = "http://workspace-mcp:8081/mcp"
	defaultWorkerID        = "worker-local"
)

// Config contains the environment-derived configuration shared by service roles.
type Config struct {
	MySQLDSN        string
	RedisURL        string
	LLMBaseURL      string
	LLMAPIKey       string
	LLMModel        string
	WorkspaceMCPURL string
	WorkerID        string
}

// Load reads the process configuration and validates values required by every role.
func Load() (Config, error) {
	config := Config{
		MySQLDSN:        os.Getenv("MYSQL_DSN"),
		RedisURL:        valueOrDefault("REDIS_URL", defaultRedisURL),
		LLMBaseURL:      valueOrDefault("LLM_BASE_URL", defaultLLMBaseURL),
		LLMAPIKey:       os.Getenv("LLM_API_KEY"),
		LLMModel:        valueOrDefault("LLM_MODEL", defaultLLMModel),
		WorkspaceMCPURL: valueOrDefault("WORKSPACE_MCP_URL", defaultWorkspaceMCPURL),
		WorkerID:        valueOrDefault("WORKER_ID", defaultWorkerID),
	}

	if config.MySQLDSN == "" {
		return Config{}, errors.New("MYSQL_DSN is required")
	}

	return config, nil
}

func valueOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
