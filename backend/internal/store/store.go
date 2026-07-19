// Package store persists Agent Platform records through generated GORM queries.
package store

import (
	"errors"

	"gorm.io/gorm"
)

var (
	// ErrConversationNotFound indicates a requested Conversation does not exist.
	ErrConversationNotFound = errors.New("Conversation not found")
	// ErrRunNotFound indicates a requested Run does not exist.
	ErrRunNotFound = errors.New("Run not found")
	// ErrArtifactNotFound indicates a requested Artifact does not exist.
	ErrArtifactNotFound = errors.New("Artifact not found")
	// ErrUnauthorized indicates the principal does not own the requested record.
	ErrUnauthorized = errors.New("Unauthorized")
	// ErrLeaseLost indicates another Worker owns the current Run execution token.
	ErrLeaseLost = errors.New("Run lease lost")
)

// Store owns durable Conversation and Run workflows.
type Store struct {
	database *gorm.DB
}

// New creates a Store backed by database.
func New(database *gorm.DB) *Store {
	return &Store{database: database}
}
