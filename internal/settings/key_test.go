package settings_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestGet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		merged     func() *settings.Merged
		wantJobs   int
		wantSource settings.Source
	}{
		{
			name:       "should return the zero of a row with no source",
			merged:     func() *settings.Merged { return &settings.Merged{} },
			wantSource: settings.SourceDefault,
		},
		{
			name:       "should return the zero of a nil merge",
			merged:     func() *settings.Merged { return nil },
			wantSource: settings.SourceDefault,
		},
		{
			name: "should return a merged value with its source",
			merged: func() *settings.Merged {
				merged := &settings.Merged{}
				merged.Set(settings.Jobs.Name(), 4, settings.SourceFlag)

				return merged
			},
			wantJobs:   4,
			wantSource: settings.SourceFlag,
		},
		{
			name: "should return the zero of a value of another type",
			merged: func() *settings.Merged {
				merged := &settings.Merged{}
				merged.Set(settings.Jobs.Name(), "4", settings.SourceEnv)

				return merged
			},
			wantSource: settings.SourceEnv,
		},
	}

	t.Run("should return an empty string for a HeldKey", func(t *testing.T) {
		t.Parallel()

		merged := &settings.Merged{}
		merged.Set(settings.APIKey.Name(), settings.HeldKey{}, settings.SourceChosenProfile)

		if got := settings.Get(merged, settings.APIKey); got != "" {
			t.Errorf("Get(api_key) = %q, want an empty string", got)
		}
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			merged := tc.merged()

			if got := settings.Get(merged, settings.Jobs); got != tc.wantJobs {
				t.Errorf("Get = %d, want %d", got, tc.wantJobs)
			}

			if got := merged.Source(settings.Jobs.Name()); got != tc.wantSource {
				t.Errorf("Source = %d, want %d", got, tc.wantSource)
			}
		})
	}
}

func TestDerived(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		set  values
		tty  bool
		want string
	}{
		{name: "should default to table on a terminal", tty: true, want: "table"},
		{name: "should default to json off a terminal", want: "json"},
		{
			name: "should default to json when streaming even on a terminal",
			set:  values{"input": "jsonl"}, tty: true, want: "json",
		},
		{name: "should default to json under merge even on a terminal", set: values{"merge": true}, tty: true, want: "json"},
		{
			name: "should resolve an explicit auto under merge to json",
			set:  values{"output": "auto", "merge_key": "x"}, tty: true, want: "json",
		},
		{name: "should resolve an explicit auto on a terminal to table", set: values{"output": "auto"}, tty: true, want: "table"},
		{
			name: "should default to json on a terminal when --out takes the answers",
			set:  values{"out": "answers.jsonl"}, tty: true, want: "json",
		},
		{name: "should keep an explicit mode", set: values{"output": "values"}, tty: true, want: "values"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			view := run{set: tc.set, facts: settings.Facts{StdoutTTY: tc.tty}}.view(t)

			if got := settings.Derived(view, settings.Output); got != tc.want {
				t.Errorf("Derived(output) = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("should skip blank lines under --keep unless --skip-blank is given", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name  string
			set   values
			typed []settingrow.Name
			want  bool
		}{
			{name: "should skip them under --keep", set: values{"keep": "pass"}, want: true},
			{
				name: "should keep them under --keep --skip-blank=false", set: values{"keep": "pass", "skip_blank": false},
				typed: []settingrow.Name{"skip_blank"},
			},
			{name: "should keep them without --keep"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				if got := settings.Derived(run{set: tc.set, typed: tc.typed}.view(t), settings.SkipBlank); got != tc.want {
					t.Errorf("Derived(skip_blank) = %v, want %v", got, tc.want)
				}
			})
		}
	})

	t.Run("should return the merged value of a row that derives nothing", func(t *testing.T) {
		t.Parallel()

		view := run{set: values{"jobs": 4}}.view(t)

		if got := settings.Derived(view, settings.Jobs); got != 4 {
			t.Errorf("Derived(jobs) = %d, want 4", got)
		}
	})
}
