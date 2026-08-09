package main

import "testing"

func TestReplayGCBatchSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "default", want: defaultReplayGCBatchSize},
		{name: "explicit", raw: "25", want: 25},
		{name: "zero", raw: "0", wantErr: true},
		{name: "invalid", raw: "many", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := replayGCBatchSize(test.raw)
			if (err != nil) != test.wantErr {
				t.Fatalf("replayGCBatchSize(%q) error = %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("replayGCBatchSize(%q) = %d, want %d", test.raw, got, test.want)
			}
		})
	}
}
