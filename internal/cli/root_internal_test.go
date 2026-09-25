package cli

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemaURLOf(t *testing.T) {
	t.Parallel()

	const base = "https://raw.githubusercontent.com/frodi-karlsson/onesie/"

	tests := []struct {
		name string
		tag  string
		want string
	}{
		{name: "should name the tag the binary was built from", tag: "v0.2.0", want: base + "v0.2.0/schema/questions.json"},
		{name: "should name a prerelease tag", tag: "v0.2.0-rc.1", want: base + "v0.2.0-rc.1/schema/questions.json"},
		{name: "should name main for a build with no tag", tag: "", want: base + "main/schema/questions.json"},
		{name: "should name main for a dev build", tag: "dev", want: base + "main/schema/questions.json"},
		{name: "should name main for a build past a tag", tag: "v0.1.0-3-gabc1234", want: base + "main/schema/questions.json"},
		{name: "should name main for a tag with no v", tag: "0.2.0", want: base + "main/schema/questions.json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := schemaURLOf(BuildInfo{Version: "1.2.3", Tag: tc.tag}); got != tc.want {
				t.Errorf("schemaURLOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithTerminalWidth(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"onesie-1.13.0","answers":{"team":{"type":"choice",` +
		`"choice":"x","confidence":1,"probabilities":{"x":1,"y":0}}}}`

	tests := []struct {
		name     string
		columns  int
		known    bool
		env      map[string]string
		wantBars int
	}{
		{
			name: "should size the bars to the terminal", columns: 60, known: true,
			wantBars: 35,
		},
		{
			name: "should size the bars to a wider terminal", columns: 100, known: true,
			wantBars: 75,
		},
		{
			name:     "should fall back to eighty columns when the terminal reports no width",
			wantBars: 55,
		},
		{
			name: "should prefer COLUMNS over the terminal", columns: 100, known: true,
			env:      map[string]string{"COLUMNS": "50"},
			wantBars: 25,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(answerHandler(answered))
			defer srv.Close()

			out, errOut, code := runAgainst(t,
				[]string{"--ask", "team=who owns this", "--pick", "x,y", "-o", "table"},
				"the server is down", srv.URL,
				WithLookupEnv(lookupFrom(tc.env)),
				WithTerminalWidth(func() (int, bool) { return tc.columns, tc.known }))

			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, ExitOK, out, errOut)
			}

			if got := strings.Count(out, "█"); got != tc.wantBars {
				t.Errorf("bar cells = %d, want %d\nstdout:\n%s", got, tc.wantBars, out)
			}
		})
	}
}
