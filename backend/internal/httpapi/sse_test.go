package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestEventCursorPrefersLastEventID(t *testing.T) {
	testCases := []struct {
		name      string
		query     string
		header    string
		want      int64
		wantError bool
	}{
		{name: "reconnect header", query: "0", header: "7", want: 7},
		{name: "initial query", query: "3", want: 3},
		{name: "empty cursor", want: 0},
		{name: "invalid reconnect header", query: "0", header: "invalid", wantError: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/events?after="+testCase.query, nil)
			if testCase.header != "" {
				request.Header.Set("Last-Event-ID", testCase.header)
			}
			cursor, err := eventCursor(request)
			if testCase.wantError {
				if err == nil {
					t.Fatal("eventCursor() error = nil, want validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("eventCursor() error = %v", err)
			}
			if cursor != testCase.want {
				t.Errorf("eventCursor() = %d, want %d", cursor, testCase.want)
			}
		})
	}
}
