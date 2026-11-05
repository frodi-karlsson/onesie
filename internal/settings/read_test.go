package settings_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		reading reading
		wantErr string
		check   func(t *testing.T, layers []settings.Layer)
	}{
		{
			name:    "should read a typed flag by its long name",
			reading: reading{args: []string{"-m", "jev-preview", "--cache"}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				flags := layerAt(t, layers, settings.SourceFlag)
				wantValue(t, flags, "model", "jev-preview", "--model")
				wantValue(t, flags, "cache", true, "--cache")
			},
		},
		{
			name:    "should leave a flag that was not typed to the default layer",
			reading: reading{args: []string{"-m", "jev-preview"}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantNoValue(t, layerAt(t, layers, settings.SourceFlag), "jobs")
				defaults := layerAt(t, layers, settings.SourceDefault)
				wantValue(t, defaults, "jobs", 1, "")
				wantValue(t, defaults, "input", "text", "")
			},
		},
		{
			name:    "should take a command's own default",
			reading: reading{command: settings.CommandCalibrate},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantNoValue(t, layerAt(t, layers, settings.SourceDefault), "input")
			},
		},
		{
			name: "should read each variable by its name",
			reading: reading{env: map[string]string{
				"ONESIE_MOCK": "answers.json", "ONESIE_CACHE": "1", "ONESIE_CACHE_TTL": "90m",
				"ONESIE_LONG_CONTEXT": "true", "ONESIE_CONFIG_DIR": "config", "ONESIE_CACHE_DIR": "cache",
				"TYPESAFE_API_KEY": "secret",
			}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				env := layerAt(t, layers, settings.SourceEnv)
				wantValue(t, env, "mock", "answers.json", "ONESIE_MOCK")
				wantValue(t, env, "cache", true, "ONESIE_CACHE")
				wantValue(t, env, "cache_ttl", 90*time.Minute, "ONESIE_CACHE_TTL")
				wantValue(t, env, "long_context", true, "ONESIE_LONG_CONTEXT")
				wantValue(t, env, "config_dir", "config", "ONESIE_CONFIG_DIR")
				wantValue(t, env, "cache_dir", "cache", "ONESIE_CACHE_DIR")
				wantValue(t, env, "api_key", "secret", "TYPESAFE_API_KEY")
			},
		},
		{
			name:    "should take the first variable set of a row",
			reading: reading{env: map[string]string{"TYPESAFE_BASE_URL": "https://b.test", "ONESIE_BASE_URL": "https://a.test"}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantValue(t, layerAt(t, layers, settings.SourceEnv), "endpoint",
					settings.EndpointValue{BaseURL: "https://a.test"}, "ONESIE_BASE_URL")
			},
		},
		{
			name: "should read a provider scoped variable only on its provider",
			reading: reading{
				args: []string{"--provider", "openrouter"},
				env: map[string]string{
					"TYPESAFE_DEFAULT_MODEL": "jev-1.13.0", "TYPESAFE_API_KEY": "ts", "OPENROUTER_API_KEY": "or",
					"TYPESAFE_BASE_URL": "https://ts.test",
				},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				env := layerAt(t, layers, settings.SourceEnv)
				wantNoValue(t, env, "model")
				wantNoValue(t, env, "endpoint")
				wantValue(t, env, "api_key", "or", "OPENROUTER_API_KEY")
			},
		},
		{
			name: "should read a provider scoped variable on the profile's provider",
			reading: reading{
				defaultProfile: "work",
				profile:        map[string]string{"provider": "cloudflare"},
				env:            map[string]string{"CLOUDFLARE_ACCOUNT_ID": strings.Repeat("a", 32), "TYPESAFE_API_KEY": "ts"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				env := layerAt(t, layers, settings.SourceEnv)
				wantValue(t, env, "endpoint", settings.EndpointValue{AccountID: strings.Repeat("a", 32)},
					"CLOUDFLARE_ACCOUNT_ID")
				wantNoValue(t, env, "api_key")
			},
		},
		{
			name: "should treat an empty variable as unset",
			reading: reading{env: map[string]string{
				"ONESIE_MOCK": "", "ONESIE_CACHE": "", "ONESIE_CACHE_TTL": " ", "ONESIE_LONG_CONTEXT": "",
				"ONESIE_BASE_URL": " ", "ONESIE_PROFILE": "", "ONESIE_PROVIDER": " ",
			}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				if layer, found := layerOf(layers, settings.SourceEnv); found {
					t.Errorf("env layer = %#v, want none", layer.Values)
				}
			},
		},
		{
			name:    "should refuse an ONESIE_CACHE the flag would not take, even beside the flag",
			reading: reading{args: []string{"--cache"}, env: map[string]string{"ONESIE_CACHE": "maybe"}},
			wantErr: "ONESIE_CACHE=maybe is not a value --cache takes. Use 1 or 0",
		},
		{
			name:    "should refuse an ONESIE_LONG_CONTEXT the flag would not take",
			reading: reading{env: map[string]string{"ONESIE_LONG_CONTEXT": "yes"}},
			wantErr: "ONESIE_LONG_CONTEXT=yes is not a value --long-context takes. Use 1 or 0",
		},
		{
			name:    "should refuse a negative ONESIE_CACHE_TTL",
			reading: reading{env: map[string]string{"ONESIE_CACHE_TTL": "-1h"}},
			wantErr: "ONESIE_CACHE_TTL=-1h is not a duration of zero or more, such as 90m or 72h",
		},
		{
			name: "should refuse a CLOUDFLARE_ACCOUNT_ID that is no account id",
			reading: reading{
				args: []string{"--provider", "cloudflare"}, env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "nope"},
			},
			wantErr: "CLOUDFLARE_ACCOUNT_ID takes an account id of 32 hexadecimal characters, got 'nope'",
		},
		{
			name:    "should refuse a flag its kind refuses",
			reading: reading{args: []string{"-o", "jsno"}},
			wantErr: "-o takes auto, json, values, table, raw, csv, tsv or markdown, got 'jsno'",
		},
		{
			name: "should read a question file's top level keys",
			reading: reading{file: &settings.FileValues{TopLevel: map[string]any{
				"assert": "urgent", "abstain_if": "unsure", "model": "jev-preview",
			}}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				file := layerAt(t, layers, settings.SourceFile)
				wantValue(t, file, "assert", []string{"urgent"}, "assert")
				wantValue(t, file, "abstain_if", []string{"unsure"}, "abstain_if")
				wantNoValue(t, file, "model")

				if file.Body {
					t.Error("Body = true, want a question file")
				}
			},
		},
		{
			name: "should read a request body's model and long_context",
			reading: reading{file: &settings.FileValues{Body: true, TopLevel: map[string]any{
				"model": "jev-preview", "long_context": true, "assert": "urgent",
			}}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				file := layerAt(t, layers, settings.SourceFile)
				wantValue(t, file, "model", "jev-preview", "model")
				wantValue(t, file, "long_context", true, "long_context")
				wantNoValue(t, file, "assert")

				if !file.Body {
					t.Error("Body = false, want a request body")
				}
			},
		},
		{
			name: "should read a request body's long_context false as unset",
			reading: reading{file: &settings.FileValues{Body: true, TopLevel: map[string]any{
				"long_context": false,
			}}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				if file, found := layerOf(layers, settings.SourceFile); found {
					wantNoValue(t, file, "long_context")
				}
			},
		},
		{
			name: "should refuse a file value its kind refuses",
			reading: reading{file: &settings.FileValues{Body: true, TopLevel: map[string]any{
				"model": 4,
			}}},
			wantErr: "the request body's model takes a string",
		},
		{
			name: "should key file question values by question id",
			reading: reading{file: &settings.FileValues{Questions: []settings.QuestionValues{
				{ID: "urgent", Shape: plan.Noul, Values: map[string]any{"threshold": 0.7, "fallback": true}},
				{ID: "team", Shape: plan.Pick, Values: map[string]any{"pick": []any{"sales", "support"}}},
				{ID: "plain", Shape: plan.Noul},
			}}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				file := layerAt(t, layers, settings.SourceFile)
				want := map[string]map[settings.Name]any{
					"urgent": {"threshold": 0.7, "fallback": "true"},
					"team":   {"pick": []any{"sales", "support"}},
					"plain":  {},
				}

				if !reflect.DeepEqual(file.Questions, want) {
					t.Errorf("Questions = %#v, want %#v", file.Questions, want)
				}
			},
		},
		{
			name: "should read every number type a question file yields",
			reading: reading{file: &settings.FileValues{Questions: []settings.QuestionValues{
				{ID: "a", Values: map[string]any{"threshold": int64(0)}},
				{ID: "b", Values: map[string]any{"threshold": uint64(1)}},
				{ID: "c", Values: map[string]any{"min_confidence": float64(0.5)}},
			}}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				file := layerAt(t, layers, settings.SourceFile)
				for id, want := range map[string]map[settings.Name]any{
					"a": {"threshold": 0.0}, "b": {"threshold": 1.0}, "c": {"min_confidence": 0.5},
				} {
					if !reflect.DeepEqual(file.Questions[id], want) {
						t.Errorf("Questions[%s] = %#v, want %#v", id, file.Questions[id], want)
					}
				}
			},
		},
		{
			name: "should refuse a question value out of range",
			reading: reading{file: &settings.FileValues{Questions: []settings.QuestionValues{
				{ID: "urgent", Values: map[string]any{"threshold": 1.5}},
			}}},
			wantErr: "the question file's threshold for 'urgent' takes a number between 0 and 1, got 1.5",
		},
		{
			name: "should drop a calibrate file's assert, abstain_if and policy by Modes",
			reading: reading{
				command: settings.CommandCalibrate,
				file: &settings.FileValues{
					TopLevel: map[string]any{"assert": "urgent", "abstain_if": "unsure"},
					Questions: []settings.QuestionValues{{ID: "team", Shape: plan.Pick, Values: map[string]any{
						"pick": []any{"sales"}, "min_confidence": 0.5, "fallback": "human",
					}}},
				},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				file := layerAt(t, layers, settings.SourceFile)
				wantNoValue(t, file, "assert")
				wantNoValue(t, file, "abstain_if")

				want := map[settings.Name]any{"pick": []any{"sales"}}
				if !reflect.DeepEqual(file.Questions["team"], want) {
					t.Errorf("Questions[team] = %#v, want %#v", file.Questions["team"], want)
				}
			},
		},
		{
			name: "should skip an ambient value silently outside its mode",
			reading: reading{
				args: []string{"--print-request"}, env: map[string]string{"ONESIE_CACHE": "1", "ONESIE_MOCK": "m.json"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				if env, found := layerOf(layers, settings.SourceEnv); found {
					wantNoValue(t, env, "cache")
					wantNoValue(t, env, "mock")
				}
			},
		},
		{
			name:    "should keep a typed flag outside its mode for the rules to refuse",
			reading: reading{args: []string{"--print-request", "--cache"}},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantValue(t, layerAt(t, layers, settings.SourceFlag), "cache", true, "--cache")
			},
		},
		{
			name: "should tag a chosen profile by rank",
			reading: reading{
				args: []string{"--profile", "work"}, profile: map[string]string{"provider": "typesafe", "model": "m"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				chosen := layerAt(t, layers, settings.SourceChosenProfile)
				wantValue(t, chosen, "model", "m", "model")
				wantValue(t, chosen, "provider", "typesafe", "provider")

				if _, found := layerOf(layers, settings.SourceDefaultProfile); found {
					t.Error("found a default profile layer beside a chosen profile")
				}
			},
		},
		{
			name: "should tag a profile ONESIE_PROFILE chose as chosen",
			reading: reading{
				env: map[string]string{"ONESIE_PROFILE": "work"}, defaultProfile: "home",
				profile: map[string]string{"provider": "typesafe", "model": "m"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantValue(t, layerAt(t, layers, settings.SourceChosenProfile), "model", "m", "model")
				wantValue(t, layerAt(t, layers, settings.SourceEnv), "profile", "work", "ONESIE_PROFILE")
			},
		},
		{
			name: "should tag the default profile by rank, with its name",
			reading: reading{
				defaultProfile: "work",
				profile: map[string]string{
					"provider": "typesafe", "model": "m", "base_url": "https://w.test",
				},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				fallback := layerAt(t, layers, settings.SourceDefaultProfile)
				wantValue(t, fallback, "profile", "work", "")
				wantValue(t, fallback, "model", "m", "model")
				wantValue(t, fallback, "endpoint", settings.EndpointValue{BaseURL: "https://w.test"}, "base_url")

				if _, found := layerOf(layers, settings.SourceChosenProfile); found {
					t.Error("found a chosen profile layer with nothing chosen")
				}
			},
		},
		{
			name: "should read a profile's account_id as the endpoint's account half",
			reading: reading{
				args:    []string{"--profile", "cf"},
				profile: map[string]string{"provider": "cloudflare", "account_id": strings.Repeat("b", 32)},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantValue(t, layerAt(t, layers, settings.SourceChosenProfile), "endpoint",
					settings.EndpointValue{AccountID: strings.Repeat("b", 32)}, "account_id")
			},
		},
		{
			name: "should refuse a profile member its kind refuses",
			reading: reading{
				args:    []string{"--profile", "cf"},
				profile: map[string]string{"provider": "cloudflare", "account_id": "nope"},
			},
			wantErr: "the profile's account_id takes an account id of 32 hexadecimal characters, got 'nope'",
		},
		{
			name: "should build no profile layer under a provider choice",
			reading: reading{
				args:    []string{"--provider", "openrouter"},
				profile: map[string]string{"provider": "typesafe", "model": "m"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				for _, source := range []settings.Source{settings.SourceChosenProfile, settings.SourceDefaultProfile} {
					if layer, found := layerOf(layers, source); found {
						t.Errorf("profile layer %d = %#v, want none", source, layer.Values)
					}
				}
			},
		},
		{
			name: "should keep only the default profile's name under a provider choice",
			reading: reading{
				env: map[string]string{"ONESIE_PROVIDER": "openrouter"}, defaultProfile: "work",
				profile: map[string]string{"provider": "typesafe", "model": "m"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				fallback := layerAt(t, layers, settings.SourceDefaultProfile)
				if want := (map[settings.Name]any{"profile": "work"}); !reflect.DeepEqual(fallback.Values, want) {
					t.Errorf("default profile layer = %#v, want %#v", fallback.Values, want)
				}
			},
		},
		{
			name:    "should refuse --profile with --provider",
			reading: reading{args: []string{"--profile", "work", "--provider", "openrouter"}},
			wantErr: "--profile and --provider both choose what the run uses. Pass one",
		},
		{
			name: "should order the layers from the highest source down",
			reading: reading{
				args: []string{"-m", "a"}, env: map[string]string{"ONESIE_CACHE": "1"}, defaultProfile: "work",
				profile: map[string]string{"provider": "typesafe", "model": "p"},
				file:    &settings.FileValues{Body: true, TopLevel: map[string]any{"model": "b"}},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				got := make([]settings.Source, 0, len(layers))
				for _, layer := range layers {
					got = append(got, layer.Source)
				}

				want := []settings.Source{
					settings.SourceFlag, settings.SourceFile, settings.SourceEnv, settings.SourceDefaultProfile,
					settings.SourceDefault,
				}
				if !slices.Equal(got, want) {
					t.Errorf("layer sources = %v, want %v", got, want)
				}
			},
		},
		{
			name: "should read that a chosen profile holds a key, never the key",
			reading: reading{
				args: []string{"--profile", "work"}, keyHeld: true,
				profile: map[string]string{"provider": "typesafe", "api_key": "secret"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantValue(t, layerAt(t, layers, settings.SourceChosenProfile), "api_key", settings.HeldKey{}, "api_key")
			},
		},
		{
			name: "should read no key from a profile that holds none",
			reading: reading{
				args: []string{"--profile", "work"}, profile: map[string]string{"provider": "typesafe", "api_key": "secret"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantNoValue(t, layerAt(t, layers, settings.SourceChosenProfile), "api_key")
			},
		},
		{
			name: "should read no profile when nothing names one",
			reading: reading{
				profile: map[string]string{"provider": "cloudflare", "model": "p"},
				env:     map[string]string{"TYPESAFE_DEFAULT_MODEL": "e"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				for _, source := range []settings.Source{settings.SourceChosenProfile, settings.SourceDefaultProfile} {
					if layer, found := layerOf(layers, source); found {
						t.Errorf("profile layer %d = %#v, want none", source, layer.Values)
					}
				}

				wantValue(t, layerAt(t, layers, settings.SourceEnv), "model", "e", "TYPESAFE_DEFAULT_MODEL")
			},
		},
		{
			name: "should read --profile on calibrate from root's persistent flags",
			reading: reading{
				command: settings.CommandCalibrate, args: []string{"--profile", "work"},
				profile: map[string]string{"provider": "typesafe", "model": "p"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				wantValue(t, layerAt(t, layers, settings.SourceFlag), "profile", "work", "--profile")
				wantValue(t, layerAt(t, layers, settings.SourceChosenProfile), "model", "p", "model")
			},
		},
		{
			name: "should keep a profile command off the run choice",
			reading: reading{
				command: settings.CommandProfileUpdate, args: []string{"--provider", "openrouter"},
				env: map[string]string{"ONESIE_PROFILE": "work", "ONESIE_PROVIDER": "levanto"},
			},
			check: func(t *testing.T, layers []settings.Layer) {
				t.Helper()

				for _, layer := range layers {
					wantNoValue(t, layer, "provider")
					wantNoValue(t, layer, "profile")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			layers, err := settings.Read(settings.Table(), tc.reading.sources(t))
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("Read error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Read: %v", err)
			}

			tc.check(t, layers)
		})
	}
}

func TestChoose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		reading reading
		want    settings.Choice
		wantErr string
	}{
		{
			name:    "should choose nothing when nothing is given",
			reading: reading{},
		},
		{
			name:    "should read the question file from -f",
			reading: reading{args: []string{"-f", "moderation"}},
			want:    settings.Choice{File: "moderation"},
		},
		{
			name:    "should choose the profile --profile names",
			reading: reading{args: []string{"--profile", " work "}, defaultProfile: "home"},
			want:    settings.Choice{Profile: "work", ProfileBy: settings.SourceFlag, DefaultProfile: "home"},
		},
		{
			name:    "should choose the provider --provider names",
			reading: reading{args: []string{"--provider", "openrouter"}, defaultProfile: "home"},
			want:    settings.Choice{Provider: "openrouter", ProviderBy: settings.SourceFlag, DefaultProfile: "home"},
		},
		{
			name: "should choose the profile --profile names on calibrate",
			reading: reading{
				command: settings.CommandCalibrate, args: []string{"--profile", "work"}, defaultProfile: "home",
			},
			want: settings.Choice{Profile: "work", ProfileBy: settings.SourceFlag, DefaultProfile: "home"},
		},
		{
			name: "should choose the provider --provider names on calibrate",
			reading: reading{
				command: settings.CommandCalibrate, args: []string{"--provider", "openrouter"},
			},
			want: settings.Choice{Provider: "openrouter", ProviderBy: settings.SourceFlag},
		},
		{
			name: "should keep profile update's --provider off the run choice beside ONESIE_PROFILE",
			reading: reading{
				command: settings.CommandProfileUpdate, args: []string{"--provider", "openrouter"},
				env: map[string]string{"ONESIE_PROFILE": "work"}, defaultProfile: "home",
			},
			want: settings.Choice{DefaultProfile: "home"},
		},
		{
			name:    "should choose the profile ONESIE_PROFILE names",
			reading: reading{env: map[string]string{"ONESIE_PROFILE": " work "}},
			want:    settings.Choice{Profile: "work", ProfileBy: settings.SourceEnv},
		},
		{
			name:    "should choose the provider ONESIE_PROVIDER names",
			reading: reading{env: map[string]string{"ONESIE_PROVIDER": "openrouter"}},
			want:    settings.Choice{Provider: "openrouter", ProviderBy: settings.SourceEnv},
		},
		{
			name:    "should fall back to the default profile",
			reading: reading{defaultProfile: "home"},
			want:    settings.Choice{Profile: "home", ProfileBy: settings.SourceDefaultProfile, DefaultProfile: "home"},
		},
		{
			name:    "should let --profile beat ONESIE_PROVIDER",
			reading: reading{args: []string{"--profile", "work"}, env: map[string]string{"ONESIE_PROVIDER": "openrouter"}},
			want:    settings.Choice{Profile: "work", ProfileBy: settings.SourceFlag},
		},
		{
			name:    "should let --provider beat ONESIE_PROVIDER",
			reading: reading{args: []string{"--provider", "openrouter"}, env: map[string]string{"ONESIE_PROVIDER": "levanto"}},
			want:    settings.Choice{Provider: "openrouter", ProviderBy: settings.SourceFlag},
		},
		{
			name:    "should let --profile beat ONESIE_PROFILE",
			reading: reading{args: []string{"--profile", "work"}, env: map[string]string{"ONESIE_PROFILE": "home"}},
			want:    settings.Choice{Profile: "work", ProfileBy: settings.SourceFlag},
		},
		{
			name:    "should refuse --profile with --provider",
			reading: reading{args: []string{"--profile", "work", "--provider", "openrouter"}},
			wantErr: "--profile and --provider both choose what the run uses. Pass one",
		},
		{
			name:    "should refuse ONESIE_PROFILE with ONESIE_PROVIDER",
			reading: reading{env: map[string]string{"ONESIE_PROFILE": "work", "ONESIE_PROVIDER": "openrouter"}},
			wantErr: "ONESIE_PROFILE and ONESIE_PROVIDER are both set, and each chooses what the run uses. " +
				"Unset one",
		},
		{
			name:    "should refuse --provider with ONESIE_PROFILE",
			reading: reading{args: []string{"--provider", "openrouter"}, env: map[string]string{"ONESIE_PROFILE": "work"}},
			wantErr: "--provider and ONESIE_PROFILE both choose what the run uses. Drop --provider or unset " +
				"ONESIE_PROFILE",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := settings.Choose(settings.Table(), tc.reading.sources(t))
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("Choose error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Choose: %v", err)
			}

			if got != tc.want {
				t.Errorf("Choose = %#v, want %#v", got, tc.want)
			}
		})
	}
}

type reading struct {
	command        settings.Command
	args           []string
	env            map[string]string
	file           *settings.FileValues
	profile        map[string]string
	keyHeld        bool
	defaultProfile string
}

func (r reading) sources(t *testing.T) settings.Sources {
	t.Helper()

	command := r.command
	if command == 0 {
		command = settings.CommandRoot
	}

	registered := register(command)
	if command == settings.CommandCalibrate {
		root := register(settings.CommandRoot)
		registered.persistent = root.persistent
		registered.given.Inherit(root.persistent)
	}

	registered.parse(t, r.args...)

	src := settings.Sources{
		Given: registered.given,
		File:  r.file,
		LookupEnv: func(name string) (string, bool) {
			value, found := r.env[name]

			return value, found
		},
		DefaultProfile: r.defaultProfile,
		Facts:          settings.Facts{Command: command, Events: registered.recorder.Events()},
	}

	if r.profile != nil {
		src.Profile = &settings.ProfileValues{Members: r.profile, KeyHeld: r.keyHeld}
	}

	return src
}

func layerOf(layers []settings.Layer, source settings.Source) (settings.Layer, bool) {
	for _, layer := range layers {
		if layer.Source == source {
			return layer, true
		}
	}

	return settings.Layer{}, false
}

func layerAt(t *testing.T, layers []settings.Layer, source settings.Source) settings.Layer {
	t.Helper()

	layer, found := layerOf(layers, source)
	if !found {
		t.Fatalf("no layer of source %d in %d layers", source, len(layers))
	}

	return layer
}

func wantValue(t *testing.T, layer settings.Layer, name settings.Name, want any, spelled string) {
	t.Helper()

	got, found := layer.Values[name]
	if !found {
		t.Errorf("layer %d holds no %s, want %#v", layer.Source, name, want)

		return
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("layer %d %s = %#v, want %#v", layer.Source, name, got, want)
	}

	if spelled != "" && layer.Spelled[name] != spelled {
		t.Errorf("layer %d spells %s as %q, want %q", layer.Source, name, layer.Spelled[name], spelled)
	}
}

func wantNoValue(t *testing.T, layer settings.Layer, name settings.Name) {
	t.Helper()

	if got, found := layer.Values[name]; found {
		t.Errorf("layer %d %s = %#v, want none", layer.Source, name, got)
	}
}
