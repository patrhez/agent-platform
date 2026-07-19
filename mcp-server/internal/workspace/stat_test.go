package workspace

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStatReturnsFileMetadataAndLineCount(t *testing.T) {
	workspaceRoot := t.TempDir()
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "main.go"), "one\ntwo\nthree")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := service.Stat(context.Background(), StatInput{Repo: "repo", Path: "main.go"})
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if output.Type != "file" || output.TotalLines != 3 || output.SizeBytes == 0 {
		t.Errorf("Stat() = %#v, want file with 3 lines", output)
	}
}

func TestStatReturnsDirectoryType(t *testing.T) {
	workspaceRoot := t.TempDir()
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "src", "main.go"), "package main")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := service.Stat(context.Background(), StatInput{Repo: "repo", Path: "src"})
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if output.Type != "dir" || output.TotalLines != 0 || output.SizeBytes != 0 {
		t.Errorf("Stat() = %#v, want directory metadata", output)
	}
}

func TestStatMissingPathReturnsNotFound(t *testing.T) {
	workspaceRoot := t.TempDir()
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "main.go"), "ok")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := service.Stat(context.Background(), StatInput{Repo: "repo", Path: "missing.go"}); err != ErrFileNotFound {
		t.Errorf("Stat() error = %v, want ErrFileNotFound", err)
	}
}
