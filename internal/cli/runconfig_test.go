package cli

import (
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/cache"
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
			name: "should keep the parsed flags and the flags given",
			args: []string{"-i", "jsonl", "--state", "s", "--map", ".body", "--timeout", "30", "--long-context"},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.inputMode != input.JSONL || setting.Get(got.merged, setting.Input) != "jsonl" || setting.Get(got.merged, setting.Timeout) != 30 || !setting.Get(got.merged, setting.LongContext) {
					t.Errorf("input = %v %q, timeout %d, long context %v, want jsonl, 30 and on",
						got.inputMode, setting.Get(got.merged, setting.Input), setting.Get(got.merged, setting.Timeout), setting.Get(got.merged, setting.LongContext))
				}

				for _, name := range []settingrow.Name{
					setting.Input.Name(), setting.State.Name(), setting.Map.Name(), setting.Timeout.Name(),
				} {
					if !got.typed(name) {
						t.Errorf("typed(%s) = false, want true", name)
					}
				}

				for _, name := range []settingrow.Name{setting.StateFile.Name(), setting.ID.Name(), setting.Jobs.Name()} {
					if got.typed(name) {
						t.Errorf("typed(%s) = true, want false", name)
					}
				}

				if got.positional != "is it urgent" || len(got.events) != 1 || got.events[0].Name != "ask" {
					t.Errorf("positional %q events %v, want the question and the ask event", got.positional, got.events)
				}
			},
		},
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
			name:    "should refuse a typed --delimiter longer than one character",
			args:    []string{"--delimiter", ";;"},
			wantErr: "--delimiter takes auto or one character, got ';;'",
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
	flags.given = setting.Register(cmd.Flags(), cmd.PersistentFlags(), settingrow.Table(), argv.New(),
		settingrow.CommandRoot, runTargets(flags))

	cmd.Flags().AddFlagSet(cmd.PersistentFlags())

	return flags, cmd
}

func TestRunConfig_withMerged(t *testing.T) {
	t.Parallel()

	layer := func(source setting.Source, values map[settingrow.Name]any, spelled string) setting.Layer {
		spellings := map[settingrow.Name]string{}
		for name := range values {
			spellings[name] = spelled
		}

		return setting.Layer{Source: source, Values: values, Spelled: spellings}
	}

	tests := []struct {
		name   string
		layers []setting.Layer
		check  func(t *testing.T, got runConfig)
	}{
		{
			name: "should read the mock file from --mock first",
			layers: []setting.Layer{
				layer(setting.SourceFlag, map[settingrow.Name]any{"mock": "flag.json"}, "--mock"),
				layer(setting.SourceEnv, map[settingrow.Name]any{"mock": "env.json"}, "ONESIE_MOCK"),
			},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.mockPath != "flag.json" || got.mockSpelled != flagMock {
					t.Errorf("mock = %q from %q, want flag.json from --mock", got.mockPath, got.mockSpelled)
				}
			},
		},
		{
			name:   "should read the mock file from ONESIE_MOCK without --mock",
			layers: []setting.Layer{layer(setting.SourceEnv, map[settingrow.Name]any{"mock": "env.json"}, "ONESIE_MOCK")},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.mockPath != "env.json" || got.mockSpelled != "ONESIE_MOCK" {
					t.Errorf("mock = %q from %q, want env.json from ONESIE_MOCK", got.mockPath, got.mockSpelled)
				}
			},
		},
		{
			name: "should take the cache and its source, long context and the alias lifetime",
			layers: []setting.Layer{layer(setting.SourceEnv, map[settingrow.Name]any{
				"cache": true, "long_context": true, "cache_ttl": time.Duration(0),
			}, "ONESIE_X")},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				want := switches{cache: true, cacheFrom: setting.SourceEnv, longContext: true, aliasTTL: cache.NoAliasTTL}
				if got.env != want {
					t.Errorf("switches = %+v, want %+v", got.env, want)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			merged, _ := setting.Merge(settingrow.Table(), tc.layers, setting.Choice{})
			tc.check(t, runConfig{}.withMerged(merged))
		})
	}
}

func runWith(values map[string]any) runConfig {
	merged := &setting.Merged{}
	for name, value := range values {
		merged.Set(settingrow.Name(name), value, setting.SourceFlag)
	}
	return runConfig{}.withMerged(merged)
}
