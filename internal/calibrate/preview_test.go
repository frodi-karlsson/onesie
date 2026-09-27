package calibrate_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
)

func TestPreview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "should keep a short text as it is", text: "rm -rf /", want: "rm -rf /"},
		{name: "should fold line breaks and tabs into single spaces", text: "one\n\ntwo\tthree\r\n", want: "one two three"},
		{name: "should strip control characters", text: "bell\a and \x1b[31mred", want: "bell and [31mred"},
		{
			name: "should cut a long text at forty characters",
			text: strings.Repeat("abcde ", 10),
			want: "abcde abcde abcde abcde abcde abcde abcd...",
		},
		{name: "should count characters rather than bytes", text: strings.Repeat("é", 41), want: strings.Repeat("é", 40) + "..."},
		{name: "should drop a bidi override", text: "safe\u202etxt.exe", want: "safetxt.exe"},
		{name: "should leave an empty text empty", text: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := calibrate.Preview(tc.text); got != tc.want {
				t.Errorf("Preview(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
