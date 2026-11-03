package input_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/input"
)

func TestDetectDelimiter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   rune
	}{
		{name: "should detect a comma", header: "id,body,label\n", want: ','},
		{name: "should detect a semicolon", header: "id;body;label\n", want: ';'},
		{name: "should detect a tab", header: "id\tbody\tlabel\n", want: '\t'},
		{name: "should ignore a delimiter inside quotes", header: `"a;b;c",id,body` + "\n", want: ','},
		{name: "should prefer a comma on a tie", header: "a,b;c\n", want: ','},
		{name: "should prefer a comma when a semicolon and a tab tie", header: "a;b\tc\n", want: ','},
		{name: "should take a comma for a one column header", header: "body\n", want: ','},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := input.DetectDelimiter(tc.header); got != tc.want {
				t.Errorf("DetectDelimiter(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

func TestParseDelimiter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    input.Delimiter
		wantErr string
	}{
		{name: "should take auto", value: "auto", want: input.Delimiter{Detect: true}},
		{name: "should take one character", value: ";", want: input.Delimiter{Comma: ';'}},
		{name: "should take one character past ascii", value: "§", want: input.Delimiter{Comma: '§'}},
		{name: "should refuse two characters", value: ";;", wantErr: "takes auto or one character, got ';;'"},
		{name: "should refuse an empty value", value: "", wantErr: "takes auto or one character, got ''"},
		{name: "should refuse a newline", value: "\n", wantErr: `cannot be a quote or a line break, got "\n"`},
		{name: "should refuse a quote", value: `"`, wantErr: `cannot be a quote or a line break, got "\""`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := input.ParseDelimiter(tc.value)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("ParseDelimiter(%q) error = %v, want %q", tc.value, err, tc.wantErr)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Errorf("ParseDelimiter(%q) = %+v, %v, want %+v", tc.value, got, err, tc.want)
			}
		})
	}
}
