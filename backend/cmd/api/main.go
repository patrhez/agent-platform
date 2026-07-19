package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/patrhez/agent-platform/backend/internal/config"
	"github.com/patrhez/agent-platform/backend/internal/database"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/events"
	"github.com/patrhez/agent-platform/backend/internal/httpapi"
	"github.com/patrhez/agent-platform/backend/internal/logging"
	"github.com/patrhez/agent-platform/backend/internal/pkg/async"
	"github.com/patrhez/agent-platform/backend/internal/runtime"
	"github.com/patrhez/agent-platform/backend/internal/store"
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
	logConfig, err := logging.ConfigFromEnv("api")
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
	database, err := database.Open(ctx, configuration.MySQLDSN)
	if err != nil {
		logger.Error(ctx, "open database failed", zap.Error(err))
		return err
	}
	definition, err := runtime.LoadDefinition(agentConfigPath(), configuration.WorkspaceMCPURL, configuration.LLMModel)
	if err != nil {
		logger.Error(ctx, "load Agent definition failed", zap.Error(err))
		return err
	}
	pins := domain.RunPins{
		AgentConfigVersion:  definition.Agent.Version,
		SkillsBundleVersion: definition.Agent.SkillsBundleVersion,
	}
	notifier, err := events.Open(ctx, configuration.RedisURL, logger)
	if err != nil {
		logger.Error(ctx, "open Redis notifier failed", zap.Error(err))
		return err
	}
	instanceID := apiInstanceID()
	logger = logger.With(zap.String("instance_id", instanceID))
	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: httpapi.New(store.New(database), pins, notifier, logger),
	}
	shutdown := make(chan error, 1)
	go func() {
		defer async.Recover(ctx, logger)
		<-ctx.Done()
		shutdown <- httpServer.Shutdown(context.Background())
	}()
	logger.Info(ctx, "api listening", zap.String("addr", httpServer.Addr))
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(ctx, "api serve failed", zap.Error(err))
		return err
	}
	return <-shutdown
}

func apiInstanceID() string {
	if instanceID := os.Getenv("INSTANCE_ID"); instanceID != "" {
		return instanceID
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

func agentConfigPath() string {
	if path := os.Getenv("AGENT_CONFIG_PATH"); path != "" {
		return path
	}
	return defaultAgentConfigPath
}
