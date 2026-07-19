// Command migrate applies the backend's code-first GORM schema to MySQL.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/patrhez/agent-platform/backend/internal/config"
	"github.com/patrhez/agent-platform/backend/internal/database"
	"github.com/patrhez/agent-platform/backend/internal/model"
)

const migrationTimeout = 30 * time.Second

func main() {
	configuration, err := config.Load()
	if err != nil {
		log.Printf("load migration configuration: %v", err)
		os.Exit(1)
	}

	migrationContext, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()
	databaseConnection, err := database.Open(migrationContext, configuration.MySQLDSN)
	if err != nil {
		log.Printf("open migration database: %v", err)
		os.Exit(1)
	}
	if err := databaseConnection.WithContext(migrationContext).AutoMigrate(model.All()...); err != nil {
		log.Printf("migrate database schema: %v", err)
		os.Exit(1)
	}
}
