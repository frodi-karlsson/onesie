package output_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestPrintable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "should keep plain text, newlines and tabs", text: "a\tb\nc é", want: "a\tb\nc é"},
		{name: "should drop an escape sequence introducer", text: "a\x1b[2Jb", want: "a[2Jb"},
		{name: "should drop a bell, a carriage return and a delete", text: "a\x07b\rc\x7fd", want: "abcd"},
		{name: "should drop a C1 control", text: "a\u009bb\u0085c", want: "abc"},
		{name: "should replace a raw byte that is not UTF-8", text: "a\x9bb", want: "a�b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := output.Printable(tc.text); got != tc.want {
				t.Errorf("Printable(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
