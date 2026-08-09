// Command replay-gc removes expired provider continuation heads and orphan replay artifacts.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/config"
	"github.com/patrhez/agent-platform/backend/internal/database"
	"github.com/patrhez/agent-platform/backend/internal/store"
)

const defaultReplayGCBatchSize = 100

func main() {
	if err := run(context.Background()); err != nil {
		log.Printf("provider replay GC failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	configuration, err := config.Load()
	if err != nil {
		return fmt.Errorf("load replay GC configuration: %w", err)
	}
	databaseConnection, err := database.Open(ctx, configuration.MaintenanceMySQLDSN)
	if err != nil {
		return fmt.Errorf("open replay GC database: %w", err)
	}
	batchSize, err := replayGCBatchSize(os.Getenv("REPLAY_GC_BATCH_SIZE"))
	if err != nil {
		return err
	}
	result, err := store.New(databaseConnection).CollectProviderReplays(
		ctx, time.Now().UTC(), batchSize,
	)
	if err != nil {
		return err
	}
	log.Printf(
		"provider replay GC completed: deleted_heads=%d deleted_replays=%d",
		result.DeletedHeads,
		result.DeletedReplays,
	)
	return nil
}

func replayGCBatchSize(raw string) (int, error) {
	if raw == "" {
		return defaultReplayGCBatchSize, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("REPLAY_GC_BATCH_SIZE must be a positive integer")
	}
	return value, nil
}
