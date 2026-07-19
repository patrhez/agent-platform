// Command ha-dashboard serves the localhost-only Kubernetes HA experiment UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/hadashboard"
)

const (
	defaultAddress   = "127.0.0.1:8090"
	defaultNamespace = "agent-platform-ha"
	defaultContext   = "k3d-agent-platform-ha"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(os.Stderr, "HA dashboard failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	kubernetes, err := hadashboard.NewKubectlClient(
		valueOrDefault("HA_NAMESPACE", defaultNamespace),
		valueOrDefault("HA_KUBE_CONTEXT", defaultContext),
	)
	if err != nil {
		return err
	}
	handler, err := hadashboard.NewHandler(kubernetes)
	if err != nil {
		return fmt.Errorf("create HA dashboard handler: %w", err)
	}
	server := &http.Server{
		Addr:              valueOrDefault("HA_DASHBOARD_ADDR", defaultAddress),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	shutdown := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdown <- server.Shutdown(shutdownContext)
	}()
	_, _ = fmt.Fprintf(os.Stdout, "HA dashboard: http://%s\n", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HA dashboard: %w", err)
	}
	return <-shutdown
}

func valueOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
