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
	writeSearchTestFile(t, filepath.Join(repositoryRoot, ".venv", "lib", "site.py"), "nginx venv result")
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

func TestSearchMissingPathPrefixReturnsNotFound(t *testing.T) {
	workspaceRoot := t.TempDir()
	repositoryRoot := filepath.Join(workspaceRoot, "repo")
	writeSearchTestFile(t, filepath.Join(repositoryRoot, "src", "server.go"), "hello")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = service.Search(context.Background(), SearchInput{
		Repo: "repo", Query: "hello", PathPrefix: "missing_dir",
	})
	if err != ErrFileNotFound {
		t.Fatalf("Search() error = %v, want ErrFileNotFound", err)
	}
}

func TestSearchSupportsRegexAndCaseInsensitiveMatching(t *testing.T) {
	workspaceRoot := t.TempDir()
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "main.go"), "func StreamRunEvents() {}\nfunc other() {}")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	regex, err := service.Search(context.Background(), SearchInput{
		Repo: "repo", Query: `func\s+Stream\w+`, Regex: true,
	})
	if err != nil {
		t.Fatalf("Search(regex) error = %v", err)
	}
	if len(regex.Matches) != 1 || regex.Matches[0].Line != 1 {
		t.Errorf("Search(regex) matches = %#v, want line 1", regex.Matches)
	}
	insensitive, err := service.Search(context.Background(), SearchInput{
		Repo: "repo", Query: "streamrunevents", CaseInsensitive: true,
	})
	if err != nil {
		t.Fatalf("Search(caseInsensitive) error = %v", err)
	}
	if len(insensitive.Matches) != 1 {
		t.Errorf("Search(caseInsensitive) matches = %d, want 1", len(insensitive.Matches))
	}
	if _, err := service.Search(context.Background(), SearchInput{Repo: "repo", Query: "(", Regex: true}); err != ErrInvalidPath {
		t.Errorf("Search(bad regex) error = %v, want ErrInvalidPath", err)
	}
}

func TestSearchSkipsBinaryFiles(t *testing.T) {
	workspaceRoot := t.TempDir()
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "text.go"), "needle in text")
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "blob.bin"), "needle\x00binary")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.Search(context.Background(), SearchInput{Repo: "repo", Query: "needle"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Path != "text.go" {
		t.Errorf("Search() matches = %#v, want only text.go", result.Matches)
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
