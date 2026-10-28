package cli

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/output"
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

			err := unwritableID(tc.mode, tc.id)
			if got := errorText(err); got != tc.want {
				t.Errorf("unwritableID(%q) = %q, want %q", tc.id, got, tc.want)
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
