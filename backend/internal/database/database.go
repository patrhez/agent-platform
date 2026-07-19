// Package database opens the GORM database connection used by backend service roles.
package database

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	maxOpenConnections = 20
	maxIdleConnections = 10
	connectionMaxAge   = 30 * time.Minute
)

// Open connects to MySQL, verifies reachability with ctx, and configures bounded pooling.
func Open(ctx context.Context, dsn string) (*gorm.DB, error) {
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: newGORMLogger(os.Stderr)})
	if err != nil {
		return nil, fmt.Errorf("open MySQL database: %w", err)
	}

	sqlDatabase, err := database.DB()
	if err != nil {
		return nil, fmt.Errorf("get MySQL connection pool: %w", err)
	}
	sqlDatabase.SetMaxOpenConns(maxOpenConnections)
	sqlDatabase.SetMaxIdleConns(maxIdleConnections)
	sqlDatabase.SetConnMaxLifetime(connectionMaxAge)
	if err := sqlDatabase.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping MySQL database: %w", err)
	}

	return database, nil
}

func newGORMLogger(output io.Writer) logger.Interface {
	return logger.New(log.New(output, "", log.LstdFlags), logger.Config{
		SlowThreshold:             time.Second,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  false,
	})
}
