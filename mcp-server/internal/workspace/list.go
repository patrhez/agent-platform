package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ListRepositoriesInput is the empty input for workspace.list_repositories.
type ListRepositoriesInput struct{}

// ListRepositoriesOutput contains safe repository aliases without host paths.
type ListRepositoriesOutput struct {
	Repositories []string `json:"repositories"`
}

// ListRepositories returns visible direct child directories of the Workspace root.
func (service *Service) ListRepositories(ctx context.Context, _ ListRepositoriesInput) (ListRepositoriesOutput, error) {
	if err := ctx.Err(); err != nil {
		return ListRepositoriesOutput{}, fmt.Errorf("list repositories context: %w", err)
	}
	entries, err := os.ReadDir(service.root)
	if err != nil {
		return ListRepositoriesOutput{}, fmt.Errorf("list Workspace root: %w", err)
	}
	repositories := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ListRepositoriesOutput{}, fmt.Errorf("list repositories context: %w", err)
		}
		if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		repositories = append(repositories, entry.Name())
	}
	return ListRepositoriesOutput{Repositories: repositories}, nil
}
