package cli

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"syscall"
	"testing"
)

func TestOutcomeReport(t *testing.T) {
	t.Parallel()

	stderrGone := &fs.PathError{Op: "write", Path: "/dev/stderr", Err: syscall.EPIPE}
	release := errors.New("unlocking answers.jsonl: busy")

	tests := []struct {
		name    string
		outcome Outcome
		want    []string
	}{
		{name: "should report nothing when nothing failed"},
		{
			name:    "should leave out an interrupt among the later failures",
			outcome: Outcome{Failure: &recordsError{}, Later: []error{context.Canceled, release}},
			want:    []string{"unlocking answers.jsonl: busy"},
		},
		{
			name:    "should leave out a broken pipe among the later failures",
			outcome: Outcome{Failure: &recordsError{}, Later: []error{stderrGone}},
		},
		{
			name:    "should report a server error and leave out the broken stderr that followed it",
			outcome: Outcome{Failure: errors.New("502 bad gateway"), Later: []error{stderrGone}},
			want:    []string{"502 bad gateway"},
		},
		{
			name:    "should strip the terminal controls a later failure quotes",
			outcome: Outcome{Failure: &recordsError{}, Later: []error{errors.New("502 \x1b[2Jbad gateway")}},
			want:    []string{"502 [2Jbad gateway"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.outcome.Report(); !slices.Equal(got, tc.want) {
				t.Errorf("Report = %q, want %q", got, tc.want)
			}
		})
	}
}
