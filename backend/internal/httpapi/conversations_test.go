package httpapi

import (
	"testing"

	"github.com/oklog/ulid/v2"
)

func TestValidClientMessageID(t *testing.T) {
	if !validClientMessageID(ulid.Make().String()) {
		t.Fatal("ULID should be accepted")
	}
	if validClientMessageID("9ee85c0e-85b6-4fe3-b5e7-9d6e61d51370") {
		t.Fatal("UUID must be rejected")
	}
}
