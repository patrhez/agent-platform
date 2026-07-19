package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadStreamsRequestedRangeWithTotalLines(t *testing.T) {
	workspaceRoot := t.TempDir()
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "main.go"), "one\ntwo\nthree\nfour")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := service.Read(context.Background(), ReadInput{
		Repo: "repo", Path: "main.go", StartLine: 2, EndLine: 3,
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if output.TotalLines != 4 {
		t.Errorf("Read() totalLines = %d, want 4", output.TotalLines)
	}
	if len(output.Lines) != 2 || output.Lines[0].Text != "two" || output.Lines[1].Number != 3 {
		t.Errorf("Read() lines = %#v", output.Lines)
	}
}

func TestReadClampsRangeBeyondEndOfFile(t *testing.T) {
	workspaceRoot := t.TempDir()
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "short.txt"), "only line")

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := service.Read(context.Background(), ReadInput{
		Repo: "repo", Path: "short.txt", StartLine: 5, EndLine: 10,
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(output.Lines) != 0 || output.TotalLines != 1 {
		t.Errorf("Read() = %d lines, totalLines %d; want 0 lines, 1 total", len(output.Lines), output.TotalLines)
	}
}

func TestReadTruncatesVeryLongLines(t *testing.T) {
	workspaceRoot := t.TempDir()
	longLine := strings.Repeat("a", maxLineBytes+100)
	writeSearchTestFile(t, filepath.Join(workspaceRoot, "repo", "minified.js"), longLine)

	service, err := New(workspaceRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	output, err := service.Read(context.Background(), ReadInput{
		Repo: "repo", Path: "minified.js", StartLine: 1, EndLine: 1,
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(output.Lines) != 1 || !strings.HasSuffix(output.Lines[0].Text, "…") {
		t.Fatalf("Read() long line = %d lines", len(output.Lines))
	}
	if len(output.Lines[0].Text) > maxLineBytes+len("…") {
		t.Errorf("Read() line length = %d, want <= %d", len(output.Lines[0].Text), maxLineBytes+len("…"))
	}
}
