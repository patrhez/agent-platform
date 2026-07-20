package domain

import "testing"

func TestParseFollowUpMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input   string
		want    FollowUpMode
		wantErr bool
	}{
		{input: "", want: FollowUpModeQueue},
		{input: "queue", want: FollowUpModeQueue},
		{input: "QUEUE", want: FollowUpModeQueue},
		{input: "steer", want: FollowUpModeSteer},
		{input: " Steer ", want: FollowUpModeSteer},
		{input: "interrupt", wantErr: true},
	}
	for _, testCase := range cases {
		got, err := ParseFollowUpMode(testCase.input)
		if testCase.wantErr {
			if err == nil {
				t.Fatalf("ParseFollowUpMode(%q) error = nil, want error", testCase.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseFollowUpMode(%q) error = %v", testCase.input, err)
		}
		if got != testCase.want {
			t.Fatalf("ParseFollowUpMode(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}
