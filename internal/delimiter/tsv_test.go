package delimiter

import (
	"reflect"
	"testing"
)

func TestTSVFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want []string
	}{
		{name: "should decode LF framing", line: "a\tb\n", want: []string{"a", "b"}},
		{name: "should decode CRLF framing", line: "a\tb\r\n", want: []string{"a", "b"}},
		{name: "should preserve data CR before CRLF", line: "x\r\r\n", want: []string{"x\r"}},
		{name: "should preserve final CR in a live payload", line: "x\r", want: []string{"x\r"}},
		{name: "should preserve quotes and empty cells", line: "\t\"a\"\t", want: []string{"", `"a"`, ""}},
		{name: "should leave blank record policy to the caller", line: "\n", want: []string{""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TSVFields(tc.line); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("TSVFields = %q, want %q", got, tc.want)
			}
		})
	}
}
