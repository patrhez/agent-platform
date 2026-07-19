package jsonutil

import (
	"testing"
)

func TestMustJSONEncodesStruct(t *testing.T) {
	got := MustJSON(struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}{RunID: "run-1", Status: "running"})
	want := `{"run_id":"run-1","status":"running"}`
	if got != want {
		t.Fatalf("MustJSON() = %q, want %q", got, want)
	}
}

func TestMustJSONPanicsOnUnsupportedValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustJSON() did not panic for unsupported value")
		}
	}()
	MustJSON(make(chan int))
}
