package httpapi

import (
	"strings"
	"testing"
)

func TestNormalizeDeleteConversationIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ids     []string
		want    []string
		wantErr string
	}{
		{
			name: "deduplicates preserving order",
			ids:  []string{"a", "b", "a"},
			want: []string{"a", "b"},
		},
		{
			name:    "rejects empty slice",
			ids:     nil,
			wantErr: "ids must be a non-empty array",
		},
		{
			name:    "rejects empty string",
			ids:     []string{"a", ""},
			wantErr: "ids must not contain empty values",
		},
		{
			name:    "rejects more than 100 unique ids",
			ids:     makeUniqueIDs(101),
			wantErr: "ids must contain at most 100 unique values",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeDeleteConversationIDs(testCase.ids)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(testCase.want) {
				t.Fatalf("got %#v, want %#v", got, testCase.want)
			}
			for index := range testCase.want {
				if got[index] != testCase.want[index] {
					t.Fatalf("got %#v, want %#v", got, testCase.want)
				}
			}
		})
	}
}

func makeUniqueIDs(count int) []string {
	ids := make([]string, count)
	for index := 0; index < count; index++ {
		ids[index] = string(rune('a'+(index%26))) + strings.Repeat("x", index/26+1)
	}
	return ids
}
