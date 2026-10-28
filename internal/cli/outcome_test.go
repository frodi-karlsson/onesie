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
		outcome outcome
		want    []string
	}{
		{name: "should report nothing when nothing failed"},
		{
			name: "should report a failure and the cleanup failure that followed it",
			outcome: outcome{
				failure: errors.New("saving profiles.json: the disk is full"), cleanups: []error{errors.New("stderr is gone")},
			},
			want: []string{"saving profiles.json: the disk is full", "stderr is gone"},
		},
		{
			name:    "should report only what followed failed records",
			outcome: outcome{failure: &recordsError{}, cleanups: []error{errors.New("compacting answers.jsonl: the disk is full")}},
			want:    []string{"compacting answers.jsonl: the disk is full"},
		},
		{
			name: "should report a finish failure and a release failure that both followed failed records",
			outcome: outcome{
				failure: &recordsError{}, cleanups: []error{errors.New("compacting answers.jsonl: the disk is full"), release},
			},
			want: []string{"compacting answers.jsonl: the disk is full", "unlocking answers.jsonl: busy"},
		},
		{
			name:    "should leave out an interrupt among the cleanup failures",
			outcome: outcome{failure: &recordsError{}, cleanups: []error{context.Canceled, release}},
			want:    []string{"unlocking answers.jsonl: busy"},
		},
		{
			name:    "should leave out a broken pipe among the cleanup failures",
			outcome: outcome{failure: &recordsError{}, cleanups: []error{stderrGone}},
		},
		{
			name:    "should report a server error and leave out the broken stderr that followed it",
			outcome: outcome{failure: errors.New("502 bad gateway"), cleanups: []error{stderrGone}},
			want:    []string{"502 bad gateway"},
		},
		{
			name:    "should stay quiet for an interrupt followed by a broken pipe",
			outcome: outcome{failure: context.Canceled, cleanups: []error{syscall.EPIPE}},
		},
		{
			name:    "should report a release failure that followed a broken stdout",
			outcome: outcome{failure: stdoutGone, cleanups: []error{release}},
			want:    []string{"unlocking answers.jsonl: busy"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.outcome.report(); !slices.Equal(got, tc.want) {
				t.Errorf("report = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCleanupLogAfter(t *testing.T) {
	t.Parallel()

	run := errors.New("--mock: the file is empty")
	release := errors.New("unlocking answers.jsonl: busy")

	tests := []struct {
		name         string
		failure      error
		cleanup      error
		want         error
		wantRecorded []error
	}{
		{name: "should keep the failure and record nothing when the cleanup succeeded", failure: run, want: run},
		{name: "should make the cleanup failure the failure when nothing else failed", cleanup: release, want: release},
		{
			name: "should keep the failure and record the cleanup failure that followed it", failure: run, cleanup: release,
			want: run, wantRecorded: []error{release},
		},
		{name: "should return nil when neither failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := &cleanupLog{}

			if got := log.after(tc.failure, tc.cleanup); !errors.Is(got, tc.want) {
				t.Errorf("after = %v, want %v", got, tc.want)
			}

			if got := log.drain(); !slices.Equal(got, tc.wantRecorded) {
				t.Errorf("drained = %v, want %v", got, tc.wantRecorded)
			}
		})
	}
}
