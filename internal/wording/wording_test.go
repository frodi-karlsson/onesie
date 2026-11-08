package wording_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/wording"
)

func TestPlural(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int
		want  string
	}{
		{name: "should write the singular for one", count: 1, want: "1 retry"},
		{name: "should write the plural for zero", count: 0, want: "0 retries"},
		{name: "should write the plural for more than one", count: 3, want: "3 retries"},
		{name: "should write the plural for a negative count", count: -1, want: "-1 retries"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := wording.Plural(tc.count, "retry", "retries"); got != tc.want {
				t.Errorf("Plural(%d) = %q, want %q", tc.count, got, tc.want)
			}
		})
	}
}
