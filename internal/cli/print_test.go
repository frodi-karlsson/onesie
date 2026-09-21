package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
)

func TestNewRootCmdPrintQuestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCode int
		contains []string
		absent   []string
	}{
		{
			name:     "should write a question file and exit without a request",
			args:     []string{"--ask", "urgent=is this urgent", "--print-questions"},
			wantCode: cli.ExitOK,
			contains: []string{"urgent:", "ask: is this urgent"},
		},
		{
			name: "should write the questions in the order they were asked",
			args: []string{
				"--ask", "zebra=z", "--ask", "mike=m", "--ask", "alpha=a", "--print-questions",
			},
			wantCode: cli.ExitOK,
			contains: []string{"zebra:\n  ask: z\nmike:\n  ask: m\nalpha:\n  ask: a\n"},
		},
		{
			name: "should write the policy keys the loader reads",
			args: []string{
				"--ask", "team=who owns this", "--pick", "billing,platform",
				"--min-confidence", "0.7", "--fallback", "billing", "--print-questions",
			},
			wantCode: cli.ExitOK,
			contains: []string{"min_confidence: 0.7", "fallback: billing"},
			absent:   []string{"min-confidence"},
		},
		{
			name: "should write a yes and no rubric under the _means keys",
			args: []string{
				"--ask", "spam=is this spam", "--desc", "yes=bulk or bot sent",
				"--desc", "no=written by a person", "--print-questions",
			},
			wantCode: cli.ExitOK,
			contains: []string{"yes_means: bulk or bot sent", "no_means: written by a person"},
		},
		{
			name:     "should reject print-questions for a positional question",
			args:     []string{"is this urgent", "--print-questions"},
			wantCode: cli.ExitUsage,
			contains: []string{"--print-questions needs a named question"},
		},
		{
			name:     "should report the missing key when the run does make a request",
			args:     []string{"--ask", "urgent=is this urgent", "--state", "the server is down"},
			wantCode: cli.ExitUsage,
			contains: []string{"jev: no API key"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, code := runOffline(t, tc.args)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\noutput:\n%s", code, tc.wantCode, out)
			}

			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\ngot:\n%s", want, out)
				}
			}

			for _, unwanted := range tc.absent {
				if strings.Contains(out, unwanted) {
					t.Errorf("output should not contain %q\ngot:\n%s", unwanted, out)
				}
			}
		})
	}
}

func runOffline(t *testing.T, args []string) (string, int) {
	t.Helper()

	var out bytes.Buffer

	// No client factory and no environment, so the real factory runs against a machine with no key
	// and no state on stdin. A case that exits ok here reached neither the network nor stdin, which
	// is the whole claim --print-questions makes.
	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithStdin(strings.NewReader("")),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
	)

	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	code := cli.Execute(t.Context(), root)

	return out.String(), code
}
