package cli

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestJudge(t *testing.T) {
	t.Parallel()

	const (
		holds = "answer.value > 0.5"
		fails = "answer.value > 0.95"
	)

	tests := []struct {
		name          string
		gate          string
		abstain       string
		wantFailed    bool
		wantAbstained bool
	}{
		{
			name: "should be yes when the assertion holds and the abstain expression holds too",
			gate: holds, abstain: holds,
		},
		{
			name: "should be yes when the assertion holds and the abstain expression is false",
			gate: holds, abstain: fails,
		},
		{
			name: "should be unsure when the assertion is false and the abstain expression holds",
			gate: fails, abstain: holds, wantAbstained: true,
		},
		{
			name: "should be no when both are false",
			gate: fails, abstain: fails, wantFailed: true,
		},
		{
			name: "should be no when there is no abstain expression and the assertion is false",
			gate: fails, wantFailed: true,
		},
		{
			name: "should be yes when there is neither an assertion nor an abstain expression",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			record := output.Record{Answers: []output.Named{
				{ID: "answer", Answer: &answer.Answer{Value: 0.9}},
			}}

			stats := &collector{}

			got := judge(parsed(t, tc.gate), parsed(t, tc.abstain), record, stats)

			if got.AssertFailed != tc.wantFailed {
				t.Errorf("AssertFailed = %v, want %v", got.AssertFailed, tc.wantFailed)
			}

			if got.Abstained != tc.wantAbstained {
				t.Errorf("Abstained = %v, want %v", got.Abstained, tc.wantAbstained)
			}

			snapshot := stats.snapshot(0, 0)

			if want := count(tc.wantFailed); snapshot.FalseAsserts != want {
				t.Errorf("FalseAsserts = %d, want %d", snapshot.FalseAsserts, want)
			}

			if want := count(tc.wantAbstained); snapshot.Abstains != want {
				t.Errorf("Abstains = %d, want %d", snapshot.Abstains, want)
			}
		})
	}
}

func parsed(t *testing.T, source string) *assert.Expr {
	t.Helper()

	if source == "" {
		return nil
	}

	expr, err := assert.Parse(source)
	if err != nil {
		t.Fatalf("Parse(%q): %v", source, err)
	}

	return expr
}

func count(counted bool) int {
	if counted {
		return 1
	}

	return 0
}

func TestStreamResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		failed       int
		falseAsserts int
		abstains     int
		want         int
	}{
		{name: "should exit six when a failed record sits beside the rest", failed: 1, falseAsserts: 1, abstains: 1, want: ExitRecords},
		{name: "should exit one when a false assertion sits beside an abstain", falseAsserts: 1, abstains: 1, want: ExitRejected},
		{name: "should exit seven when an abstain is all the stream holds", abstains: 2, want: ExitAbstain},
		{name: "should exit zero when the stream holds none of these", want: ExitOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := streamResult(engine.Result{Failed: tc.failed}, tc.falseAsserts, tc.abstains)

			if got := Classify(err); got != tc.want {
				t.Errorf("exit = %d, want %d, from %v", got, tc.want, err)
			}
		})
	}
}
