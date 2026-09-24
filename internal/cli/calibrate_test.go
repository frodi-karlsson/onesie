package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

func TestNewCalibrateCmd(t *testing.T) {
	t.Parallel()

	const notYet = "onesie: calibrate cannot ask yet"

	dir := t.TempDir()

	assertFile := writeCalibrateFile(t, dir, "assert.yaml",
		"assert: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n")
	thresholdFile := writeCalibrateFile(t, dir, "threshold.yaml",
		"urgent:\n  ask: is this urgent\n  threshold: 0.8\n")
	confidenceFile := writeCalibrateFile(t, dir, "confidence.yaml",
		"team:\n  ask: which team\n  pick: [billing, shipping]\n  min_confidence: 0.8\n")
	fallbackFile := writeCalibrateFile(t, dir, "fallback.yaml",
		"urgent:\n  ask: is this urgent\n  fallback: no\n")
	abstainFile := writeCalibrateFile(t, dir, "abstain.yaml",
		"abstain_if: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n")
	bodyFile := writeCalibrateFile(t, dir, "body.json",
		`{"questions":{"mood":{"type":"score","instructions":"how cross is the writer",`+
			`"criteria":["calm","annoyed","furious"]}}}`)

	base := []string{
		"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
		"--label", "urgent=.is_urgent",
	}

	with := func(extra ...string) []string {
		return append(append([]string{}, base...), extra...)
	}

	tests := []struct {
		name     string
		args     []string
		wantCode int
		contains []string
	}{
		{
			name:     "should print help when given nothing",
			args:     []string{"calibrate"},
			wantCode: ExitOK,
			contains: []string{"Usage:", "onesie calibrate [question]"},
		},
		{
			name:     "should reach the end of the checks with a valid command line",
			args:     base,
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name: "should refuse -i text",
			args: []string{
				"calibrate", "--ask", "urgent=q", "-i", "text", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{"jsonl, csv or tsv", "-i text"},
		},
		{
			name: "should refuse -i json",
			args: []string{
				"calibrate", "--ask", "urgent=q", "-i", "json", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{"jsonl, csv or tsv", "-i json"},
		},
		{
			name: "should refuse -i lines",
			args: []string{
				"calibrate", "--ask", "urgent=q", "-i", "lines", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{"jsonl, csv or tsv", "-i lines"},
		},
		{
			name: "should refuse -i request",
			args: []string{
				"calibrate", "--ask", "urgent=q", "-i", "request", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{"jsonl, csv or tsv", "-i request"},
		},
		{
			name:     "should refuse a missing -i",
			args:     []string{"calibrate", "--ask", "urgent=q", "--map", ".body", "--label", "urgent=.u"},
			wantCode: ExitUsage,
			contains: []string{"jsonl, csv or tsv"},
		},
		{
			name:     "should accept -i csv",
			args:     []string{"calibrate", "--ask", "urgent=q", "-i", "csv", "--map", ".body", "--label", "urgent=.u"},
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name:     "should accept -i tsv",
			args:     []string{"calibrate", "--ask", "urgent=q", "-i", "tsv", "--map", ".body", "--label", "urgent=.u"},
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name:     "should refuse a missing --map",
			args:     []string{"calibrate", "--ask", "urgent=q", "-i", "jsonl", "--label", "urgent=.u"},
			wantCode: ExitUsage,
			contains: []string{"--map", "the whole record, label included, would be the state"},
		},
		{
			name:     "should refuse --assert",
			args:     with("--assert", "urgent.value > 0.5"),
			wantCode: ExitUsage,
			contains: []string{"so --assert does not apply. Drop it"},
		},
		{
			name:     "should refuse --abstain-if",
			args:     with("--abstain-if", "urgent.value > 0.5"),
			wantCode: ExitUsage,
			contains: []string{"so --abstain-if does not apply. Drop it"},
		},
		{
			name:     "should refuse --threshold",
			args:     with("--threshold", "0.5"),
			wantCode: ExitUsage,
			contains: []string{"onesie: calibrate reports every cut, so --threshold does not apply. Drop it"},
		},
		{
			name:     "should refuse --min-confidence",
			args:     with("--min-confidence", "0.5"),
			wantCode: ExitUsage,
			contains: []string{"so --min-confidence does not apply. Drop it"},
		},
		{
			name:     "should refuse --fallback",
			args:     with("--fallback", "no"),
			wantCode: ExitUsage,
			contains: []string{"so --fallback does not apply. Drop it"},
		},
		{
			name:     "should refuse --merge",
			args:     with("--merge"),
			wantCode: ExitUsage,
			contains: []string{"so --merge does not apply. Drop it"},
		},
		{
			name:     "should refuse --merge-key",
			args:     with("--merge-key", "k"),
			wantCode: ExitUsage,
			contains: []string{"so --merge-key does not apply. Drop it"},
		},
		{
			name:     "should refuse -q",
			args:     with("-q"),
			wantCode: ExitUsage,
			contains: []string{"so -q does not apply. Drop it"},
		},
		{
			name:     "should refuse --quiet by the spelling given",
			args:     with("--quiet"),
			wantCode: ExitUsage,
			contains: []string{"so --quiet does not apply. Drop it"},
		},
		{
			name:     "should refuse --raw by the spelling given",
			args:     with("--raw"),
			wantCode: ExitUsage,
			contains: []string{"so --raw does not apply. Drop it"},
		},
		{
			name:     "should refuse -r",
			args:     with("-r"),
			wantCode: ExitUsage,
			contains: []string{"so -r does not apply. Drop it"},
		},
		{
			name:     "should refuse --stop-on-error",
			args:     with("--stop-on-error"),
			wantCode: ExitUsage,
			contains: []string{"so --stop-on-error does not apply. Drop it"},
		},
		{
			name:     "should refuse --stop-on-assert",
			args:     with("--stop-on-assert"),
			wantCode: ExitUsage,
			contains: []string{"so --stop-on-assert does not apply. Drop it"},
		},
		{
			name:     "should refuse --unordered",
			args:     with("--unordered"),
			wantCode: ExitUsage,
			contains: []string{"so --unordered does not apply. Drop it"},
		},
		{
			name:     "should refuse --print-questions",
			args:     with("--print-questions"),
			wantCode: ExitUsage,
			contains: []string{"so --print-questions does not apply. Drop it"},
		},
		{
			name:     "should refuse --resume without --id",
			args:     with("--out", filepath.Join(dir, "answers.jsonl"), "--resume"),
			wantCode: ExitUsage,
			contains: []string{
				"onesie: calibrate resumes by id, since which records are asked follows the labels. Pass --id",
			},
		},
		{
			name:     "should refuse -o csv",
			args:     with("-o", "csv"),
			wantCode: ExitUsage,
			contains: []string{"table, json or auto", "csv"},
		},
		{
			name:     "should accept -o table",
			args:     with("-o", "table"),
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name:     "should accept -o json",
			args:     with("-o", "json"),
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name:     "should accept -o auto",
			args:     with("--output", "auto"),
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name:     "should refuse a cut above 1",
			args:     with("--cuts", "0.5,1.2"),
			wantCode: ExitUsage,
			contains: []string{"onesie: --cuts", "'1.2'"},
		},
		{
			name:     "should refuse a cut that is not a number",
			args:     with("--cuts", "x"),
			wantCode: ExitUsage,
			contains: []string{"onesie: --cuts", "'x'"},
		},
		{
			name:     "should refuse an empty list of cuts",
			args:     with("--cuts", ""),
			wantCode: ExitUsage,
			contains: []string{"onesie: --cuts", "got nothing"},
		},
		{
			name:     "should accept a list of cuts",
			args:     with("--cuts", "0.9,0.95"),
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name:     "should refuse a question with no --label",
			args:     with("--ask", "team=which team", "--pick", "billing,shipping"),
			wantCode: ExitUsage,
			contains: []string{"question 'team' has no --label"},
		},
		{
			name:     "should refuse a --label for an unknown question",
			args:     with("--label", "tema=.team"),
			wantCode: ExitUsage,
			contains: []string{"onesie: --label names an unknown question 'tema'. Questions: urgent"},
		},
		{
			name:     "should refuse two labels for one question",
			args:     with("--label", "urgent=.other"),
			wantCode: ExitUsage,
			contains: []string{"--label", "twice", "'urgent'"},
		},
		{
			name: "should accept a bare label for a positional question",
			args: []string{
				"calibrate", "is this urgent", "-i", "jsonl", "--map", ".body", "--label", ".is_urgent",
			},
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name: "should refuse an assert key in a question file",
			args: []string{
				"calibrate", "-f", assertFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{"so 'assert' does not apply. Drop it"},
		},
		{
			name: "should refuse a threshold in a question file",
			args: []string{
				"calibrate", "-f", thresholdFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{"so 'threshold' does not apply. Drop it"},
		},
		{
			name: "should refuse a min_confidence in a question file",
			args: []string{
				"calibrate", "-f", confidenceFile, "-i", "jsonl", "--map", ".body", "--label", "team=.team",
			},
			wantCode: ExitUsage,
			contains: []string{"so 'min_confidence' does not apply. Drop it"},
		},
		{
			name: "should refuse a fallback in a question file",
			args: []string{
				"calibrate", "-f", fallbackFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{"so 'fallback' does not apply. Drop it"},
		},
		{
			name: "should refuse an abstain_if key in a question file",
			args: []string{
				"calibrate", "-f", abstainFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{"so 'abstain_if' does not apply. Drop it"},
		},
		{
			name: "should refuse a request body with an unlabelled rate question",
			args: []string{
				"calibrate", "-f", bodyFile, "-i", "jsonl", "--map", ".body", "--label", "mood=.mood",
			},
			wantCode: ExitUsage,
			contains: []string{"onesie: question 'mood' from a request body has no level names to label with"},
		},
		{
			name:     "should refuse --usage with neither --out nor -o json",
			args:     with("--usage"),
			wantCode: ExitUsage,
			contains: []string{
				"onesie: --usage adds token counts to the answers file or the json report, " +
					"and this run writes neither",
			},
		},
		{
			name:     "should accept --usage with -o json",
			args:     with("--usage", "-o", "json"),
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name:     "should accept --usage with --out",
			args:     with("--usage", "--out", filepath.Join(dir, "usage.jsonl")),
			wantCode: ExitUsage,
			contains: []string{notYet},
		},
		{
			name:     "should say in its help that -i is required and hide the default",
			args:     []string{"calibrate", "--help"},
			wantCode: ExitOK,
			contains: []string{"required, jsonl, csv or tsv"},
		},
		{
			name:     "should refuse --out under --print-request",
			args:     with("--print-request", "--out", filepath.Join(dir, "bodies.jsonl")),
			wantCode: ExitUsage,
			contains: []string{"--out", "--print-request"},
		},
		{
			name:     "should refuse --resume under --print-request",
			args:     with("--print-request", "--id", ".id", "--out", filepath.Join(dir, "resume.jsonl"), "--resume"),
			wantCode: ExitUsage,
			contains: []string{"--print-request writes request bodies, not answers, so --resume has nothing"},
		},
		{
			name:     "should refuse --resume under --print-request without --id",
			args:     with("--print-request", "--out", filepath.Join(dir, "resume.jsonl"), "--resume"),
			wantCode: ExitUsage,
			contains: []string{"so --resume has nothing to pick up. Drop --resume"},
		},
		{
			name:     "should refuse --stats under --print-request",
			args:     with("--print-request", "--stats"),
			wantCode: ExitUsage,
			contains: []string{"--stats has nothing to report with --print-request"},
		},
		{
			name:     "should refuse --usage under --print-request",
			args:     with("--print-request", "--usage"),
			wantCode: ExitUsage,
			contains: []string{"--usage reports the tokens a question cost, which --print-request does not ask"},
		},
		{
			name:     "should refuse -o under --print-request",
			args:     with("--print-request", "-o", "json"),
			wantCode: ExitUsage,
			contains: []string{"onesie: -o does not apply to --print-request, which writes a request body"},
		},
		{
			name:     "should refuse a flag calibrate does not register",
			args:     with("--state", "x"),
			wantCode: ExitUsage,
			contains: []string{"unknown flag: --state"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOfflineStdin(t, tc.args, "")

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, out, errOut)
			}

			combined := out + errOut
			for _, want := range tc.contains {
				if !strings.Contains(combined, want) {
					t.Errorf("output missing %q\nstdout:\n%s\nstderr:\n%s", want, out, errOut)
				}
			}

			if strings.Contains(combined, `default "text"`) {
				t.Errorf("output advertises the text default\n%s", combined)
			}

			if tc.wantCode != ExitOK && out != "" {
				t.Errorf("stdout = %q, want nothing on a refusal", out)
			}
		})
	}

	t.Run("should refuse a label with a syntax error before reading any input", func(t *testing.T) {
		t.Parallel()

		var out, errOut bytes.Buffer

		root := NewRootCmd(
			BuildInfo{Version: "1.2.3"},
			WithKeychain(noKeychain()),
			WithStdin(unreadable{t: t}),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
		)

		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{
			"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
			"--label", "urgent=.is_urgent |",
		})

		code := Execute(t.Context(), root)
		if code != ExitUsage {
			t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, ExitUsage, errOut.String())
		}

		if !strings.Contains(errOut.String(), "onesie: --label urgent:") {
			t.Errorf("stderr = %q, want it to name the label", errOut.String())
		}
	})
}

func writeCalibrateFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}

	return path
}

type unreadable struct {
	t *testing.T
}

func (u unreadable) Read([]byte) (int, error) {
	u.t.Error("stdin was read, want the label checked first")

	return 0, io.EOF
}

func TestReadLabelled(t *testing.T) {
	t.Parallel()

	dry := func(extra ...string) []string {
		return append([]string{"calibrate", "--print-request", "--ask", "urgent=is this urgent"}, extra...)
	}

	jsonl := func(extra ...string) []string {
		return dry(append([]string{"-i", "jsonl", "--map", ".body", "--label", "urgent=.u"}, extra...)...)
	}

	both := jsonl("--ask", "team=which team", "--pick", "billing,shipping", "--label", "team=.team")

	tests := []struct {
		name       string
		args       []string
		stdin      string
		wantCode   int
		wantStates []string
		wantAsked  []string
		contains   []string
	}{
		{
			name:       "should print one body per labelled jsonl record",
			args:       jsonl(),
			stdin:      "{\"body\":\"a\",\"u\":true}\n{\"body\":\"b\",\"u\":\"no\"}\n",
			wantStates: []string{`"a"`, `"b"`},
			wantAsked:  []string{"urgent"},
		},
		{
			name: "should print one body per labelled csv row",
			args: dry("-i", "csv", "--map", ".body", "--label", "urgent=.u"),
			stdin: "body,u\n" +
				"a,yes\n" +
				"b,0\n",
			wantStates: []string{`"a"`, `"b"`},
			wantAsked:  []string{"urgent"},
		},
		{
			name:       "should print one body per labelled tsv row",
			args:       dry("-i", "tsv", "--map", ".body", "--label", "urgent=.u"),
			stdin:      "body\tu\na\ttrue\nb\tFALSE\n",
			wantStates: []string{`"a"`, `"b"`},
			wantAsked:  []string{"urgent"},
		},
		{
			name:       "should skip a record no question labels",
			args:       jsonl(),
			stdin:      "{\"body\":\"a\",\"u\":true}\n{\"body\":\"b\"}\n{\"body\":\"c\",\"u\":0}\n",
			wantStates: []string{`"a"`, `"c"`},
			wantAsked:  []string{"urgent"},
		},
		{
			name:       "should ask every question of a record one question labels",
			args:       both,
			stdin:      "{\"body\":\"a\",\"u\":true}\n{\"body\":\"b\",\"team\":\"billing\"}\n{\"body\":\"c\"}\n",
			wantStates: []string{`"a"`, `"b"`},
			wantAsked:  []string{"urgent", "team"},
		},
		{
			name:       "should leave a record unlabelled by null and by an empty string",
			args:       jsonl(),
			stdin:      "{\"body\":\"a\",\"u\":null}\n{\"body\":\"b\",\"u\":\"\"}\n{\"body\":\"c\",\"u\":1}\n",
			wantStates: []string{`"c"`},
			wantAsked:  []string{"urgent"},
		},
		{
			name:       "should leave a record unlabelled by a label with no result",
			args:       dry("-i", "jsonl", "--map", ".body", "--label", `urgent=.u | select(. != "skip")`),
			stdin:      "{\"body\":\"a\",\"u\":\"skip\"}\n{\"body\":\"b\",\"u\":\"yes\"}\n",
			wantStates: []string{`"b"`},
			wantAsked:  []string{"urgent"},
		},
		{
			name:     "should name a record by its id when a label is bad",
			args:     jsonl("--id", ".id"),
			stdin:    "{\"id\":\"T-1\",\"body\":\"a\",\"u\":true}\n{\"id\":\"T-7\",\"body\":\"b\",\"u\":\"maybe\"}\n",
			wantCode: ExitUsage,
			contains: []string{
				`onesie: --label urgent: record T-7: "maybe" is not a yes/no label. Use true, false, yes, no, 1 or 0`,
			},
		},
		{
			name:     "should name a record by its line when a label is bad and there is no --id",
			args:     jsonl(),
			stdin:    "{\"body\":\"a\",\"u\":true}\n{\"body\":\"b\",\"u\":\"maybe\"}\n",
			wantCode: ExitUsage,
			contains: []string{`onesie: --label urgent: line 2: "maybe" is not a yes/no label`},
		},
		{
			name:     "should quote a pick label that differs from the option in case",
			args:     both,
			stdin:    "{\"body\":\"a\",\"team\":\"Billing\"}\n",
			wantCode: ExitUsage,
			contains: []string{`onesie: --label team: line 1: "Billing" is not a pick label. Use billing or shipping`},
		},
		{
			name:     "should refuse a label that yields two values",
			args:     dry("-i", "jsonl", "--map", ".body", "--label", "urgent=.u, .u"),
			stdin:    "{\"body\":\"a\",\"u\":true}\n",
			wantCode: ExitUsage,
			contains: []string{"onesie: --label urgent: line 1: yields more than one value"},
		},
		{
			name:     "should refuse a label that fails at run time",
			args:     dry("-i", "jsonl", "--map", ".body", "--label", `urgent=error("boom")`),
			stdin:    "{\"body\":\"a\",\"u\":true}\n",
			wantCode: ExitUsage,
			contains: []string{"onesie: --label urgent: line 1:", "boom"},
		},
		{
			name:     "should refuse an unparseable line before printing any body",
			args:     jsonl(),
			stdin:    "{\"body\":\"a\",\"u\":true}\n{nope\n",
			wantCode: ExitUsage,
			contains: []string{"line 2"},
		},
		{
			name:     "should refuse a blank line before printing any body",
			args:     jsonl(),
			stdin:    "{\"body\":\"a\",\"u\":true}\n\n{\"body\":\"b\",\"u\":true}\n",
			wantCode: ExitUsage,
			contains: []string{"line 2", "blank line"},
		},
		{
			name:       "should drop a blank line under --skip-blank",
			args:       jsonl("--skip-blank"),
			stdin:      "{\"body\":\"a\",\"u\":true}\n\n{\"body\":\"b\",\"u\":true}\n",
			wantStates: []string{`"a"`, `"b"`},
			wantAsked:  []string{"urgent"},
		},
		{
			name:     "should refuse a failed --map before printing any body",
			args:     dry("-i", "jsonl", "--map", ".body | ascii_downcase", "--label", "urgent=.u"),
			stdin:    "{\"body\":\"a\",\"u\":true}\n{\"body\":{\"x\":1}}\n",
			wantCode: ExitUsage,
			contains: []string{"line 2: --map"},
		},
		{
			name:     "should refuse a failed --id before printing any body",
			args:     jsonl("--id", ".id"),
			stdin:    "{\"id\":\"T-1\",\"body\":\"a\",\"u\":true}\n{\"id\":{},\"body\":\"b\"}\n",
			wantCode: ExitUsage,
			contains: []string{"line 2: --id"},
		},
		{
			name:     "should refuse a repeated id before printing any body",
			args:     jsonl("--id", ".id"),
			stdin:    "{\"id\":\"T-1\",\"body\":\"a\",\"u\":true}\n{\"id\":\"T-1\",\"body\":\"b\"}\n",
			wantCode: ExitUsage,
			contains: []string{"line 2: --id: 'T-1' is also the id of line 1"},
		},
		{
			name:     "should refuse more records than the cap",
			args:     jsonl(),
			stdin:    strings.Repeat("{\"body\":\"a\",\"u\":true}\n", limits.MaxCalibrateRecords+1),
			wantCode: ExitUsage,
			contains: []string{"onesie: calibrate reads at most 100000 records, and -V lists the cap"},
		},
		{
			name:       "should read as many records as the cap",
			args:       jsonl(),
			stdin:      strings.Repeat("{\"body\":\"a\",\"u\":true}\n", limits.MaxCalibrateRecords),
			wantStates: slices.Repeat([]string{`"a"`}, limits.MaxCalibrateRecords),
			wantAsked:  []string{"urgent"},
		},
		{
			name: "should print nothing for empty input",
			args: jsonl(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOfflineStdin(t, tc.args, tc.stdin)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, out, errOut)
			}

			for _, want := range tc.contains {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr missing %q\nstderr:\n%s", want, errOut)
				}
			}

			var lines []string
			if out != "" {
				lines = strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			}

			if len(lines) != len(tc.wantStates) {
				t.Fatalf("printed %d bodies, want %d\nstdout:\n%s\nstderr:\n%s",
					len(lines), len(tc.wantStates), out, errOut)
			}

			for i, text := range lines {
				var body struct {
					State     json.RawMessage            `json:"state"`
					Questions map[string]json.RawMessage `json:"questions"`
				}

				if err := json.Unmarshal([]byte(text), &body); err != nil {
					t.Fatalf("body %d is not json: %v\n%s", i, err, text)
				}

				if string(body.State) != tc.wantStates[i] {
					t.Errorf("body %d state = %s, want %s", i, body.State, tc.wantStates[i])
				}

				if len(body.Questions) != len(tc.wantAsked) {
					t.Errorf("body %d asks %d questions, want %v", i, len(body.Questions), tc.wantAsked)
				}

				for _, id := range tc.wantAsked {
					if _, found := body.Questions[id]; !found {
						t.Errorf("body %d does not ask %q\n%s", i, id, text)
					}
				}
			}
		})
	}
}

