package cli

import (
	"testing"
	"time"

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

				if got.inputMode != input.JSONL || got.input != "jsonl" || got.timeout != 30 || !got.longContext {
					t.Errorf("input = %v %q, timeout %d, long context %v, want jsonl, 30 and on",
						got.inputMode, got.input, got.timeout, got.longContext)
				}

				for _, name := range []setting.Name{
					setting.Input.Name(), setting.State.Name(), setting.Map.Name(), setting.Timeout.Name(),
				} {
					if !got.typed(name) {
						t.Errorf("typed(%s) = false, want true", name)
					}
				}

				for _, name := range []setting.Name{setting.StateFile.Name(), setting.ID.Name(), setting.Jobs.Name()} {
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

			got, err := newRunConfig(*flags, events, "is it urgent")

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

	t.Run("should hold the values ParseRun parses for the same flags", func(t *testing.T) {
		t.Parallel()

		for _, args := range [][]string{
			{"-j", "4", "--timeout", "7", "-o", "md"},
			{"-o", "values"},
			{},
		} {
			flags, cmd := registeredRun()

			if err := cmd.Flags().Parse(args); err != nil {
				t.Fatal(err)
			}

			parsed, err := setting.ParseRun(setting.Table(), flags.given, setting.CommandRoot)
			if err != nil {
				t.Fatal(err)
			}

			config, err := newRunConfig(*flags, nil, "is it urgent")
			if err != nil {
				t.Fatal(err)
			}

			if got := setting.Get(parsed, setting.Jobs); got != config.jobs {
				t.Errorf("%v: ParseRun -j = %d, runConfig = %d", args, got, config.jobs)
			}

			if got := setting.Get(parsed, setting.Timeout); got != config.timeout {
				t.Errorf("%v: ParseRun --timeout = %d, runConfig = %d", args, got, config.timeout)
			}

			if got, want := setting.Get(parsed, setting.Output), canonicalOutput(config.output); got != want {
				t.Errorf("%v: ParseRun -o = %q, runConfig = %q", args, got, want)
			}
		}
	})
}

func registeredRun() (*runFlags, *cobra.Command) {
	flags := &runFlags{}
	cmd := &cobra.Command{Use: "onesie"}
	flags.given = setting.Register(cmd.Flags(), cmd.PersistentFlags(), setting.Table(), argv.New(),
		setting.CommandRoot, runTargets(flags))

	cmd.Flags().AddFlagSet(cmd.PersistentFlags())

	return flags, cmd
}

func TestRunConfig_withMerged(t *testing.T) {
	t.Parallel()

	layer := func(source setting.Source, values map[setting.Name]any, spelled string) setting.Layer {
		spellings := map[setting.Name]string{}
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
				layer(setting.SourceFlag, map[setting.Name]any{"mock": "flag.json"}, "--mock"),
				layer(setting.SourceEnv, map[setting.Name]any{"mock": "env.json"}, "ONESIE_MOCK"),
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
			layers: []setting.Layer{layer(setting.SourceEnv, map[setting.Name]any{"mock": "env.json"}, "ONESIE_MOCK")},
			check: func(t *testing.T, got runConfig) {
				t.Helper()

				if got.mockPath != "env.json" || got.mockSpelled != "ONESIE_MOCK" {
					t.Errorf("mock = %q from %q, want env.json from ONESIE_MOCK", got.mockPath, got.mockSpelled)
				}
			},
		},
		{
			name: "should take the cache and its source, long context and the alias lifetime",
			layers: []setting.Layer{layer(setting.SourceEnv, map[setting.Name]any{
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

			merged, _ := setting.Merge(setting.Table(), tc.layers, setting.Choice{})
			tc.check(t, runConfig{}.withMerged(merged))
		})
	}
}

func canonicalOutput(name string) string {
	if name == "md" {
		return "markdown"
	}

	return name
}
