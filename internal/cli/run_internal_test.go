package cli

import (
	"context"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func TestNoState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resolved input.Resolved
		want     string
	}{
		{
			name:     "should ask for a state when nothing was read",
			resolved: input.Resolved{Source: input.SourceNone},
			want:     "no state given. Pipe one to stdin, or pass --state or --state-file",
		},
		{
			name:     "should say stdin was empty when it was read and held nothing",
			resolved: input.Resolved{Source: input.SourceNone, EmptyStdin: true},
			want:     "no state given, since stdin was empty. Pipe one to stdin, or pass --state or --state-file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := noState(tc.resolved); got == nil || got.Error() != tc.want {
				t.Errorf("noState = %v, want %q", got, tc.want)
			}
		})
	}
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

func (a resultAnswerer) Answer(context.Context, runner.RecordKey, onesie.Request) (runner.Reply, error) {
	return runner.Reply{Result: a.result()}, nil
}

func (resultAnswerer) Salt(runner.RecordKey) (string, bool) {
	return "", true
}

type resultAnswerer struct {
	result func() *onesie.Result
}