func TestCalibrateRequests(t *testing.T) {
	t.Parallel()

	args := []string{
		"calibrate", "--print-request", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
		"--label", "urgent=.u",
	}

	const records = "{\"body\":\"a\",\"u\":true}\n{\"body\":\"b\",\"u\":false}\n"

	tests := []struct {
		name      string
		stdin     func(cancel context.CancelFunc) io.Reader
		stdout    func(cancel context.CancelFunc, out *bytes.Buffer) io.Writer
		wantLines int
	}{
		{
			name: "should exit 130 when cancelled as the input ends",
			stdin: func(cancel context.CancelFunc) io.Reader {
				return &cancelAtEnd{data: strings.NewReader(records), cancel: cancel}
			},
		},
		{
			name: "should exit 130 when cancelled while stdin is blocked",
			stdin: func(cancel context.CancelFunc) io.Reader {
				return &blockingReader{data: strings.NewReader(records), cancel: cancel, release: t.Context().Done()}
			},
		},
		{
			name: "should stop printing when cancelled between bodies",
			stdin: func(context.CancelFunc) io.Reader {
				return strings.NewReader(records)
			},
			stdout: func(cancel context.CancelFunc, out *bytes.Buffer) io.Writer {
				return &cancelOnWrite{out: out, cancel: cancel}
			},
			wantLines: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithKeychain(noKeychain()),
				WithStdin(tc.stdin(cancel)),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
			)

			if tc.stdout != nil {
				root.SetOut(tc.stdout(cancel, &out))
			} else {
				root.SetOut(&out)
			}

			root.SetErr(&errOut)
			root.SetArgs(args)

			done := make(chan int, 1)
			go func() { done <- Execute(ctx, root) }()

			var code int
			select {
			case code = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the command did not end after the interrupt")
			}

			if code != ExitInterrupt {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitInterrupt, out.String(),
					errOut.String())
			}

			if got := strings.Count(out.String(), "\n"); got != tc.wantLines {
				t.Errorf("printed %d bodies, want %d\nstdout:\n%s", got, tc.wantLines, out.String())
			}
		})
	}
}

type cancelAtEnd struct {
	data   io.Reader
	cancel context.CancelFunc
}

func (c *cancelAtEnd) Read(p []byte) (int, error) {
	n, err := c.data.Read(p)
	if err == io.EOF {
		c.cancel()
	}

	return n, err
}

type blockingReader struct {
	data    io.Reader
	cancel  context.CancelFunc
	release <-chan struct{}
	once    sync.Once
}

func (b *blockingReader) Read(p []byte) (int, error) {
	n, err := b.data.Read(p)
	if err != io.EOF {
		return n, err
	}

	b.once.Do(b.cancel)
	<-b.release

	return 0, io.EOF
}

type cancelOnWrite struct {
	out    *bytes.Buffer
	cancel context.CancelFunc
}

func (c *cancelOnWrite) Write(p []byte) (int, error) {
	c.cancel()

	return c.out.Write(p)
}
