package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/patrhez/agent-platform/backend/internal/config"
	"github.com/patrhez/agent-platform/backend/internal/database"
	"github.com/patrhez/agent-platform/backend/internal/events"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
	"github.com/patrhez/agent-platform/backend/internal/store"
	"github.com/patrhez/agent-platform/backend/internal/worker"
	"go.uber.org/zap"
)

const defaultAgentConfigPath = "/etc/agent-platform/issue-troubleshooter.yaml"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logConfig, err := logging.ConfigFromEnv("worker")
	if err != nil {
		return err
	}
	logger, closeLogger, err := logging.Open(logConfig)
	if err != nil {
		return err
	}
	defer func() { _ = closeLogger() }()

	configuration, err := config.Load()
	if err != nil {
		logger.Error(ctx, "load configuration failed", zap.Error(err))
		return err
	}
	db, err := database.Open(ctx, configuration.MySQLDSN)
	if err != nil {
		logger.Error(ctx, "open database failed", zap.Error(err))
		return err
	}
	definition, err := runtime.LoadDefinition(agentConfigPath(), configuration.WorkspaceMCPURL, configuration.LLMModel)
	if err != nil {
		logger.Error(ctx, "load Agent definition failed", zap.Error(err))
		return err
	}
	runner, err := runtime.NewEinoRunner(definition, configuration.LLMBaseURL, configuration.LLMAPIKey)
	if err != nil {
		logger.Error(ctx, "create Eino runner failed", zap.Error(err))
		return err
	}
	notifier, err := events.Open(ctx, configuration.RedisURL, logger)
	if err != nil {
		logger.Error(ctx, "open Redis notifier failed", zap.Error(err))
		return err
	}
	service, err := worker.New(store.New(db), runner, notifier, logger, configuration.WorkerID)
	if err != nil {
		logger.Error(ctx, "create Worker failed", zap.Error(err))
		return err
	}
	logger.Info(ctx, "worker started", zap.String("worker_id", configuration.WorkerID))
	return service.Run(ctx)
}

func agentConfigPath() string {
	if path := os.Getenv("AGENT_CONFIG_PATH"); path != "" {
		return path
	}
	return defaultAgentConfigPath
}
