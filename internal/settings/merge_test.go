package settings_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestMerge(t *testing.T) {
	t.Parallel()

	account := strings.Repeat("a", 32)

	tests := []struct {
		name        string
		reading     reading
		layers      []settings.Layer
		built       *plan.Plan
		want        values
		wantSources map[settings.Name]settings.Source
		wantNotices []string
		check       func(t *testing.T, merged *settings.Merged)
	}{
		{
			name: "should let a flag beat the request body, spelling the flag by its long name",
			reading: reading{
				args: []string{"-m", "jev-preview"},
				file: &settings.FileValues{Body: true, TopLevel: map[string]any{"model": "jev-1.13.0"}},
			},
			want:        values{"model": "jev-preview"},
			wantSources: map[settings.Name]settings.Source{"model": settings.SourceFlag},
			wantNotices: []string{"--model overrides the request body's model"},
		},
		{
			name: "should let a flag beat a chosen profile",
			reading: reading{
				args: []string{"--profile", "work", "--model", "x"}, profile: map[string]string{"model": "y"},
			},
			want:        values{"model": "x"},
			wantSources: map[settings.Name]settings.Source{"model": settings.SourceFlag},
			wantNotices: []string{"--model overrides the profile's model"},
		},
		{
			name: "should rank a chosen profile above the file",
			reading: reading{
				args:    []string{"--profile", "work"},
				file:    &settings.FileValues{Body: true, TopLevel: map[string]any{"model": "b"}},
				profile: map[string]string{"provider": "typesafe", "model": "p"},
			},
			want:        values{"model": "p"},
			wantSources: map[settings.Name]settings.Source{"model": settings.SourceChosenProfile},
			wantNotices: []string{"the profile's model overrides the request body's model"},
		},
		{
			name: "should rank a chosen profile above the environment",
			reading: reading{
				env:     map[string]string{"ONESIE_PROFILE": "work", "TYPESAFE_DEFAULT_MODEL": "e"},
				profile: map[string]string{"provider": "typesafe", "model": "p"},
			},
			want:        values{"model": "p", "profile": "work"},
			wantSources: map[settings.Name]settings.Source{"model": settings.SourceChosenProfile},
			wantNotices: []string{"the profile's model overrides TYPESAFE_DEFAULT_MODEL"},
		},
		{
			name: "should let the file beat the environment",
			reading: reading{
				env:  map[string]string{"TYPESAFE_DEFAULT_MODEL": "e"},
				file: &settings.FileValues{Body: true, TopLevel: map[string]any{"model": "b"}},
			},
			want:        values{"model": "b"},
			wantSources: map[settings.Name]settings.Source{"model": settings.SourceFile},
			wantNotices: []string{"the request body's model overrides TYPESAFE_DEFAULT_MODEL"},
		},
		{
			name: "should let the file beat the default profile",
			reading: reading{
				defaultProfile: "work",
				file:           &settings.FileValues{Body: true, TopLevel: map[string]any{"model": "b"}},
				profile:        map[string]string{"provider": "typesafe", "model": "p"},
			},
			want:        values{"model": "b"},
			wantSources: map[settings.Name]settings.Source{"model": settings.SourceFile},
			wantNotices: []string{"the request body's model overrides the default profile's model"},
		},
		{
			name: "should rank the default profile below the environment",
			reading: reading{
				defaultProfile: "work", env: map[string]string{"TYPESAFE_DEFAULT_MODEL": "e"},
				profile: map[string]string{"provider": "typesafe", "model": "p"},
			},
			want:        values{"model": "e"},
			wantSources: map[settings.Name]settings.Source{"model": settings.SourceEnv},
			wantNotices: []string{"TYPESAFE_DEFAULT_MODEL overrides the default profile's model"},
		},
		{
			name: "should let the default profile beat the default",
			reading: reading{
				defaultProfile: "work", profile: map[string]string{"provider": "typesafe", "model": "p"},
			},
			want:        values{"model": "p", "profile": "work", "provider": "typesafe"},
			wantSources: map[settings.Name]settings.Source{"model": settings.SourceDefaultProfile},
		},
		{
			name:        "should let the environment beat the default, which never prints",
			reading:     reading{env: map[string]string{"ONESIE_CACHE": "1"}},
			want:        values{"cache": true},
			wantSources: map[settings.Name]settings.Source{"cache": settings.SourceEnv},
		},
		{
			name:        "should let a flag beat the environment",
			reading:     reading{args: []string{"--cache=false"}, env: map[string]string{"ONESIE_CACHE": "1"}},
			want:        values{"cache": false},
			wantSources: map[settings.Name]settings.Source{"cache": settings.SourceFlag},
			wantNotices: []string{"--cache overrides ONESIE_CACHE"},
		},
		{
			name:        "should take a row's default when no source gives it",
			reading:     reading{},
			want:        values{"jobs": 1, "input": "text"},
			wantSources: map[settings.Name]settings.Source{"jobs": settings.SourceDefault},
		},
		{
			name: "should keep a chosen profile's key over the provider key variable",
			reading: reading{
				args: []string{"--profile", "work"}, keyHeld: true, env: map[string]string{"TYPESAFE_API_KEY": "env"},
				profile: map[string]string{"provider": "typesafe"},
			},
			want:        values{"api_key": settings.HeldKey{}},
			wantSources: map[settings.Name]settings.Source{"api_key": settings.SourceChosenProfile},
			wantNotices: []string{"the profile's api_key overrides TYPESAFE_API_KEY"},
		},
		{
			name: "should let the provider key variable beat the default profile's key, with a notice",
			reading: reading{
				defaultProfile: "work", keyHeld: true, env: map[string]string{"TYPESAFE_API_KEY": "env"},
				profile: map[string]string{"provider": "typesafe"},
			},
			want:        values{"api_key": "env"},
			wantSources: map[settings.Name]settings.Source{"api_key": settings.SourceEnv},
			wantNotices: []string{"TYPESAFE_API_KEY overrides the default profile's api_key"},
		},
		{
			name: "should note each lower source of -m under a chosen profile, a body and TYPESAFE_DEFAULT_MODEL",
			reading: reading{
				args: []string{"--profile", "work", "-m", "x"}, env: map[string]string{"TYPESAFE_DEFAULT_MODEL": "e"},
				profile: map[string]string{"provider": "typesafe", "model": "p"},
				file:    &settings.FileValues{Body: true, TopLevel: map[string]any{"model": "b"}},
			},
			want: values{"model": "x"},
			wantNotices: []string{
				"--model overrides the profile's model", "--model overrides the request body's model",
				"--model overrides TYPESAFE_DEFAULT_MODEL",
			},
		},
		{
			name:        "should fall back to typesafe as the provider",
			reading:     reading{},
			want:        values{"provider": "typesafe"},
			wantSources: map[settings.Name]settings.Source{"provider": settings.SourceDefault},
		},
		{
			name:        "should fall back to typesafe as the provider on calibrate",
			reading:     reading{command: settings.CommandCalibrate},
			want:        values{"provider": "typesafe"},
			wantSources: map[settings.Name]settings.Source{"provider": settings.SourceDefault},
		},
		{
			name: "should fall back to typesafe under a profile that names no provider",
			reading: reading{
				args: []string{"--profile", "work"}, profile: map[string]string{"model": "p"},
			},
			want:        values{"provider": "typesafe", "profile": "work"},
			wantSources: map[settings.Name]settings.Source{"provider": settings.SourceDefault},
		},
		{
			name: "should run the profile --profile names on calibrate",
			reading: reading{
				command: settings.CommandCalibrate, args: []string{"--profile", "work"},
				profile: map[string]string{"provider": "cloudflare", "model": "p"},
			},
			want: values{"profile": "work", "provider": "cloudflare", "model": "p"},
			wantSources: map[settings.Name]settings.Source{
				"profile": settings.SourceFlag, "provider": settings.SourceChosenProfile,
			},
		},
		{
			name: "should take the endpoint whole from the higher source",
			reading: reading{
				defaultProfile: "cf", env: map[string]string{"ONESIE_BASE_URL": "https://e.test"},
				profile: map[string]string{"provider": "cloudflare", "account_id": account},
			},
			want:        values{"endpoint": settings.EndpointValue{BaseURL: "https://e.test"}},
			wantSources: map[settings.Name]settings.Source{"endpoint": settings.SourceEnv},
			wantNotices: []string{"ONESIE_BASE_URL overrides the default profile's account_id"},
		},
		{
			name: "should keep a chosen profile's endpoint whole over the environment",
			reading: reading{
				args: []string{"--profile", "cf"}, env: map[string]string{"ONESIE_BASE_URL": "https://e.test"},
				profile: map[string]string{"provider": "cloudflare", "account_id": account},
			},
			want:        values{"endpoint": settings.EndpointValue{AccountID: account}},
			wantSources: map[settings.Name]settings.Source{"endpoint": settings.SourceChosenProfile},
			wantNotices: []string{"the profile's account_id overrides ONESIE_BASE_URL"},
		},
		{
			name: "should let --base-url beat a chosen profile's account",
			reading: reading{
				args:    []string{"--profile", "cf", "--base-url", "https://f.test"},
				profile: map[string]string{"provider": "cloudflare", "account_id": account},
			},
			want:        values{"endpoint": settings.EndpointValue{BaseURL: "https://f.test"}},
			wantNotices: []string{"--base-url overrides the profile's account_id"},
		},
		{
			name: "should record no notice for equal values",
			reading: reading{
				args: []string{"-m", "same"},
				env:  map[string]string{"TYPESAFE_DEFAULT_MODEL": "same"},
				file: &settings.FileValues{Body: true, TopLevel: map[string]any{"model": "same"}},
			},
			want: values{"model": "same"},
		},
		{
			name: "should skip an ambient value silently",
			reading: reading{
				args: []string{"--list-models"}, env: map[string]string{"ONESIE_MOCK": "m.json", "ONESIE_CACHE": "1"},
			},
			want:        values{"cache": false},
			wantSources: map[settings.Name]settings.Source{"cache": settings.SourceDefault, "mock": settings.SourceDefault},
		},
		{
			name: "should drop a calibrate file's assert, abstain_if and policy by Modes",
			reading: reading{
				command: settings.CommandCalibrate,
				file: &settings.FileValues{
					TopLevel: map[string]any{"assert": "answer.value > 0.5"},
					Questions: []settings.QuestionValues{{ID: "u", Values: map[string]any{
						"threshold": 0.7, "fallback": "no",
					}}},
				},
			},
			built: &plan.Plan{Questions: []plan.Question{{ID: "u"}}},
			check: func(t *testing.T, merged *settings.Merged) {
				t.Helper()

				if got := merged.Value("assert"); got != nil {
					t.Errorf("assert = %#v, want none", got)
				}

				if got, found := settings.GetFor(merged, "u", settings.Threshold); found {
					t.Errorf("threshold for u = %v, want none", got)
				}
			},
		},
		{
			name: "should take the file's assert as a list",
			reading: reading{
				file: &settings.FileValues{TopLevel: map[string]any{"assert": "answer.value > 0.5"}},
			},
			want:        values{"assert": []string{"answer.value > 0.5"}},
			wantSources: map[settings.Name]settings.Source{"assert": settings.SourceFile},
		},
		{
			name: "should let --assert replace the file's",
			reading: reading{
				args: []string{"--assert", "answer.value > 0.9"},
				file: &settings.FileValues{TopLevel: map[string]any{"assert": "answer.value > 0.5"}},
			},
			want:        values{"assert": []string{"answer.value > 0.9"}},
			wantNotices: []string{"--assert overrides the question file's assert"},
		},
		{
			name: "should name the default profile a --provider run goes without",
			reading: reading{
				args: []string{"--provider", "openrouter"}, defaultProfile: "work",
				profile: map[string]string{"provider": "typesafe", "model": "p"},
			},
			want:        values{"provider": "openrouter", "profile": "", "model": ""},
			wantSources: map[settings.Name]settings.Source{"provider": settings.SourceFlag, "profile": settings.SourceDefault},
			wantNotices: []string{"--provider openrouter runs without the default profile 'work'"},
		},
		{
			name: "should name the default profile an ONESIE_PROVIDER run goes without",
			reading: reading{
				env: map[string]string{"ONESIE_PROVIDER": "openrouter"}, defaultProfile: "work",
			},
			want:        values{"provider": "openrouter"},
			wantSources: map[settings.Name]settings.Source{"provider": settings.SourceEnv},
			wantNotices: []string{"ONESIE_PROVIDER=openrouter runs without the default profile 'work'"},
		},
		{
			name:        "should note nothing for a provider run with no default profile",
			reading:     reading{args: []string{"--provider", "openrouter"}},
			want:        values{"provider": "openrouter"},
			wantSources: map[settings.Name]settings.Source{"provider": settings.SourceFlag},
		},
		{
			name: "should note --profile over ONESIE_PROVIDER, and run the profile's provider",
			reading: reading{
				args: []string{"--profile", "work"}, env: map[string]string{"ONESIE_PROVIDER": "openrouter"},
				profile: map[string]string{"provider": "typesafe"},
			},
			want: values{"profile": "work", "provider": "typesafe"},
			wantSources: map[settings.Name]settings.Source{
				"profile": settings.SourceFlag, "provider": settings.SourceChosenProfile,
			},
			wantNotices: []string{"--profile work overrides ONESIE_PROVIDER"},
		},
		{
			name: "should note --provider over ONESIE_PROVIDER by the name it runs",
			reading: reading{
				args: []string{"--provider", "openrouter"}, env: map[string]string{"ONESIE_PROVIDER": "levanto"},
			},
			want:        values{"provider": "openrouter"},
			wantNotices: []string{"--provider openrouter overrides ONESIE_PROVIDER"},
		},
		{
			name: "should run the default profile when nothing chooses",
			reading: reading{
				defaultProfile: "work", profile: map[string]string{"provider": "cloudflare"},
			},
			want: values{"profile": "work", "provider": "cloudflare"},
			wantSources: map[settings.Name]settings.Source{
				"profile": settings.SourceDefaultProfile, "provider": settings.SourceDefaultProfile,
			},
		},
		{
			name: "should resolve a file question's settings per question",
			reading: reading{file: &settings.FileValues{Questions: []settings.QuestionValues{
				{ID: "urgent", Values: map[string]any{"threshold": 0.7}},
				{ID: "spam", Values: map[string]any{"threshold": 0.4}},
			}}},
			built: &plan.Plan{Questions: []plan.Question{{ID: "urgent"}, {ID: "spam"}}},
			check: func(t *testing.T, merged *settings.Merged) {
				t.Helper()

				for id, want := range map[string]float64{"urgent": 0.7, "spam": 0.4} {
					if got, found := settings.GetFor(merged, id, settings.Threshold); !found || got != want {
						t.Errorf("threshold for %s = %v, %t, want %v", id, got, found, want)
					}
				}
			},
		},
		{
			name: "should merge no question setting without a plan",
			reading: reading{file: &settings.FileValues{Questions: []settings.QuestionValues{
				{ID: "urgent", Values: map[string]any{"threshold": 0.7}},
			}}},
			check: func(t *testing.T, merged *settings.Merged) {
				t.Helper()

				if got, found := settings.GetFor(merged, "urgent", settings.Threshold); found {
					t.Errorf("threshold for urgent = %v, want none", got)
				}
			},
		},
		{
			name: "should note a flag that sets a setting a file question lacks",
			layers: []settings.Layer{
				{Source: settings.SourceFlag, Questions: map[string]map[settings.Name]any{"urgent": {"threshold": 0.9}}},
				{Source: settings.SourceFile, Questions: map[string]map[settings.Name]any{"urgent": {}}},
			},
			built:       &plan.Plan{Questions: []plan.Question{{ID: "urgent"}}},
			wantNotices: []string{"--threshold sets the question file's threshold for 'urgent'"},
			check:       wantThreshold("urgent", 0.9),
		},
		{
			name: "should note a flag that overrides a file question's setting",
			layers: []settings.Layer{
				{Source: settings.SourceFlag, Questions: map[string]map[settings.Name]any{"urgent": {"threshold": 0.9}}},
				{Source: settings.SourceFile, Body: true, Questions: map[string]map[settings.Name]any{
					"urgent": {"threshold": 0.7},
				}},
			},
			built:       &plan.Plan{Questions: []plan.Question{{ID: "urgent"}}},
			wantNotices: []string{"--threshold overrides the request body's threshold for 'urgent'"},
			check:       wantThreshold("urgent", 0.9),
		},
		{
			name: "should note nothing for a flag on a question no file holds",
			layers: []settings.Layer{
				{Source: settings.SourceFlag, Questions: map[string]map[settings.Name]any{"urgent": {"threshold": 0.9}}},
			},
			built: &plan.Plan{Questions: []plan.Question{{ID: "urgent"}}},
			check: wantThreshold("urgent", 0.9),
		},
		{
			name: "should merge a profile's key as HeldKey with its rank",
			reading: reading{
				defaultProfile: "work", keyHeld: true, profile: map[string]string{"provider": "typesafe"},
			},
			want:        values{"api_key": settings.HeldKey{}},
			wantSources: map[settings.Name]settings.Source{"api_key": settings.SourceDefaultProfile},
		},
		{
			name: "should keep the default profile's key with its own base_url over the key variable",
			reading: reading{
				defaultProfile: "work", keyHeld: true,
				profile: map[string]string{"provider": "typesafe", "base_url": "https://work.example"},
				env:     map[string]string{"TYPESAFE_API_KEY": "secret"},
			},
			want: values{"api_key": settings.HeldKey{}},
			wantSources: map[settings.Name]settings.Source{
				"api_key": settings.SourceDefaultProfile, "endpoint": settings.SourceDefaultProfile,
			},
			wantNotices: []string{"the default profile's base_url overrides TYPESAFE_API_KEY, since a key goes " +
				"only with its own endpoint"},
		},
		{
			name: "should send no held key to ONESIE_BASE_URL",
			reading: reading{
				defaultProfile: "work", keyHeld: true, profile: map[string]string{"provider": "typesafe"},
				env: map[string]string{"ONESIE_BASE_URL": "https://env.example"},
			},
			want: values{"api_key": nil},
			wantNotices: []string{"ONESIE_BASE_URL overrides the default profile's api_key, since a key goes only " +
				"with its own endpoint"},
		},
		{
			name: "should send the key variable to TYPESAFE_BASE_URL over a chosen profile's key",
			reading: reading{
				args: []string{"--profile", "work"}, keyHeld: true, profile: map[string]string{"provider": "typesafe"},
				env: map[string]string{"TYPESAFE_BASE_URL": "https://env.example", "TYPESAFE_API_KEY": "secret"},
			},
			want:        values{"api_key": "secret"},
			wantSources: map[settings.Name]settings.Source{"api_key": settings.SourceEnv},
			wantNotices: []string{"TYPESAFE_BASE_URL overrides the profile's api_key, since a key goes only with " +
				"its own endpoint"},
		},
		{
			name: "should send no held key to the account CLOUDFLARE_ACCOUNT_ID names",
			reading: reading{
				defaultProfile: "cf", keyHeld: true, profile: map[string]string{"provider": "cloudflare"},
				env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": strings.Repeat("b", 32)},
			},
			want: values{"api_key": nil},
			wantNotices: []string{"CLOUDFLARE_ACCOUNT_ID overrides the default profile's api_key, since a key goes " +
				"only with its own endpoint"},
		},
		{
			name: "should rank the key freely on the provider's own base URL",
			reading: reading{
				defaultProfile: "work", keyHeld: true, profile: map[string]string{"provider": "typesafe"},
				env: map[string]string{"TYPESAFE_API_KEY": "secret", "ONESIE_BASE_URL": "https://api.typesafe.ai/"},
			},
			want:        values{"api_key": "secret"},
			wantNotices: []string{"TYPESAFE_API_KEY overrides the default profile's api_key"},
		},
		{
			name: "should leave a chosen profile with its own base_url and key alone",
			reading: reading{
				args: []string{"--profile", "work"}, keyHeld: true,
				profile: map[string]string{"provider": "typesafe", "base_url": "https://work.example"},
				env:     map[string]string{"TYPESAFE_API_KEY": "secret", "ONESIE_BASE_URL": "https://env.example"},
			},
			want: values{"api_key": settings.HeldKey{}},
			wantNotices: []string{
				"the profile's base_url overrides ONESIE_BASE_URL",
				"the profile's api_key overrides TYPESAFE_API_KEY",
			},
		},
		{
			name: "should pair --base-url with a chosen profile's key",
			reading: reading{
				args: []string{"--profile", "work", "--base-url", "https://flag.example"}, keyHeld: true,
				profile: map[string]string{"provider": "typesafe"},
			},
			want: values{"api_key": settings.HeldKey{}},
		},
		{
			name: "should pair --base-url with the key variable",
			reading: reading{
				args: []string{"--base-url", "https://flag.example"}, env: map[string]string{"TYPESAFE_API_KEY": "secret"},
			},
			want: values{"api_key": "secret"},
		},
		{
			name: "should send no default profile's key to --base-url",
			reading: reading{
				args: []string{"--base-url", "https://flag.example"}, defaultProfile: "work", keyHeld: true,
				profile: map[string]string{"provider": "typesafe"},
			},
			want: values{"api_key": nil},
			wantNotices: []string{"--base-url overrides the default profile's api_key, since a key goes only with " +
				"its own endpoint"},
		},
		{
			name:    "should map ONESIE_CACHE_TTL=0 to no alias lifetime",
			reading: reading{env: map[string]string{"ONESIE_CACHE_TTL": "0"}},
			check: func(t *testing.T, merged *settings.Merged) {
				t.Helper()

				view := settings.NewView(merged, nil, settings.Facts{})
				if got := settings.Derived(view, settings.CacheTTL); got != settings.NoAliasTTL {
					t.Errorf("Derived(cache_ttl) = %v, want NoAliasTTL", got)
				}
			},
		},
		{
			name: "should keep an unset ONESIE_CACHE_TTL at zero",
			check: func(t *testing.T, merged *settings.Merged) {
				t.Helper()

				view := settings.NewView(merged, nil, settings.Facts{})
				if got := settings.Derived(view, settings.CacheTTL); got != 0 {
					t.Errorf("Derived(cache_ttl) = %v, want 0", got)
				}
			},
		},
		{
			name: "should merge layers given in any order",
			layers: []settings.Layer{
				{Source: settings.SourceEnv, Values: map[settings.Name]any{"model": "e"}, Spelled: map[settings.Name]string{
					"model": "TYPESAFE_DEFAULT_MODEL",
				}},
				{Source: settings.SourceFlag, Values: map[settings.Name]any{"model": "f"}, Spelled: map[settings.Name]string{
					"model": "--model",
				}},
			},
			want:        values{"model": "f"},
			wantNotices: []string{"--model overrides TYPESAFE_DEFAULT_MODEL"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			layers := tc.layers
			if layers == nil {
				read, err := settings.Read(settings.Table(), tc.reading.sources(t))
				if err != nil {
					t.Fatalf("Read: %v", err)
				}

				layers = read
			}

			merged, notices := settings.Merge(settings.Table(), layers, tc.built)

			for name, want := range tc.want {
				if got := merged.Value(name); !reflect.DeepEqual(got, want) {
					t.Errorf("Value(%s) = %#v, want %#v", name, got, want)
				}
			}

			for name, want := range tc.wantSources {
				if got := merged.Source(name); got != want {
					t.Errorf("Source(%s) = %d, want %d", name, got, want)
				}
			}

			got := make([]string, 0, len(notices))
			for _, notice := range notices {
				got = append(got, notice.String())
			}

			if !slices.Equal(got, tc.wantNotices) && len(got)+len(tc.wantNotices) > 0 {
				t.Errorf("notices = %q, want %q", got, tc.wantNotices)
			}

			if tc.check != nil {
				tc.check(t, merged)
			}
		})
	}
}

func wantThreshold(id string, want float64) func(t *testing.T, merged *settings.Merged) {
	return func(t *testing.T, merged *settings.Merged) {
		t.Helper()

		if got, found := settings.GetFor(merged, id, settings.Threshold); !found || got != want {
			t.Errorf("threshold for %s = %v, %t, want %v", id, got, found, want)
		}
	}
}
