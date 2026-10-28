package spelling_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/spelling"
)

func TestDistance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want int
	}{
		{name: "should count no edit between the same words", a: "billing", b: "billing", want: 0},
		{name: "should count a missing letter as one edit", a: "billin", b: "billing", want: 1},
		{name: "should count a changed letter as one edit", a: "billong", b: "billing", want: 1},
		{name: "should count a swap of two neighbouring letters as one edit", a: "questoins", b: "questions", want: 1},
		{name: "should count a different case as an edit, since it compares bytes", a: "Billing", b: "billing", want: 1},
		{name: "should count every letter of a word against nothing", a: "", b: "sales", want: 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := spelling.Distance(tc.a, tc.b); got != tc.want {
				t.Errorf("Distance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
