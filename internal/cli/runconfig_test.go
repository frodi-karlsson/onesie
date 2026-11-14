package cli

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/input"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestNewRunConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		check   func(t *testing.T, got runConfig)
		wantErr string
	}{
		{
			name: "should parse a typed --delimiter",
			args: []string{"--delimiter", ";"},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.parsedDelimiter != (input.Delimiter{Comma: ';'}) {
					t.Errorf("delimiter = %+v, want ;", got.parsedDelimiter)
				}
			},
		},
		{
			name: "should leave the delimiter unparsed when --delimiter is not typed",
			args: []string{},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.parsedDelimiter != (input.Delimiter{}) {
					t.Errorf("delimiter = %+v, want none", got.parsedDelimiter)
				}
			},
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

			flags, cmd := registeredRun()

			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatal(err)
			}

			events := []argv.Event{{Name: "ask", Value: "a=x"}}

			parsed, err := setting.ParseRun(settingrow.Table(), flags.given, settingrow.CommandRoot)
			var got runConfig
			if err == nil {
				got, err = newRunConfig(parsed, flags.given, events, "is it urgent")
			}

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

func registeredRun() (*runFlags, *cobra.Command) {
	flags := &runFlags{}
	cmd := &cobra.Command{Use: "onesie"}
	flags.given = setting.Register(cmd.Flags(), settingrow.Table(), argv.New(),
		settingrow.CommandRoot, runTargets(flags))

	return flags, cmd
}

func runWith(values map[string]any) runConfig {
	merged := &setting.Merged{}
	for name, value := range values {
		merged.Set(settingrow.Name(name), value, setting.SourceFlag)
	}
	return runConfig{}.withMerged(merged)
}
