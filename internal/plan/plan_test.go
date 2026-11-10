package plan_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestQuestion_Mention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question plan.Question
		want     string
	}{
		{name: "should quote an id the user chose", question: plan.Question{ID: "team", Origin: plan.OriginFlag}, want: "'team'"},
		{name: "should call the positional question the question", question: plan.Question{ID: plan.PositionalID, Origin: plan.OriginPositional}, want: "the question"},
		{
			name:     "should quote a body question named answer, since the rule follows the origin",
			question: plan.Question{ID: plan.PositionalID, Origin: plan.OriginBody}, want: "'answer'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.question.Mention(); got != tc.want {
				t.Errorf("Mention() = %q, want %q", got, tc.want)
			}
		})
	}
}
