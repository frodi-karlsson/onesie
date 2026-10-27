package cli

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/input"
)

func TestNewRunConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		check   func(t *testing.T, got runConfig)
		wantErr string
	}{
		{
			name: "should keep the parsed flags and which of them were given",
			args: []string{"-i", "jsonl", "--state", "s", "--map", ".body", "--timeout", "30", "--long-context"},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.inputMode != input.JSONL || got.input != "jsonl" || !got.hasInput {
					t.Errorf("input = %v %q given %v, want jsonl given", got.inputMode, got.input, got.hasInput)
				}

				if !got.hasState || got.hasStateFile || !got.hasMap || got.hasID || got.hasModel {
					t.Errorf("given = state %v, state-file %v, map %v, id %v, model %v, want state and map",
						got.hasState, got.hasStateFile, got.hasMap, got.hasID, got.hasModel)
				}

				if got.timeout != 30 || !got.timeoutSet || got.jobsSet || got.retriesSet || got.maxRetryAfterSet {
					t.Errorf("timing = %d set %v, want 30 set and nothing else set", got.timeout, got.timeoutSet)
				}

				if !got.longContextSet || !got.env.longContext {
					t.Errorf("long context = set %v on %v, want set and on", got.longContextSet, got.env.longContext)
				}

				if got.positional != "is it urgent" || len(got.events) != 1 || got.events[0].Name != "ask" {
					t.Errorf("positional %q events %v, want the question and the ask event", got.positional, got.events)
				}
			},
		},
		{
			name: "should read the mock file from --mock first",
			args: []string{"--mock", "flag.json"},
			env:  map[string]string{envMock: "env.json"},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.mockPath != "flag.json" || got.mockSpelled != flagMock {
					t.Errorf("mock = %q from %q, want flag.json from --mock", got.mockPath, got.mockSpelled)
				}
			},
		},
		{
			name: "should read the mock file from ONESIE_MOCK without --mock",
			env:  map[string]string{envMock: "env.json"},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.mockPath != "env.json" || got.mockSpelled != envMock {
					t.Errorf("mock = %q from %q, want env.json from ONESIE_MOCK", got.mockPath, got.mockSpelled)
				}
			},
		},
		{
			name:    "should refuse an ONESIE_CACHE the flag would not take",
			env:     map[string]string{envCache: "maybe"},
			wantErr: "ONESIE_CACHE=maybe is not a value --cache takes. Use 1 or 0",
		},
		{
			name:    "should refuse an input mode it does not know",
			args:    []string{"-i", "bogus"},
			wantErr: "-i takes text, json, jsonl, lines, csv, tsv or request, got 'bogus'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flags := &runFlags{}
			cmd := &cobra.Command{Use: "onesie"}
			cmd.Flags().StringVarP(&flags.input, flagInput, "i", "text", "")
			cmd.Flags().StringVar(&flags.state, flagState, "", "")
			cmd.Flags().StringVar(&flags.stateFile, flagStateFile, "", "")
			cmd.Flags().StringVar(&flags.mapSource, flagMap, "", "")
			cmd.Flags().StringVar(&flags.idSource, flagID, "", "")
			cmd.Flags().StringVarP(&flags.model, flagModel, "m", "", "")
			cmd.Flags().StringVar(&flags.mock, "mock", "", "")
			cmd.Flags().IntVarP(&flags.jobs, flagJobs, "j", 1, "")
			cmd.Flags().IntVar(&flags.timeout, flagTimeout, 10, "")
			cmd.Flags().IntVar(&flags.retries, flagRetries, 2, "")
			cmd.Flags().IntVar(&flags.maxRetryAfter, flagMaxRetryAfter, 60, "")
			cmd.Flags().BoolVar(&flags.cache, flagCache, false, "")
			cmd.Flags().BoolVar(&flags.longContext, flagLongContext, false, "")

			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatal(err)
			}

			events := []argv.Event{{Name: "ask", Value: "a=x"}}

			got, err := newRunConfig(cmd, lookupFrom(tc.env), *flags, events, "is it urgent")

			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("newRunConfig = %v, want the error %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			tc.check(t, got)
		})
	}
}
