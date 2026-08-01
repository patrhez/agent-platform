package logging

import "testing"

func TestPreviewTruncatesRunes(t *testing.T) {
	got := Preview("abcdefghij", 5)
	if got != "abcde…" {
		t.Fatalf("Preview = %q, want abcde…", got)
	}
}

func TestPreviewKeepsShortValues(t *testing.T) {
	got := Preview("short", 512)
	if got != "short" {
		t.Fatalf("Preview = %q, want short", got)
	}
}

func TestPreviewDefaultMax(t *testing.T) {
	runes := make([]rune, defaultPreviewRunes+10)
	for i := range runes {
		runes[i] = 'x'
	}
	got := Preview(string(runes), 0)
	want := string(runes[:defaultPreviewRunes]) + "…"
	if got != want {
		t.Fatalf("Preview = %q, want truncated preview", got)
	}
}
