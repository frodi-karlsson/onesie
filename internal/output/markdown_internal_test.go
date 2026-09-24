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
		{name: "should write html code with entities for a pipe inside a table", text: "a|b", inTable: true, want: "<code>a&#124;b</code>"},
		{
			name: "should keep a backslash before a pipe from splitting the cell", text: `a\|b`, inTable: true,
			want: "<code>a&#92;&#124;b</code>",
		},
		{
			name: "should entity encode markup and mentions inside html code", text: "<b>@x</b> & *y* | `z`", inTable: true,
			want: "<code>&#60;b&#62;&#64;x&#60;&#47;b&#62; &#38; &#42;y&#42; &#124; &#96;z&#96;</code>",
		},
		{name: "should keep a backtick span in a table when there is no pipe", text: "a&b", inTable: true, want: "`a&b`"},
		{name: "should keep a pipe outside a table", text: "a|b", want: "`a|b`"},
		{name: "should turn a newline into a space", text: "line one\nline two", want: "`line one line two`"},
		{name: "should turn a carriage return and a tab into spaces", text: "a\r\nb\tc", want: "`a  b c`"},
		{name: "should turn an escape code into a space", text: "\x1b[31mred", want: "` [31mred`"},
		{name: "should pad text that starts and ends with a space", text: " a ", want: "`  a  `"},
		{name: "should not pad text that is only spaces", text: "  ", want: "`  `"},
		{name: "should pad text whose spaces enclose only a non breaking space", text: " \u00a0 ", want: "`  \u00a0  `"},
		{name: "should not pad text with a space at one end", text: " a", want: "` a`"},
		{name: "should mark empty text so the cell is not blank", text: "", want: "_empty_"},
		{name: "should mark empty text inside a table too", text: "", inTable: true, want: "_empty_"},
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
