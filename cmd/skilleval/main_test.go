package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/skilleval"
)

func TestAppResultFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		runs    []string
		globErr error
		want    string
		wantErr bool
	}{
		{
			name: "should pick the newest run when given no argument",
			runs: []string{"evals/results/2026-09-24/aggregate-result.json", "evals/results/2026-09-23/aggregate-result.json"},
			want: "evals/results/2026-09-24/aggregate-result.json",
		},
		{
			name: "should use the result it is given without globbing",
			args: []string{"other.json"},
			want: "other.json",
		},
		{name: "should fail when there is no run", wantErr: true},
		{name: "should fail when the glob fails", globErr: errors.New("bad pattern"), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := &app{glob: func(string) ([]string, error) {
				if len(tc.args) > 0 {
					t.Error("glob called although a result was given")
				}

				return tc.runs, tc.globErr
			}}

			got, err := a.resultFile(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("resultFile(...) error = %v, wantErr %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("resultFile(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAppRun(t *testing.T) {
	t.Parallel()

	report := skilleval.Report{Cases: []skilleval.CaseReport{{
		Name: "gate",
		Arms: []skilleval.ArmReport{{
			Arm: "with", Runs: 1, Commands: 1, Clean: 1,
			QuietWithAssert: []skilleval.Finding{{Run: 1, Command: "jev 'a' -q --assert 'a.value'"}},
		}},
	}}}

	tests := []struct {
		name string
		want string
	}{
		{name: "should say the -q with --assert tally is separate from the eval score", want: "not part of the eval score"},
		{name: "should say a form quoted as a warning is counted too", want: "quotes as a warning is counted too"},
		{name: "should report the tally beside the dry run result", want: "1 clean, 0 failed, 0 skipped, 1 use -q with --assert"},
		{name: "should list the command that combines them", want: "-q with --assert, run 1: jev 'a' -q --assert 'a.value'"},
	}

	a := &app{
		glob:   func(string) ([]string, error) { return []string{"r.json"}, nil },
		grader: fakeGrader(report),
	}

	var out bytes.Buffer
	if err := a.run(context.Background(), nil, &out); err != nil {
		t.Fatalf("run(...) error = %v", err)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("output = %q, want it to contain %q", out.String(), tc.want)
			}
		})
	}
}

type fakeGrader skilleval.Report

func (f fakeGrader) Grade(context.Context, string) (skilleval.Report, error) {
	return skilleval.Report(f), nil
}
