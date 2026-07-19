package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListRepositoriesReturnsSortedVisibleDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"zeta", "alpha", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("create directory %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("not a repository"), 0o644); err != nil {
		t.Fatalf("create regular file: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "alpha"), filepath.Join(root, "linked")); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}

	service, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.ListRepositories(context.Background(), ListRepositoriesInput{})
	if err != nil {
		t.Fatalf("ListRepositories() error = %v", err)
	}
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(result.Repositories, want) {
		t.Errorf("ListRepositories() = %v, want %v", result.Repositories, want)
	}
}
