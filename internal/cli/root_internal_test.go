package cli

import (
	"net/http/httptest"
	"strings"
	"testing"
)

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
