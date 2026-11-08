package output_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestVerdict_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		verdict output.Verdict
		want    string
	}{
		{name: "should name a record answered with no gate", verdict: output.Answered, want: "answered"},
		{name: "should name a pass", verdict: output.Pass, want: "pass"},
		{name: "should name a fail", verdict: output.Fail, want: "fail"},
		{name: "should name an unsure", verdict: output.Unsure, want: "unsure"},
		{name: "should name an error", verdict: output.Error, want: "error"},
		{name: "should show the number of a verdict it does not know", verdict: output.Verdict(9), want: "Verdict(9)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.verdict.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
