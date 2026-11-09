package settings_test

import (
	"slices"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestParseRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		command   settingrow.Command
		args      []string
		wantErr   string
		want      values
		wantTyped []settingrow.Name
		wantDef   []settingrow.Name
	}{
		{
			name:    "should report -o before --threshold",
			args:    []string{"-o", "jsno", "--threshold", "2"},
			wantErr: "-o takes auto, json, values, table, raw, csv, tsv or markdown, got 'jsno'",
		},
		{
			name:    "should report --delimiter before -i",
			args:    []string{"-i", "bogus", "--delimiter", "ab"},
			wantErr: "--delimiter takes auto or one character, got 'ab'",
		},
		{
			name:    "should refuse a negative job count",
			args:    []string{"-j", "-2"},
			wantErr: "-j takes a positive number of records in flight, got -2",
		},
		{
			name:    "should refuse a job count above the ceiling",
			args:    []string{"-j", "2000000"},
			wantErr: "-j takes at most 256 records in flight, got 2000000",
		},
		{
			name:    "should refuse a retry count above the ceiling",
			args:    []string{"--retries", "100001"},
			wantErr: "--retries takes at most 100, got 100001",
		},
		{
			name:    "should refuse a max retry after of zero",
			args:    []string{"--max-retry-after", "0"},
			wantErr: "--max-retry-after must be a positive number of seconds, got 0",
		},
		{
			name:    "should refuse a max retry after above the ceiling",
			args:    []string{"--max-retry-after", "86401"},
			wantErr: "--max-retry-after takes at most 86400 seconds, got 86401",
		},
		{
			name:      "should accept every number at its ceiling",
			args:      []string{"-j", "256", "--timeout", "86400", "--retries", "100", "--max-retry-after", "86400"},
			want:      values{"jobs": 256, "timeout": 86400, "retries": 100, "max_retry_after": 86400},
			wantTyped: []settingrow.Name{"jobs", "timeout", "retries", "max_retry_after"},
		},
		{
			name:      "should accept a retry count of zero",
			args:      []string{"--retries", "0"},
			want:      values{"retries": 0},
			wantTyped: []settingrow.Name{"retries"},
		},
		{
			name:      "should read md as markdown",
			args:      []string{"-o", "md"},
			want:      values{"output": "markdown"},
			wantTyped: []settingrow.Name{"output"},
		},
		{
			name:      "should read a typed value with its source",
			args:      []string{"-m", "x", "-q", "--out", "a.jsonl", "--delimiter", ";"},
			want:      values{"model": "x", "quiet": true, "out": "a.jsonl", "delimiter": ";"},
			wantTyped: []settingrow.Name{"model", "quiet", "out", "delimiter"},
		},
		{
			name:      "should read a repeated flag as a list",
			args:      []string{"--assert", "a.value", "--assert", "b.value", "--image-field", "img"},
			want:      values{"assert": []string{"a.value", "b.value"}, "image_field": []string{"img"}},
			wantTyped: []settingrow.Name{"assert", "image_field"},
		},
		{
			name: "should give a row nothing typed its default",
			args: []string{"-q"},
			want: values{
				"input": "text", "jobs": 1, "output": "",
				"timeout": int(limits.DefaultAttemptTimeout.Seconds()), "retries": limits.DefaultRetries,
			},
			wantDef: []settingrow.Name{"input", "jobs", "timeout", "retries", "output", "model"},
		},
		{
			name:    "should hold no group row",
			args:    []string{"--ask", "a=q", "--threshold", "0.5"},
			wantDef: []settingrow.Name{"output"},
		},
		{
			name:      "should leave calibrate's own -o and -i to its rules",
			command:   settingrow.CommandCalibrate,
			args:      []string{"-o", "values", "-i", "text", "-j", "4"},
			want:      values{"jobs": 4},
			wantTyped: []settingrow.Name{"jobs"},
		},
		{
			name:    "should check a number on calibrate",
			command: settingrow.CommandCalibrate,
			args:    []string{"-j", "0"},
			wantErr: "-j takes a positive number of records in flight, got 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			command := tc.command
			if command == 0 {
				command = settingrow.CommandRoot
			}

			registered := register(command)
			registered.parse(t, tc.args...)

			merged, err := settings.ParseRun(settingrow.Table(), registered.given, command)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("ParseRun error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseRun: %v", err)
			}

			checkParsed(t, merged, tc.want, tc.wantTyped, tc.wantDef)

			for _, name := range []settingrow.Name{"threshold", "ask", "pick"} {
				if got := merged.Value(name); got != nil {
					t.Errorf("Value(%s) = %v, want no value, since the row binds to a question", name, got)
				}
			}

			if command == settingrow.CommandCalibrate {
				for _, name := range []settingrow.Name{"output", "input"} {
					if merged.Value(name) != nil {
						t.Errorf("calibrate holds %s, want it left to calibrate's rules", name)
					}
				}
			}
		})
	}
}

func checkParsed(t *testing.T, merged *settings.Merged, want values, typed, untyped []settingrow.Name) {
	t.Helper()

	for name, value := range want {
		got := merged.Value(name)

		list, isList := value.([]string)
		if isList {
			if gotList, ok := got.([]string); !ok || !slices.Equal(gotList, list) {
				t.Errorf("Value(%s) = %#v, want %#v", name, got, list)
			}

			continue
		}

		if got != value {
			t.Errorf("Value(%s) = %#v, want %#v", name, got, value)
		}
	}

	for _, name := range typed {
		if got := merged.Source(name); got != settings.SourceFlag {
			t.Errorf("Source(%s) = %d, want the flag", name, got)
		}
	}

	for _, name := range untyped {
		if merged.Value(name) == nil {
			t.Errorf("no value for %s, want its default", name)
		}

		if got := merged.Source(name); got != settings.SourceDefault {
			t.Errorf("Source(%s) = %d, want the default", name, got)
		}
	}
}
