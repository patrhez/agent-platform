package httpapi

import (
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/patrhez/agent-platform/backend/internal/domain"
)

func TestValidClientMessageID(t *testing.T) {
	if !validClientMessageID(ulid.Make().String()) {
		t.Fatal("ULID should be accepted")
	}
	if validClientMessageID("9ee85c0e-85b6-4fe3-b5e7-9d6e61d51370") {
		t.Fatal("UUID must be rejected")
	}
}

func TestParseFollowUpModeInAPI(t *testing.T) {
	mode, err := domain.ParseFollowUpMode("steer")
	if err != nil || mode != domain.FollowUpModeSteer {
		t.Fatalf("ParseFollowUpMode(steer) = %q, %v", mode, err)
	}
	if _, err := domain.ParseFollowUpMode("nope"); err == nil {
		t.Fatal("expected invalid mode error")
	}
}
