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

	stdoutGone := &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}
	stderrGone := &fs.PathError{Op: "write", Path: "/dev/stderr", Err: syscall.EPIPE}
	release := errors.New("unlocking answers.jsonl: busy")

	tests := []struct {
		name    string
		outcome Outcome
		want    []string
	}{
		{name: "should report nothing when nothing failed"},
		{
			name: "should report a failure and the later failure that followed it",
			outcome: Outcome{
				Failure: errors.New("saving profiles.json: the disk is full"), Later: []error{errors.New("stderr is gone")},
			},
			want: []string{"saving profiles.json: the disk is full", "stderr is gone"},
		},
		{
			name:    "should report only what followed failed records",
			outcome: Outcome{Failure: &recordsError{}, Later: []error{errors.New("compacting answers.jsonl: the disk is full")}},
			want:    []string{"compacting answers.jsonl: the disk is full"},
		},
		{
			name: "should report a finish failure and a release failure that both followed failed records",
			outcome: Outcome{
				Failure: &recordsError{}, Later: []error{errors.New("compacting answers.jsonl: the disk is full"), release},
			},
			want: []string{"compacting answers.jsonl: the disk is full", "unlocking answers.jsonl: busy"},
		},
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
			name:    "should stay quiet for an interrupt followed by a broken pipe",
			outcome: Outcome{Failure: context.Canceled, Later: []error{syscall.EPIPE}},
		},
		{
			name:    "should report a release failure that followed a broken stdout",
			outcome: Outcome{Failure: stdoutGone, Later: []error{release}},
			want:    []string{"unlocking answers.jsonl: busy"},
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

func TestFailureLogKeepFirst(t *testing.T) {
	t.Parallel()

	run := errors.New("--mock: the file is empty")
	release := errors.New("unlocking answers.jsonl: busy")

	tests := []struct {
		name         string
		failure      error
		later        error
		want         error
		wantRecorded []error
	}{
		{name: "should keep the failure and record nothing when nothing failed after it", failure: run, want: run},
		{name: "should make the later failure the failure when nothing else failed", later: release, want: release},
		{
			name: "should keep the failure and record the later failure that followed it", failure: run, later: release,
			want: run, wantRecorded: []error{release},
		},
		{name: "should return nil when neither failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := &failureLog{}

			if got := log.keepFirst(tc.failure, tc.later); !errors.Is(got, tc.want) {
				t.Errorf("keepFirst = %v, want %v", got, tc.want)
			}

			if got := log.drain(); !slices.Equal(got, tc.wantRecorded) {
				t.Errorf("drained = %v, want %v", got, tc.wantRecorded)
			}
		})
	}
}
