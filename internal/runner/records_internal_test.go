package runner

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestUnwritableID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode output.Mode
		id   string
		want string
	}{
		{
			name: "should escape a single quote inside the single quoted id",
			mode: output.CSV,
			id:   "it's\r",
			want: `--id: 'it\'s\r' holds a carriage return, which -o csv cannot read back`,
		},
		{
			name: "should leave a double quote unescaped inside the single quoted id",
			mode: output.CSV,
			id:   "say \"hi\"\r",
			want: `--id: 'say "hi"\r' holds a carriage return, which -o csv cannot read back`,
		},
		{
			name: "should escape a tab and a newline as they are written in Go",
			mode: output.TSV,
			id:   "a\tb\nc",
			want: `--id: 'a\tb\nc' holds a tab, carriage return or newline, which -o tsv cannot write`,
		},
		{
			name: "should keep a backslash before a quote apart from the escaped quote",
			mode: output.CSV,
			id:   "a\\'\r",
			want: `--id: 'a\\\'\r' holds a carriage return, which -o csv cannot read back`,
		},
		{
			name: "should accept an id the output mode can write",
			mode: output.CSV,
			id:   "it's",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := UnwritableID(tc.mode, tc.id, plan.Flags)
			if got := errorText(err); got != tc.want {
				t.Errorf("unwritableID(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestDelimiterHint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    input.Mode
		comma   rune
		header  []string
		speller plan.Speller
		want    string
	}{
		{
			name: "should name the input flag the way the speller does",
			mode: input.CSV,
			want: "-i csv splits each line on commas, so if the file is tab separated, pass -i tsv",
		},
		{
			name:    "should name the input setting as a tool argument for that speller",
			mode:    input.TSV,
			speller: plan.Arguments,
			want:    "input tsv splits each line on tabs, so if the file is comma separated, pass input csv",
		},
		{
			name:   "should name the delimiter setting for a one column header holding semicolons",
			mode:   input.CSV,
			header: []string{"id;text"},
			want: "-i csv splits each line on commas, so if the file is semicolon separated, " +
				"pass --delimiter ';' or --delimiter auto",
		},
		{
			name: "should name a tab the csv input split on", mode: input.CSV, comma: '\t', header: []string{"id;text"},
			want: "-i csv splits each line on tabs, so if the file is semicolon separated, " +
				"pass --delimiter ';' or --delimiter auto",
		},
		{
			name: "should quote a delimiter that has no plural name", mode: input.CSV, comma: '|',
			want: "-i csv splits each line on '|', so if the file is tab separated, pass -i tsv",
		},
		{name: "should give no hint for a mode with no delimiter", mode: input.JSONL},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := DelimiterHint(tc.mode, tc.comma, tc.header, tc.speller); got != tc.want {
				t.Errorf("DelimiterHint = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStoredFailure_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		speller plan.Speller
		want    string
	}{
		{
			name: "should name the flags to carry on",
			want: "line 2: not json. The stored failure for record 3 is followed by more lines, so a run without " +
				"--stop-on-error wrote it. Pass --id or drop --stop-on-error to carry on",
		},
		{
			name:    "should name the settings as tool arguments for that speller",
			speller: plan.Arguments,
			want: "line 2: not json. The stored failure for record 3 is followed by more lines, so a run without " +
				"stop_on_error wrote it. Pass id or drop stop_on_error to carry on",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored := &StoredFailure{Failure: output.Failure{Message: "line 2: not json."}, Record: 3, speller: tc.speller}
			if got := stored.Error(); got != tc.want {
				t.Errorf("Error = %q, want %q", got, tc.want)
			}
		})
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
