package output

import "testing"

func TestCodeSpan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		inTable bool
		want    string
	}{
		{name: "should fence plain text in one backtick", text: "T-1", want: "`T-1`"},
		{name: "should fence a mention so it pings no one", text: "@someone", want: "`@someone`"},
		{name: "should fence an issue reference so it links nowhere", text: "#12", want: "`#12`"},
		{name: "should fence html so it renders as text", text: "<b>bold</b>", want: "`<b>bold</b>`"},
		{name: "should use a fence one longer than the longest backtick run", text: "a ``b`` c", want: "```a ``b`` c```"},
		{name: "should pad text that starts with a backtick", text: "`x", want: "`` `x ``"},
		{name: "should pad text that ends with a backtick", text: "x`", want: "`` x` ``"},
		{name: "should escape a pipe inside a table", text: "a|b", inTable: true, want: "`a\\|b`"},
		{name: "should keep a pipe outside a table", text: "a|b", want: "`a|b`"},
		{name: "should turn a newline into a space", text: "line one\nline two", want: "`line one line two`"},
		{name: "should turn a carriage return and a tab into spaces", text: "a\r\nb\tc", want: "`a  b c`"},
		{name: "should turn an escape code into a space", text: "\x1b[31mred", want: "` [31mred`"},
		{name: "should write nothing for empty text", text: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := codeSpan(tc.text, tc.inTable); got != tc.want {
				t.Errorf("codeSpan(%q, %v) = %q, want %q", tc.text, tc.inTable, got, tc.want)
			}
		})
	}
}
