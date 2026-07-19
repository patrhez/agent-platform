// Package workspace provides read-only repository access for Workspace MCP tools.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const defaultRoot = "/workspace/repos"

var (
	// ErrInvalidRepo indicates a repository alias is not a simple directory name.
	ErrInvalidRepo = errors.New("invalid repository alias")
	// ErrInvalidPath indicates a requested path is malformed.
	ErrInvalidPath = errors.New("invalid repository path")
	// ErrPathOutsideWorkspace indicates a path or symlink leaves its repository root.
	ErrPathOutsideWorkspace = errors.New("path outside workspace")
	// ErrRepositoryNotFound indicates no repository exists for an alias.
	ErrRepositoryNotFound = errors.New("repository not found")
	// ErrFileNotFound indicates a requested file does not exist.
	ErrFileNotFound = errors.New("file not found")
	// ErrResultLimitExceeded indicates an input exceeds a documented result limit.
	ErrResultLimitExceeded = errors.New("result limit exceeded")
)

// Service resolves and reads only files under a configured workspace root.
type Service struct {
	root string
}

// New creates a Service rooted at root, or at the container workspace when root is empty.
func New(root string) (*Service, error) {
	if root == "" {
		root = defaultRoot
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %s is not a directory", resolvedRoot)
	}
	return &Service{root: resolvedRoot}, nil
}

func (service *Service) resolveRepository(alias string) (string, error) {
	if alias == "" || filepath.Base(alias) != alias || alias == "." {
		return "", ErrInvalidRepo
	}
	repositoryRoot, err := filepath.EvalSymlinks(filepath.Join(service.root, alias))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrRepositoryNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve repository %s: %w", alias, err)
	}
	if err := ensureWithinRoot(service.root, repositoryRoot); err != nil {
		return "", err
	}
	info, err := os.Stat(repositoryRoot)
	if err != nil {
		return "", fmt.Errorf("stat repository %s: %w", alias, err)
	}
	if !info.IsDir() {
		return "", ErrRepositoryNotFound
	}
	return repositoryRoot, nil
}

func resolveFile(repositoryRoot string, relativePath string) (string, error) {
	if relativePath == "" || filepath.IsAbs(relativePath) {
		return "", ErrInvalidPath
	}
	cleanPath := filepath.Clean(relativePath)
	if cleanPath == "." || hasParentReference(cleanPath) {
		return "", ErrPathOutsideWorkspace
	}
	candidate := filepath.Join(repositoryRoot, cleanPath)
	if err := ensureWithinRoot(repositoryRoot, candidate); err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrFileNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve file %s: %w", relativePath, err)
	}
	if err := ensureWithinRoot(repositoryRoot, resolvedPath); err != nil {
		return "", err
	}
	return resolvedPath, nil
}

func ensureWithinRoot(root string, candidate string) error {
	relativePath, err := filepath.Rel(root, candidate)
	if err != nil {
		return fmt.Errorf("compare workspace paths: %w", err)
	}
	if hasParentReference(relativePath) {
		return ErrPathOutsideWorkspace
	}
	return nil
}

func hasParentReference(path string) bool {
	return path == ".." || len(path) > 3 && path[:3] == ".."+string(filepath.Separator)
}
