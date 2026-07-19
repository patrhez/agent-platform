package workspace

import (
	"context"
	"testing"
)

func listTestService(t *testing.T) *Service {
	t.Helper()
	workspaceRoot := t.TempDir()
	writeSearchTestFile(t, workspaceRoot+"/repo/README.md", "readme")
	writeSearchTestFile(t, workspaceRoot+"/repo/src/main.go", "package main")
	writeSearchTestFile(t, workspaceRoot+"/repo/src/nested/worker.go", "package nested")
	writeSearchTestFile(t, workspaceRoot+"/repo/node_modules/pkg/index.js", "ignored")
	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

func TestListEntriesReturnsImmediateChildrenByDefault(t *testing.T) {
	service := listTestService(t)
	output, err := service.ListEntries(context.Background(), ListEntriesInput{Repo: "repo"})
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	paths := map[string]string{}
	for _, entry := range output.Entries {
		paths[entry.Path] = entry.Type
	}
	if paths["README.md"] != "file" || paths["src"] != "dir" {
		t.Errorf("ListEntries() = %#v, want README.md file and src dir", paths)
	}
	if _, found := paths["src/main.go"]; found {
		t.Error("ListEntries() descended below depth 1 by default")
	}
	if _, found := paths["node_modules"]; found {
		t.Error("ListEntries() listed an excluded directory")
	}
}

func TestListEntriesFindsFilesByGlobRecursively(t *testing.T) {
	service := listTestService(t)
	output, err := service.ListEntries(context.Background(), ListEntriesInput{Repo: "repo", Glob: "*.go"})
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(output.Entries) != 2 {
		t.Fatalf("ListEntries() entries = %#v, want two Go files", output.Entries)
	}
	for _, entry := range output.Entries {
		if entry.Type != "file" {
			t.Errorf("glob result %q has type %q, want file", entry.Path, entry.Type)
		}
	}
}

func TestListEntriesTruncatesAtLimit(t *testing.T) {
	service := listTestService(t)
	output, err := service.ListEntries(context.Background(), ListEntriesInput{
		Repo: "repo", MaxDepth: 8, MaxEntries: 1,
	})
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(output.Entries) != 1 || !output.Truncated {
		t.Errorf("ListEntries() = %d entries truncated=%v, want 1 entry truncated", len(output.Entries), output.Truncated)
	}
}

func TestListEntriesRejectsInvalidLimits(t *testing.T) {
	service := listTestService(t)
	if _, err := service.ListEntries(context.Background(), ListEntriesInput{Repo: "repo", MaxEntries: 9999}); err != ErrResultLimitExceeded {
		t.Errorf("MaxEntries error = %v, want ErrResultLimitExceeded", err)
	}
	if _, err := service.ListEntries(context.Background(), ListEntriesInput{Repo: "repo", MaxDepth: 99}); err != ErrResultLimitExceeded {
		t.Errorf("MaxDepth error = %v, want ErrResultLimitExceeded", err)
	}
	if _, err := service.ListEntries(context.Background(), ListEntriesInput{Repo: "repo", Glob: "["}); err != ErrInvalidPath {
		t.Errorf("bad glob error = %v, want ErrInvalidPath", err)
	}
}
