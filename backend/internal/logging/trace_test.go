package logging

import (
	"context"
	"testing"
)

func TestEnsureTraceIDReusesExisting(t *testing.T) {
	ctx := WithTraceID(context.Background(), "existing")
	next, traceID := EnsureTraceID(ctx)
	if traceID != "existing" {
		t.Fatalf("traceID = %q, want existing", traceID)
	}
	if TraceID(next) != "existing" {
		t.Fatalf("TraceID(next) = %q, want existing", TraceID(next))
	}
}

func TestEnsureTraceIDCreatesULID(t *testing.T) {
	next, traceID := EnsureTraceID(context.Background())
	if len(traceID) != 26 {
		t.Fatalf("traceID length = %d, want 26", len(traceID))
	}
	if TraceID(next) != traceID {
		t.Fatalf("context trace id mismatch")
	}
}
