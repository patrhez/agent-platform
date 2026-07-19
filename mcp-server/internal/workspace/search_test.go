package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchSkipsLocalDependencyAndBuildCaches(t *testing.T) {
	workspaceRoot := t.TempDir()
	repositoryRoot := filepath.Join(workspaceRoot, "repo")
	writeSearchTestFile(t, filepath.Join(repositoryRoot, "src", "server.go"), "nginx root configuration")
	writeSearchTestFile(t, filepath.Join(repositoryRoot, ".gocache", "cached.go"), "nginx cached result")
	writeSearchTestFile(t, filepath.Join(repositoryRoot, ".gomodcache", "module.go"), "nginx module result")
	writeSearchTestFile(t, filepath.Join(repositoryRoot, "node_modules", "package.js"), "nginx package result")
	writeSearchTestFile(t, filepath.Join(repositoryRoot, ".git", "config"), "nginx git result")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.Search(context.Background(), SearchInput{Repo: "repo", Query: "nginx"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("Search() matches = %d, want 1", len(result.Matches))
	}
	if result.Matches[0].Path != "src/server.go" {
		t.Errorf("Search() path = %q, want src/server.go", result.Matches[0].Path)
	}
}

func writeSearchTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
