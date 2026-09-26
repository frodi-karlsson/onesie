package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestNewCalibrateCmd(t *testing.T) {
	t.Parallel()

	const nothing = "onesie: no record carries a label, so there is nothing to calibrate"

	dir := t.TempDir()

	assertFile := writeCalibrateFile(t, dir, "assert.yaml",
		"assert: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n")
	thresholdFile := writeCalibrateFile(t, dir, "threshold.yaml",
		"urgent:\n  ask: is this urgent\n  threshold: 0.8\n")
	assertFlagFile := writeCalibrateFile(t, dir, "assert-flag.yaml", "urgent:\n  ask: is this urgent\n")
	confidenceFile := writeCalibrateFile(t, dir, "confidence.yaml",
		"team:\n  ask: which team\n  pick: [billing, shipping]\n  min_confidence: 0.8\n")
	fallbackFile := writeCalibrateFile(t, dir, "fallback.yaml",
		"urgent:\n  ask: is this urgent\n  fallback: no\n")
	abstainFile := writeCalibrateFile(t, dir, "abstain.yaml",
		"abstain_if: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n")
	replaceFile := writeCalibrateFile(t, dir, "replace.yaml", "urgent:\n  ask: is it urgent\n")
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
			name:     "should refuse to calibrate when no record carries a label",
			args:     base,
			wantCode: ExitUsage,
			contains: []string{nothing},
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
			contains: []string{nothing},
		},
		{
			name:     "should accept -i tsv",
			args:     []string{"calibrate", "--ask", "urgent=q", "-i", "tsv", "--map", ".body", "--label", "urgent=.u"},
			wantCode: ExitUsage,
			contains: []string{nothing},
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
			contains: []string{nothing},
		},
		{
			name:     "should accept -o json",
			args:     with("-o", "json"),
			wantCode: ExitUsage,
			contains: []string{nothing},
		},
		{
			name:     "should accept -o auto",
			args:     with("--output", "auto"),
			wantCode: ExitUsage,
			contains: []string{nothing},
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
			contains: []string{nothing},
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
			contains: []string{nothing},
		},
		{
			name: "should ignore an assert key in a question file",
			args: []string{
				"calibrate", "-f", assertFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{nothing},
		},
		{
			name: "should ignore a threshold in a question file",
			args: []string{
				"calibrate", "-f", thresholdFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{nothing},
		},
		{
			name: "should ignore a min_confidence in a question file",
			args: []string{
				"calibrate", "-f", confidenceFile, "-i", "jsonl", "--map", ".body", "--label", "team=.team",
			},
			wantCode: ExitUsage,
			contains: []string{nothing},
		},
		{
			name: "should ignore a fallback in a question file",
			args: []string{
				"calibrate", "-f", fallbackFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{nothing},
		},
		{
			name: "should ignore an abstain_if key in a question file",
			args: []string{
				"calibrate", "-f", abstainFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			wantCode: ExitUsage,
			contains: []string{nothing},
		},
		{
			name: "should refuse --assert beside a question file",
			args: []string{
				"calibrate", "-f", assertFlagFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
				"--assert", "urgent.value > 0.5",
			},
			wantCode: ExitUsage,
			contains: []string{"onesie: calibrate reports every cut and judges none, so --assert does not apply. Drop it"},
		},
		{
			name: "should refuse --threshold beside a gated question file",
			args: []string{
				"calibrate", "-f", assertFile, "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
				"--threshold", "0.5",
			},
			wantCode: ExitUsage,
			contains: []string{"onesie: calibrate reports every cut, so --threshold does not apply. Drop it"},
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
			contains: []string{nothing},
		},
		{
			name:     "should accept --usage with --out",
			args:     with("--usage", "--out", filepath.Join(dir, "usage.jsonl")),
			wantCode: ExitUsage,
			contains: []string{nothing},
		},
		{
			name:     "should say in its help that -i is required and hide the default",
			args:     []string{"calibrate", "--help"},
			wantCode: ExitOK,
			contains: []string{"required, jsonl, csv or tsv"},
		},
		{
			name:     "should say in its help what each shape accepts as a label, with a sample record",
			args:     []string{"calibrate", "--help"},
			wantCode: ExitOK,
			contains: []string{
				"Labels: a yes/no label is true, false, yes, no, 1 or 0 in any case",
				"null, no result or an empty string leaves a record unlabelled",
				`{"id":"T-1","body":"the site is down","is_urgent":true}`,
				"T-1,the site is down,yes",
				"3 a refused api key or an account out of credits",
				"the same questions, model, -i, --map and --id, with -o json and no gate or merge",
				"A question file's assert, abstain_if, threshold, min_confidence and fallback are ignored, " +
					"since calibrate reports every cut. The same flags are refused.",
			},
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
			name:     "should refuse --state, since each state comes from the input",
			args:     with("--state", "x"),
			wantCode: ExitUsage,
			contains: []string{
				"onesie: calibrate reads each state from its input through --map, so --state does not apply. Drop it",
			},
		},
		{
			name:     "should refuse --state-file, since each state comes from the input",
			args:     with("--state-file", "x.txt"),
			wantCode: ExitUsage,
			contains: []string{
				"onesie: calibrate reads each state from its input through --map, so --state-file does not apply. Drop it",
			},
		},
		{
			name:     "should let --ask replace a question from -f under --replace",
			args:     with("-f", replaceFile, "--replace"),
			wantCode: ExitUsage,
			contains: []string{nothing},
		},
		{
			name:     "should refuse a flag the root does not know either",
			args:     with("--bogus"),
			wantCode: ExitUsage,
			contains: []string{"unknown flag: --bogus"},
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
	t.Run("should give each labelled record its position among the input records", func(t *testing.T) {
		t.Parallel()

		mapper, err := exprOf(true, "--map", ".body")
		if err != nil {
			t.Fatal(err)
		}

		label, err := exprOf(true, "--label u", ".u")
		if err != nil {
			t.Fatal(err)
		}

		settings := rootSettings{
			stdin: strings.NewReader("{\"body\":\"a\",\"u\":true}\n{\"body\":\"b\"}\n\n{\"body\":\"c\",\"u\":1}\n"),
		}
		flags := &runFlags{skipBlank: true}
		labels := []questionLabel{{id: "u", shape: plan.Noul, expr: label}}

		set, err := readLabelled(t.Context(), settings, input.JSONL, flags, mapper, nil, labels)
		if err != nil {
			t.Fatal(err)
		}

		got := make([][2]int, 0, len(set.records))
		for _, rec := range set.records {
			got = append(got, [2]int{rec.position, rec.line})
		}

		want := [][2]int{{1, 1}, {3, 4}}
		if !slices.Equal(got, want) {
			t.Errorf("position and line = %v, want %v", got, want)
		}
	})
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

func TestCalibrateRun(t *testing.T) {
	t.Parallel()

	urgent := func(extra ...string) []string {
		return append([]string{
			"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
			"--label", "urgent=.u", "--id", ".id", "--cuts", "0.5,0.7",
		}, extra...)
	}

	const urgentSet = `{"id":"T-1","u":true,"body":{"urgent":0.9}}
{"id":"T-2","u":true,"body":{"urgent":0.8}}
{"id":"T-3","u":true,"body":{"urgent":0.3}}
{"id":"T-4","u":false,"body":{"urgent":0.1}}
{"id":"T-5","u":false,"body":{"urgent":0.6}}
{"id":"T-6","u":false,"body":{"urgent":0.2}}
`

	urgentTable := func(failed int) string {
		return strings.Join([]string{
			"urgent, yes/no: labelled 6, 3 yes, 3 no, " + strconv.Itoa(failed) + " failed. AUC 0.89",
			"flagged means urgent.value >= cut",
			"",
			"  cut   flagged  catches         false alarms   right when flagged",
			"  0.50        3  2/3 67% 21-94%  1/3 33% 6-79%  2/3  67% 21-94%",
			"  0.70        2  2/3 67% 21-94%  0/3  0% 0-56%  2/2 100% 34-100%",
			"",
			"worst misses",
			"  T-3  labelled yes  answered 0.30",
			"  T-5  labelled no   answered 0.60",
		}, "\n") + "\n"
	}

	tests := []struct {
		name         string
		args         []string
		stdin        string
		wantCode     int
		wantOut      string
		wantRequests int
		wantStates   []string
		check        func(t *testing.T, out, errOut string)
	}{
		{
			name:         "should print the yes/no table and exit 0",
			args:         urgent(),
			stdin:        urgentSet,
			wantCode:     ExitOK,
			wantOut:      urgentTable(0),
			wantRequests: 6,
		},
		{
			name:         "should print the report as json under -o json",
			args:         urgent("-o", "json"),
			stdin:        urgentSet,
			wantCode:     ExitOK,
			wantRequests: 6,
			check: func(t *testing.T, out, _ string) {
				t.Helper()

				if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
					t.Fatalf("stdout = %q, want one json line", out)
				}

				var report struct {
					Models     []string `json:"models"`
					Records    int      `json:"records"`
					Labelled   int      `json:"labelled"`
					Unlabelled int      `json:"unlabelled"`
					Asked      int      `json:"asked"`
					Stored     int      `json:"stored"`
					Failed     int      `json:"failed"`
					Questions  []struct {
						ID       string   `json:"id"`
						Shape    string   `json:"shape"`
						Labelled int      `json:"labelled"`
						AUC      *float64 `json:"auc"`
						Misses   []struct {
							ID   any `json:"id"`
							Line int `json:"line"`
						} `json:"misses"`
					} `json:"questions"`
				}

				if err := json.Unmarshal([]byte(out), &report); err != nil {
					t.Fatalf("stdout is not json: %v\n%s", err, out)
				}

				if !slices.Equal(report.Models, []string{"onesie-1.13.0"}) {
					t.Errorf("models = %v, want the stub's model", report.Models)
				}

				counts := []int{report.Records, report.Labelled, report.Unlabelled, report.Asked, report.Stored, report.Failed}
				if !slices.Equal(counts, []int{6, 6, 0, 6, 0, 0}) {
					t.Errorf("records, labelled, unlabelled, asked, stored, failed = %v, want [6 6 0 6 0 0]", counts)
				}

				if len(report.Questions) != 1 {
					t.Fatalf("questions = %d, want 1", len(report.Questions))
				}

				question := report.Questions[0]
				if question.ID != "urgent" || question.Shape != "yes/no" || question.Labelled != 6 {
					t.Errorf("question = %+v, want urgent, yes/no, labelled 6", question)
				}

				if question.AUC == nil || *question.AUC < 0.88 || *question.AUC > 0.89 {
					t.Errorf("auc = %v, want 8/9", question.AUC)
				}

				if len(question.Misses) != 2 || question.Misses[0].ID != "T-3" || question.Misses[0].Line != 0 {
					t.Errorf("misses = %+v, want T-3 then T-5 named by id", question.Misses)
				}
			},
		},
		{
			name: "should name a miss by its line in json without --id",
			args: []string{
				"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
				"--label", "urgent=.u", "-o", "json",
			},
			stdin:        "{\"u\":true,\"body\":{\"urgent\":0.9}}\n{\"u\":true,\"body\":{\"urgent\":0.1}}\n",
			wantCode:     ExitOK,
			wantRequests: 2,
			check: func(t *testing.T, out, _ string) {
				t.Helper()

				if !strings.Contains(out, `"misses":[{"line":2,`) {
					t.Errorf("stdout = %s, want the miss named by line 2 and no id", out)
				}
			},
		},
		{
			name: "should give two wordings with the same label a section each",
			args: []string{
				"calibrate", "--ask", "urgent=is this urgent", "--ask", "pressing=is this pressing", "-i", "jsonl",
				"--map", ".body", "--label", "urgent=.u", "--label", "pressing=.u", "--id", ".id", "--cuts", "0.5",
			},
			stdin: `{"id":"a","u":true,"body":{"urgent":0.9,"pressing":0.4}}
{"id":"b","u":false,"body":{"urgent":0.1,"pressing":0.2}}
`,
			wantCode:     ExitOK,
			wantRequests: 2,
			check: func(t *testing.T, out, _ string) {
				t.Helper()

				first := strings.Index(out, "urgent, yes/no: labelled 2, 1 yes, 1 no, 0 failed. AUC 1.00")
				second := strings.Index(out, "\n\npressing, yes/no: labelled 2, 1 yes, 1 no, 0 failed. AUC 1.00")

				if first != 0 || second < 0 {
					t.Errorf("stdout = %s, want an urgent section then a pressing section", out)
				}

				if !strings.Contains(out, "  a  labelled yes  answered 0.40") {
					t.Errorf("stdout = %s, want the pressing miss listed", out)
				}
			},
		},
		{
			name: "should ask a record one of two questions labels once, and count it only toward that question",
			args: []string{
				"calibrate", "--ask", "urgent=is this urgent", "--ask", "team=which team", "--pick",
				"billing,shipping", "-i", "jsonl", "--map", ".body", "--label", "urgent=.u", "--label", "team=.team",
				"--id", ".id", "--cuts", "0.5",
			},
			stdin: `{"id":"a","u":true,"team":"billing","body":{"urgent":0.9,"team":["billing",0.9]}}
{"id":"b","u":null,"team":"shipping","body":{"urgent":0.2,"team":["billing",0.8]}}
`,
			wantCode:     ExitOK,
			wantRequests: 2,
			wantStates: []string{
				`{"team":["billing",0.9],"urgent":0.9}`,
				`{"team":["billing",0.8],"urgent":0.2}`,
			},
			check: func(t *testing.T, out, _ string) {
				t.Helper()

				for _, want := range []string{
					"urgent, yes/no: labelled 1, 1 yes, 0 no, 0 failed.",
					"team, pick: labelled 2, 0 failed. agreement 50%",
					"  b  labelled shipping  picked billing  confidence 0.80",
				} {
					if !strings.Contains(out, want) {
						t.Errorf("stdout missing %q\n%s", want, out)
					}
				}
			},
		},
		{
			name: "should score a rate question against its levels",
			args: []string{
				"calibrate", "--ask", "stars=how good", "--rate", "low,mid,high", "-i", "jsonl", "--map", ".body",
				"--label", "stars=.s", "--id", ".id", "--cuts", "0.5",
			},
			stdin: `{"id":"a","s":"low","body":{"stars":[0,0.9]}}
{"id":"b","s":"high","body":{"stars":[0,0.6]}}
`,
			wantCode:     ExitOK,
			wantRequests: 2,
			check: func(t *testing.T, out, _ string) {
				t.Helper()

				for _, want := range []string{
					"stars, rate: labelled 2, 0 failed. agreement 50%",
					"mean distance 1.00 levels",
					"  b  labelled high  picked low  confidence 0.60",
				} {
					if !strings.Contains(out, want) {
						t.Errorf("stdout missing %q\n%s", want, out)
					}
				}
			},
		},
		{
			name: "should make no request for a record no question labels",
			args: urgent(),
			stdin: urgentSet + `{"id":"T-7","u":null,"body":{"urgent":0.5}}
`,
			wantCode:     ExitOK,
			wantOut:      urgentTable(0),
			wantRequests: 6,
			check: func(t *testing.T, _, errOut string) {
				t.Helper()

				if !strings.Contains(errOut, "asking 6 of 6 records, 1 question each, 1 unlabelled skipped\n") {
					t.Errorf("stderr = %q, want the cost line to count the unlabelled record", errOut)
				}
			},
		},
		{
			name: "should report a failed record, leave it out of every question and exit 6",
			args: urgent(),
			stdin: urgentSet + `{"id":"T-7","u":true,"body":{"status":500}}
`,
			wantCode:     ExitRecords,
			wantOut:      urgentTable(1),
			wantRequests: 7,
		},
		{
			name: "should count an answer outside [0,1] as a failed record",
			args: urgent(),
			stdin: urgentSet + `{"id":"T-7","u":true,"body":{"urgent":1.5}}
`,
			wantCode:     ExitRecords,
			wantOut:      urgentTable(1),
			wantRequests: 7,
		},
		{
			name:         "should stop on an authentication failure with exit 3 and no report",
			args:         urgent(),
			stdin:        `{"id":"T-0","u":true,"body":{"status":401}}` + "\n" + urgentSet,
			wantCode:     ExitAuth,
			wantRequests: 1,
		},
		{
			name:         "should refuse a set in which no record carries a label",
			args:         urgent(),
			stdin:        `{"id":"T-1","u":null,"body":{"urgent":0.5}}` + "\n",
			wantCode:     ExitUsage,
			wantRequests: 0,
			check: func(t *testing.T, _, errOut string) {
				t.Helper()

				if !strings.Contains(errOut, "onesie: no record carries a label, so there is nothing to calibrate") {
					t.Errorf("stderr = %q, want the empty set refused", errOut)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stub := newCalibrateStub(t)

			out, errOut, code := runCalibrateAgainst(t.Context(), t, tc.args, tc.stdin, stub.url, false)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, out, errOut)
			}

			if tc.wantCode != ExitOK && tc.wantCode != ExitRecords && out != "" {
				t.Errorf("stdout = %q, want no report", out)
			}

			if tc.wantOut != "" && out != tc.wantOut {
				t.Errorf("stdout =\n%s\nwant\n%s", out, tc.wantOut)
			}

			if got := stub.count(); got != tc.wantRequests {
				t.Errorf("requests = %d, want %d", got, tc.wantRequests)
			}

			if tc.wantStates != nil && !slices.Equal(stub.sent(), tc.wantStates) {
				t.Errorf("states sent = %v, want %v", stub.sent(), tc.wantStates)
			}

			if tc.check != nil {
				tc.check(t, out, errOut)
			}
		})
	}

	t.Run("should print the cost line to stderr before the first request", func(t *testing.T) {
		t.Parallel()

		var errOut syncBuffer

		stub := newCalibrateStub(t)
		stub.onRequest = func() {
			if !strings.Contains(errOut.String(), "asking 6 of 6 records, 1 question each\n") {
				t.Errorf("stderr at the first request = %q, want the cost line", errOut.String())
			}
		}

		var out bytes.Buffer

		code := executeCalibrate(t.Context(), t, urgent(), urgentSet, stub.url, &out, &errOut)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut.String())
		}

		if out.String() != urgentTable(0) {
			t.Errorf("stdout =\n%s\nwant only the report", out.String())
		}
	})

	t.Run("should print the --stats line to stderr after the report", func(t *testing.T) {
		t.Parallel()

		stub := newCalibrateStub(t)

		out, _, code := runCalibrateAgainst(t.Context(), t, urgent("--stats"), urgentSet, stub.url, true)
		if code != ExitOK {
			t.Fatalf("exit code = %d, output:\n%s", code, out)
		}

		report := strings.Index(out, "urgent, yes/no:")
		stats := strings.Index(out, "6 requests")

		if report < 0 || stats < report {
			t.Errorf("output =\n%s\nwant the report, then the stats line", out)
		}
	})

	t.Run("should count an answer outside [0,1] as failed in --stats as the report does", func(t *testing.T) {
		t.Parallel()

		stdin := urgentSet + `{"id":"T-7","u":true,"body":{"urgent":1.5}}` + "\n" +
			`{"id":"T-8","u":true,"body":{"status":500}}` + "\n"

		out, errOut, code := runCalibrateAgainst(t.Context(), t, urgent("--stats"), stdin, newCalibrateStub(t).url, false)
		if code != ExitRecords {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitRecords, errOut)
		}

		if !strings.HasPrefix(out, "urgent, yes/no: labelled 6, 3 yes, 3 no, 2 failed.") {
			t.Errorf("stdout =\n%s\nwant the report to count two failed records", out)
		}

		if !strings.Contains(errOut, "8 requests, 2 failed, ") {
			t.Errorf("stderr =\n%s\nwant the stats line to count the same two failed records", errOut)
		}
	})

	t.Run("should say why each record failed after the report and before the stats line", func(t *testing.T) {
		t.Parallel()

		stdin := urgentSet + `{"id":"T-7","u":true,"body":{"urgent":1.5}}` + "\n" +
			`{"id":"T-8","u":true,"body":{"urgent":"yes"}}` + "\n"

		stub := newCalibrateStub(t)
		stub.raw = true

		out, _, code := runCalibrateAgainst(t.Context(), t, urgent("--stats"), stdin, stub.url, true)
		if code != ExitRecords {
			t.Fatalf("exit code = %d, want %d\noutput:\n%s", code, ExitRecords, out)
		}

		report := strings.Index(out, "urgent, yes/no:")
		first := strings.Index(out, "\nonesie: record T-7: question 'urgent' answered 1.5, which lies outside [0,1]\n")
		second := strings.Index(out, "\nonesie: record T-8: ")
		stats := strings.Index(out, "8 requests, 2 failed, ")

		if report < 0 || first < report || second < first || stats < second {
			t.Errorf("output =\n%s\nwant the report, then one line per failed record, then the stats line", out)
		}
	})

	t.Run("should name a failed record by its line without --id and list at most five", func(t *testing.T) {
		t.Parallel()

		var stdin strings.Builder
		for range 7 {
			stdin.WriteString(`{"u":true,"body":{"status":500}}` + "\n")
		}

		args := []string{
			"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
			"--label", "urgent=.u",
		}

		_, errOut, code := runCalibrateAgainst(t.Context(), t, args, stdin.String(), newCalibrateStub(t).url, false)
		if code != ExitRecords {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitRecords, errOut)
		}

		lines := strings.Split(strings.TrimSuffix(errOut, "\n"), "\n")
		if len(lines) != 7 {
			t.Fatalf("stderr =\n%s\nwant the cost line, five failed records and the rest counted", errOut)
		}

		for i, line := range lines[1:6] {
			if want := fmt.Sprintf("onesie: line %d: ", i+1); !strings.HasPrefix(line, want) || !strings.Contains(line, "500") {
				t.Errorf("stderr line %d = %q, want it to start %q and give the status", i+2, line, want)
			}
		}

		if lines[6] != "onesie: and 2 more failed records" {
			t.Errorf("last stderr line = %q, want the rest counted", lines[6])
		}
	})

	t.Run("should give the same report under -j 4 as under -j 1", func(t *testing.T) {
		t.Parallel()

		var stdin strings.Builder
		for i := range 40 {
			fmt.Fprintf(&stdin, "{\"id\":\"T-%d\",\"u\":%t,\"body\":{\"urgent\":%.2f}}\n", i, i%3 == 0, float64(i%10)/10)
		}

		serial, _, serialCode := runCalibrateAgainst(t.Context(), t, urgent("-j", "1"), stdin.String(),
			newCalibrateStub(t).url, false)
		parallel, _, parallelCode := runCalibrateAgainst(t.Context(), t, urgent("-j", "4"), stdin.String(),
			newCalibrateStub(t).url, false)

		if serialCode != ExitOK || parallelCode != ExitOK {
			t.Fatalf("exit codes = %d and %d, want 0", serialCode, parallelCode)
		}

		if serial != parallel {
			t.Errorf("-j 1 printed\n%s\n-j 4 printed\n%s", serial, parallel)
		}
	})

	t.Run("should exit 130 with no report when cancelled during a blocked request", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		stub := newCalibrateStub(t)
		stub.onBlock = cancel

		done := make(chan struct{})

		var (
			out, errOut string
			code        int
		)

		go func() {
			defer close(done)

			out, errOut, code = runCalibrateAgainst(ctx, t, urgent("-j", "2"),
				urgentSet+`{"id":"T-7","u":true,"body":{"block":true}}`+"\n", stub.url, false)
		}()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the command did not end after the interrupt")
		}

		if code != ExitInterrupt {
			t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, ExitInterrupt, errOut)
		}

		if out != "" {
			t.Errorf("stdout = %q, want no report", out)
		}
	})

	calibrating := func(answers string, extra ...string) []string {
		return append([]string{
			"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
			"--label", "urgent=.u", "--id", ".id", "--cuts", "0.5,0.7", "--out", answers,
		}, extra...)
	}

	t.Run("should write one json line per asked record with the id first, and a fingerprint beside it", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")

		_, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), urgentSet, newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		lines := answerLines(t, answers)
		if len(lines) != 6 {
			t.Fatalf("answers file holds %d lines, want 6:\n%s", len(lines), strings.Join(lines, "\n"))
		}

		for i, line := range lines {
			if want := `{"id":"T-` + string(rune('1'+i)) + `",`; !strings.HasPrefix(line, want) {
				t.Errorf("line %d = %s, want it to start %s", i+1, line, want)
			}

			if !strings.Contains(line, `"urgent":{"value":`) {
				t.Errorf("line %d = %s, want the urgent answer", i+1, line)
			}
		}

		if _, err := os.Stat(answers + fingerprintSuffix); err != nil {
			t.Errorf("no fingerprint beside the answers file: %v", err)
		}

		if !strings.Contains(errOut, "asking 6 of 6 records, 1 question each, 0 answered in "+answers+"\n") {
			t.Errorf("stderr =\n%s\nwant the cost line to name the answers file", errOut)
		}
	})

	t.Run("should write lines with no id under --out without --id and without --resume", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		args := []string{
			"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
			"--label", "urgent=.u", "--out", answers,
		}

		_, errOut, code := runCalibrateAgainst(t.Context(), t, args, urgentSet, newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		lines := answerLines(t, answers)
		if len(lines) != 6 {
			t.Fatalf("answers file holds %d lines, want 6", len(lines))
		}

		for i, line := range lines {
			if strings.Contains(line, `"id"`) || !strings.HasPrefix(line, `{"model":`) {
				t.Errorf("line %d = %s, want no id", i+1, line)
			}
		}
	})

	t.Run("should ask nothing on a resume and print the same report byte for byte", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		first, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), urgentSet,
			newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		stub := newCalibrateStub(t)

		second, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), urgentSet, stub.url, false)
		if code != ExitOK {
			t.Fatalf("second run exit code = %d, stderr:\n%s", code, errOut)
		}

		if stub.count() != 0 {
			t.Errorf("the resume made %d requests, want none", stub.count())
		}

		if second != first {
			t.Errorf("the resume printed\n%s\nthe first run printed\n%s", second, first)
		}

		if !strings.Contains(errOut, "asking 0 of 6 records, 1 question each, 6 answered in "+answers+"\n") {
			t.Errorf("stderr =\n%s\nwant the cost line to count the stored answers", errOut)
		}
	})

	t.Run("should build no client and need no key when every record is already answered", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		first, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), urgentSet,
			newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		// runMocked passes no key and fails the test if a client is built.
		second, errOut, code := runMocked(t, t.Context(), calibrating(answers, "--resume"), urgentSet, nil, nil)
		if code != ExitOK || second != first {
			t.Errorf("exit %d, want 0 and the first run's report\nstdout:\n%s\nstderr:\n%s", code, second, errOut)
		}
	})

	answered := func(t *testing.T) (string, string) {
		t.Helper()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		report, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), urgentSet,
			newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("writing the answers file exit code = %d", code)
		}

		return answers, report
	}

	untouched := func(t *testing.T, paths ...string) func() {
		t.Helper()

		type state struct {
			data []byte
			mod  time.Time
		}

		before := map[string]state{}
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			before[path] = state{data: []byte(readText(t, path)), mod: info.ModTime()}
		}

		return func() {
			t.Helper()

			for path, was := range before {
				info, err := os.Stat(path)
				if err != nil || !info.ModTime().Equal(was.mod) || readText(t, path) != string(was.data) {
					t.Errorf("%s changed under --offline", path)
				}
			}
		}
	}

	t.Run("should print the same report from the answers file alone under --offline", func(t *testing.T) {
		t.Parallel()

		answers, first := answered(t)
		check := untouched(t, answers, answers+fingerprintSuffix)

		// runMocked passes no key and fails the test if a client is built.
		out, errOut, code := runMocked(t, t.Context(), calibrating(answers, "--resume", "--offline"), urgentSet, nil, nil)
		if code != ExitOK || out != first {
			t.Errorf("exit %d, want 0 and the first run's report\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}

		check()
	})

	t.Run("should refuse a record the answers file does not answer under --offline", func(t *testing.T) {
		t.Parallel()

		answers, _ := answered(t)
		lines := answerLines(t, answers)
		writeAnswerLines(t, answers, slices.DeleteFunc(lines, func(line string) bool {
			return strings.Contains(line, `"T-5"`) || strings.Contains(line, `"T-6"`)
		}))
		check := untouched(t, answers, answers+fingerprintSuffix)

		out, errOut, code := runMocked(t, t.Context(), calibrating(answers, "--resume", "--offline"), urgentSet, nil, nil)
		if code != ExitUsage || !strings.Contains(errOut, answers+" does not answer record T-5 or 1 more, so --offline "+
			"cannot report them. Rerun without --offline to ask them") || out != "" {
			t.Errorf("exit %d, want 2 naming record T-5\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}

		check()
	})

	t.Run("should refuse a stored error line under --offline, since calibrate asks it again", func(t *testing.T) {
		t.Parallel()

		answers, _ := answered(t)
		lines := answerLines(t, answers)
		for i, line := range lines {
			if strings.Contains(line, `"T-3"`) {
				lines[i] = `{"id":"T-3","error":{"kind":"http","status":500,"message":"boom"}}`
			}
		}

		writeAnswerLines(t, answers, lines)

		_, errOut, code := runMocked(t, t.Context(), calibrating(answers, "--resume", "--offline"), urgentSet, nil, nil)
		if code != ExitUsage || !strings.Contains(errOut, "does not answer record T-3") {
			t.Errorf("exit %d, want 2 naming record T-3\nstderr:\n%s", code, errOut)
		}
	})

	for _, stale := range []struct {
		name    string
		sidecar *string
		ask     string
		wording string
		cause   string
	}{
		{name: "no sidecar", wording: "has no fingerprint beside it", cause: "it has no fingerprint beside it"},
		{
			name: "a foreign sidecar", sidecar: new("hello"), wording: "does not hold a fingerprint onesie wrote",
			cause: "onesie did not write its fingerprint",
		},
		{name: "an older version", sidecar: new("v1:abc"), wording: "an older onesie wrote", cause: "an older onesie wrote its fingerprint"},
		{name: "a newer version", sidecar: new("v9:abc"), wording: "a newer onesie wrote", cause: "a newer onesie wrote its fingerprint"},
		{
			name: "a changed question", ask: "urgent=is this urgent now", wording: "changed since",
			cause: "the questions, model or flags changed since it was written",
		},
	} {
		t.Run("should advise regenerating a file with "+stale.name+" under --offline, and not without it", func(t *testing.T) {
			t.Parallel()

			answers, _ := answered(t)

			switch {
			case stale.sidecar != nil:
				if err := os.WriteFile(answers+fingerprintSuffix, []byte(*stale.sidecar+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case stale.ask == "":
				if err := os.Remove(answers + fingerprintSuffix); err != nil {
					t.Fatal(err)
				}
			}

			args := calibrating(answers, "--resume")
			if stale.ask != "" {
				args[2] = stale.ask
			}

			_, errOut, code := runMocked(t, t.Context(), append(slices.Clone(args), "--offline"), urgentSet, nil, nil)
			want := "onesie: " + answers + " does not match this run, since " + stale.cause +
				", so --offline cannot read it. Rerun without --offline and --resume to regenerate it"
			if code != ExitUsage || !strings.Contains(errOut, want) {
				t.Errorf("exit %d, want 2 with the regenerate advice\nstderr:\n%s", code, errOut)
			}

			_, errOut, code = runMocked(t, t.Context(), args, urgentSet, nil, nil)
			if code != ExitUsage || !strings.Contains(errOut, stale.wording) || !strings.Contains(errOut, "Drop --resume to start over") {
				t.Errorf("exit %d without --offline, want 2 with today's wording\nstderr:\n%s", code, errOut)
			}
		})
	}

	t.Run("should return an error matching ErrStaleAnswers for a stale file under --offline", func(t *testing.T) {
		t.Parallel()

		answers, _ := answered(t)
		if err := os.Remove(answers + fingerprintSuffix); err != nil {
			t.Fatal(err)
		}

		root := NewRootCmd(
			BuildInfo{Version: "1.2.3"},
			WithKeychain(noKeychain()),
			WithStdin(strings.NewReader(urgentSet)),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
		)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(calibrating(answers, "--resume", "--offline"))

		if err := root.ExecuteContext(t.Context()); !errors.Is(err, ErrStaleAnswers) {
			t.Errorf("error = %v, want one matching ErrStaleAnswers", err)
		}
	})

	t.Run("should refuse a missing answers file under --offline and create nothing", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		answers := filepath.Join(dir, "answers.jsonl")

		_, errOut, code := runMocked(t, t.Context(), calibrating(answers, "--resume", "--offline"), urgentSet, nil, nil)
		want := "onesie: " + answers + " does not exist, so --offline has nothing to read. " +
			"Rerun without --offline to create it"
		if code != ExitUsage || !strings.Contains(errOut, want) {
			t.Errorf("exit %d, want 2 naming the file\nstderr:\n%s", code, errOut)
		}

		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("the directory holds %v, want nothing", entries)
		}
	})

	for _, refused := range []struct {
		name, want string
		args       []string
	}{
		{name: "without --out", want: "--offline reads every answer from --out", args: urgent("--offline")},
		{name: "without --resume", want: "--offline needs --resume", args: urgent("--offline", "--out", "x.jsonl")},
		{name: "with --mock", want: "--offline reads every answer from --out, and --mock", args: urgent("--offline", "--out", "x.jsonl", "--resume", "--mock", "m.json")},
		{name: "with --print-request", want: "--offline reads answers, and --print-request", args: urgent("--offline", "--out", "x.jsonl", "--resume", "--print-request")},
		{name: "with --prune", want: "--offline never rewrites --out, so --prune has nothing to drop", args: urgent("--offline", "--out", "x.jsonl", "--resume", "--prune")},
	} {
		t.Run("should refuse --offline "+refused.name, func(t *testing.T) {
			t.Parallel()

			_, errOut, code := runMocked(t, t.Context(), refused.args, urgentSet, nil, nil)
			if code != ExitUsage || !strings.Contains(errOut, refused.want) {
				t.Errorf("exit %d, want 2 with %q\nstderr:\n%s", code, refused.want, errOut)
			}
		})
	}

	t.Run("should leave a part file an interrupted compaction left alone under --offline", func(t *testing.T) {
		t.Parallel()

		answers, _ := answered(t)
		part := answers + ".onesie.part"
		if err := os.WriteFile(part, []byte("partial\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		_, errOut, code := runMocked(t, t.Context(), calibrating(answers, "--resume", "--offline"), urgentSet, nil, nil)
		if code != ExitOK {
			t.Fatalf("exit %d\nstderr:\n%s", code, errOut)
		}

		if got := readText(t, part); got != "partial\n" {
			t.Errorf("part file = %q, want it left as it was", got)
		}
	})

	t.Run("should exit 1 under --offline when a requirement does not hold", func(t *testing.T) {
		t.Parallel()

		answers, _ := answered(t)

		_, errOut, code := runMocked(t, t.Context(),
			calibrating(answers, "--resume", "--offline", "--require", "urgent.catches >= 0.9 at 0.5"), urgentSet, nil, nil)
		if code != ExitRejected || !strings.Contains(errOut, "did not hold: 2/3") {
			t.Errorf("exit %d, want 1\nstderr:\n%s", code, errOut)
		}
	})

	t.Run("should ask nothing when a label is fixed, and show the change", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), urgentSet,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		fixed := strings.Replace(urgentSet, `"id":"T-3","u":true`, `"id":"T-3","u":false`, 1)
		stub := newCalibrateStub(t)

		out, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), fixed, stub.url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		if stub.count() != 0 {
			t.Errorf("the resume made %d requests, want none", stub.count())
		}

		if !strings.HasPrefix(out, "urgent, yes/no: labelled 6, 2 yes, 4 no, 0 failed.") {
			t.Errorf("stdout =\n%s\nwant the fixed label counted as no", out)
		}
	})

	t.Run("should ask only the record a new label reaches", func(t *testing.T) {
		t.Parallel()

		unlabelled := urgentSet + `{"id":"T-7","u":null,"body":{"urgent":0.7}}` + "\n"
		answers := filepath.Join(t.TempDir(), "answers.jsonl")

		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), unlabelled,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		labelled := strings.Replace(unlabelled, `"u":null`, `"u":true`, 1)
		stub := newCalibrateStub(t)

		out, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), labelled, stub.url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		if sent := stub.sent(); !slices.Equal(sent, []string{`{"urgent":0.7}`}) {
			t.Errorf("sent %v, want only the newly labelled record", sent)
		}

		if !strings.HasPrefix(out, "urgent, yes/no: labelled 7, 4 yes, 3 no, 0 failed.") {
			t.Errorf("stdout =\n%s\nwant the new record counted", out)
		}
	})

	t.Run("should ask a stored error line, an unusable line and a line missing a question again", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")

		fresh, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), urgentSet,
			newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		lines := answerLines(t, answers)
		lines[1] = `{"id":"T-2","error":{"kind":"http","status":500,"message":"stub"}}`
		lines[2] = `{"id":"T-3","model":"onesie-1.13.0","urgent":{"value":1.5}}`
		lines[3] = `{"id":"T-4","model":"onesie-1.13.0"}`
		writeAnswerLines(t, answers, lines)

		stub := newCalibrateStub(t)

		out, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "--stats"), urgentSet,
			stub.url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		sent := stub.sent()
		slices.Sort(sent)

		if want := []string{`{"urgent":0.1}`, `{"urgent":0.3}`, `{"urgent":0.8}`}; !slices.Equal(sent, want) {
			t.Errorf("sent %v, want %v", sent, want)
		}

		if out != fresh {
			t.Errorf("the resume printed\n%s\nthe first run printed\n%s", out, fresh)
		}

		if strings.Contains(errOut, "onesie: record") || !strings.Contains(errOut, "3 requests, 3 skipped, ") {
			t.Errorf("stderr =\n%s\nwant no failed record and the stored ones skipped", errOut)
		}

		compacted := answerLines(t, answers)
		if ids := lineIDs(t, compacted); !slices.Equal(ids, []string{"T-1", "T-2", "T-3", "T-4", "T-5", "T-6"}) {
			t.Errorf("answers file ids = %v, want one line per record in input order", ids)
		}

		for _, line := range compacted {
			if strings.Contains(line, `"error"`) || strings.Contains(line, "1.5") {
				t.Errorf("answers file still holds %s", line)
			}
		}
	})

	t.Run("should count a stored failure asked again only by its new outcome, and name only it", func(t *testing.T) {
		t.Parallel()

		failing := urgentSet + `{"id":"T-7","u":true,"body":{"status":500}}` + "\n"
		answers := filepath.Join(t.TempDir(), "answers.jsonl")

		_, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), failing,
			newCalibrateStub(t).url, false)
		if code != ExitRecords {
			t.Fatalf("first run exit code = %d, want %d", code, ExitRecords)
		}

		stub := newCalibrateStub(t)

		out, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "--stats", "-o", "json"),
			failing, stub.url, false)
		if code != ExitRecords {
			t.Fatalf("exit code = %d, want %d, stderr:\n%s", code, ExitRecords, errOut)
		}

		if stub.count() != 1 {
			t.Errorf("the resume made %d requests, want only the failed record", stub.count())
		}

		var report struct {
			Asked  int `json:"asked"`
			Stored int `json:"stored"`
			Failed int `json:"failed"`
		}

		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("stdout is not json: %v\n%s", err, out)
		}

		if report.Asked != 1 || report.Stored != 6 || report.Failed != 1 {
			t.Errorf("asked, stored, failed = %d, %d, %d, want 1, 6, 1", report.Asked, report.Stored, report.Failed)
		}

		causes := 0

		for line := range strings.SplitSeq(errOut, "\n") {
			if strings.HasPrefix(line, "onesie: record ") {
				causes++

				if !strings.HasPrefix(line, "onesie: record T-7: ") {
					t.Errorf("stderr names %q, want only T-7", line)
				}
			}
		}

		if causes != 1 || !strings.Contains(errOut, "1 request, 6 skipped, 1 failed, ") {
			t.Errorf("stderr =\n%s\nwant one failed record named and counted once", errOut)
		}
	})

	t.Run("should keep the lines an interrupt left, then ask only the rest and compact into input order", func(t *testing.T) {
		t.Parallel()

		blocking := strings.Replace(urgentSet, `{"id":"T-3","u":true,"body":{"urgent":0.3}}`,
			`{"id":"T-3","u":true,"body":{"block":true}}`, 1)
		answers := filepath.Join(t.TempDir(), "answers.jsonl")

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		stub := newCalibrateStub(t)
		stub.onBlock = cancel

		done := make(chan int)

		go func() {
			_, _, code := runCalibrateAgainst(ctx, t, calibrating(answers, "--resume", "-j", "1"), blocking, stub.url, false)
			done <- code
		}()

		select {
		case code := <-done:
			if code != ExitInterrupt {
				t.Fatalf("interrupted run exit code = %d, want %d", code, ExitInterrupt)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the command did not end after the interrupt")
		}

		if ids := lineIDs(t, answerLines(t, answers)); !slices.Equal(ids, []string{"T-1", "T-2"}) {
			t.Fatalf("answers file ids after the interrupt = %v, want T-1 and T-2", ids)
		}

		resumed := newCalibrateStub(t)

		_, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "-j", "4"), urgentSet,
			resumed.url, false)
		if code != ExitOK {
			t.Fatalf("resume exit code = %d, stderr:\n%s", code, errOut)
		}

		if resumed.count() != 4 {
			t.Errorf("the resume made %d requests, want 4", resumed.count())
		}

		if ids := lineIDs(t, answerLines(t, answers)); !slices.Equal(ids, []string{"T-1", "T-2", "T-3", "T-4", "T-5", "T-6"}) {
			t.Errorf("answers file ids = %v, want input order", ids)
		}
	})

	t.Run("should keep a stored id the input no longer has after the rest, and drop it under --prune", func(t *testing.T) {
		t.Parallel()

		later := `{"id":"T-0","u":true,"body":{"urgent":0.95}}` + "\n" +
			strings.Replace(urgentSet, `{"id":"T-6","u":false,"body":{"urgent":0.2}}`+"\n", "", 1)

		for _, tc := range []struct {
			prune bool
			want  []string
		}{
			{prune: false, want: []string{"T-0", "T-1", "T-2", "T-3", "T-4", "T-5", "T-6"}},
			{prune: true, want: []string{"T-0", "T-1", "T-2", "T-3", "T-4", "T-5"}},
		} {
			answers := filepath.Join(t.TempDir(), "answers.jsonl")
			if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), urgentSet,
				newCalibrateStub(t).url, false); code != ExitOK {
				t.Fatalf("first run exit code = %d", code)
			}

			args := calibrating(answers, "--resume")
			if tc.prune {
				args = append(args, "--prune")
			}

			stub := newCalibrateStub(t)
			if _, errOut, code := runCalibrateAgainst(t.Context(), t, args, later, stub.url, false); code != ExitOK {
				t.Fatalf("resume exit code = %d, stderr:\n%s", code, errOut)
			}

			if stub.count() != 1 {
				t.Errorf("prune %t: the resume made %d requests, want 1", tc.prune, stub.count())
			}

			if ids := lineIDs(t, answerLines(t, answers)); !slices.Equal(ids, tc.want) {
				t.Errorf("prune %t: answers file ids = %v, want %v", tc.prune, ids, tc.want)
			}
		}
	})

	t.Run("should let a plain stream run resume the file and ask only what calibrate did not", func(t *testing.T) {
		t.Parallel()

		mixed := urgentSet + `{"id":"T-7","u":null,"body":{"urgent":0.7}}` + "\n"
		answers := filepath.Join(t.TempDir(), "answers.jsonl")

		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), mixed,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("calibrate exit code = %d", code)
		}

		stub := newCalibrateStub(t)
		args := []string{
			"--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body", "--id", ".id",
			"-o", "json", "--out", answers, "--resume",
		}

		out, errOut, code := runCalibrateAgainst(t.Context(), t, args, mixed, stub.url, false)
		if code != ExitOK {
			t.Fatalf("stream exit code = %d, stderr:\n%s", code, errOut)
		}

		if sent := stub.sent(); !slices.Equal(sent, []string{`{"urgent":0.7}`}) {
			t.Errorf("the stream sent %v, want only the record calibrate left unasked", sent)
		}

		if out != "" {
			t.Errorf("stdout = %q, want the answers in the file", out)
		}

		if ids := lineIDs(t, answerLines(t, answers)); !slices.Equal(ids, []string{"T-1", "T-2", "T-3", "T-4", "T-5", "T-6", "T-7"}) {
			t.Errorf("answers file ids = %v, want every record in input order", ids)
		}
	})

	t.Run("should refuse a changed question or model and leave the file untouched", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), urgentSet,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		before, err := os.ReadFile(answers)
		if err != nil {
			t.Fatal(err)
		}

		changedQuestion := calibrating(answers, "--resume")
		changedQuestion[2] = "urgent=is this really urgent"

		for _, args := range [][]string{changedQuestion, calibrating(answers, "--resume", "-m", "another-model")} {
			stub := newCalibrateStub(t)

			out, errOut, code := runCalibrateAgainst(t.Context(), t, args, urgentSet, stub.url, false)
			if code != ExitUsage {
				t.Errorf("%v: exit code = %d, want %d", args, code, ExitUsage)
			}

			if !strings.Contains(errOut, "the questions, flags or gate changed") || out != "" || stub.count() != 0 {
				t.Errorf("%v: stdout %q, stderr %q, %d requests, want the changed run refused", args, out, errOut, stub.count())
			}

			after, err := os.ReadFile(answers)
			if err != nil {
				t.Fatal(err)
			}

			if string(after) != string(before) {
				t.Errorf("%v: the answers file changed", args)
			}
		}
	})

	t.Run("should ignore a question file's gates and policy and resume the flag run's answers", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		answers := filepath.Join(dir, "answers.jsonl")
		gated := writeCalibrateFile(t, dir, "gated.yaml",
			"assert: urgent.value > 0.5\nabstain_if: urgent.value > 0.95\n"+
				"urgent:\n  ask: is this urgent\n  threshold: 0.8\n  fallback: no\n")

		args := []string{
			"calibrate", "-f", gated, "-i", "jsonl", "--map", ".body",
			"--label", "urgent=.u", "--id", ".id", "--cuts", "0.5,0.7", "--out", answers,
		}

		out, errOut, code := runCalibrateAgainst(t.Context(), t, args, urgentSet, newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		if out != urgentTable(0) {
			t.Errorf("stdout =\n%s\nwant the flag run's report\n%s", out, urgentTable(0))
		}

		stub := newCalibrateStub(t)

		_, errOut, code = runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), urgentSet, stub.url, false)
		if code != ExitOK || stub.count() != 0 {
			t.Errorf("flag resume exit code = %d with %d requests, want 0 and none, stderr:\n%s",
				code, stub.count(), errOut)
		}
	})

	t.Run("should leave --cuts out of the fingerprint", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), urgentSet,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		stub := newCalibrateStub(t)

		out, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "--cuts", "0.9"), urgentSet,
			stub.url, false)
		if code != ExitOK || stub.count() != 0 {
			t.Fatalf("exit code = %d with %d requests, want 0 and none, stderr:\n%s", code, stub.count(), errOut)
		}

		if !strings.Contains(out, "  0.90 ") {
			t.Errorf("stdout =\n%s\nwant the new cut", out)
		}
	})

	t.Run("should count the stored records as skipped under --stats", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), urgentSet,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		_, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "--stats"), urgentSet,
			newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		if !strings.Contains(errOut, "0 requests, 6 skipped, ") {
			t.Errorf("stderr =\n%s\nwant the stored records counted as skipped", errOut)
		}
	})

	t.Run("should keep a stored answer for a record still in the input but unlabelled in input order", func(t *testing.T) {
		t.Parallel()

		stream := strings.Join(strings.Split(urgentSet, "\n")[:5], "\n") + "\n"
		unlabelled := strings.Replace(stream, `"id":"T-4","u":false`, `"id":"T-4","u":null`, 1)

		for _, prune := range []bool{false, true} {
			answers := filepath.Join(t.TempDir(), "answers.jsonl")
			streamArgs := []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body", "--id", ".id",
				"-o", "json", "--out", answers,
			}

			if _, errOut, code := runCalibrateAgainst(t.Context(), t, streamArgs, stream, newCalibrateStub(t).url,
				false); code != ExitOK {
				t.Fatalf("stream exit code = %d, stderr:\n%s", code, errOut)
			}

			args := calibrating(answers, "--resume")
			if prune {
				args = append(args, "--prune")
			}

			stub := newCalibrateStub(t)

			out, errOut, code := runCalibrateAgainst(t.Context(), t, args, unlabelled, stub.url, false)
			if code != ExitOK || stub.count() != 0 {
				t.Fatalf("prune %t: exit code = %d with %d requests, want 0 and none, stderr:\n%s",
					prune, code, stub.count(), errOut)
			}

			if !strings.HasPrefix(out, "urgent, yes/no: labelled 4, 3 yes, 1 no, 0 failed.") {
				t.Errorf("prune %t: stdout =\n%s\nwant the unlabelled record left out of the report", prune, out)
			}

			if ids := lineIDs(t, answerLines(t, answers)); !slices.Equal(ids, []string{"T-1", "T-2", "T-3", "T-4", "T-5"}) {
				t.Errorf("prune %t: answers file ids = %v, want every input record in input order", prune, ids)
			}
		}
	})

	t.Run("should say how many records the usage covers when stored lines carry none and an answer is unusable", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), urgentSet,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		stub := newCalibrateStub(t)
		stub.usage = true

		more := urgentSet + `{"id":"T-7","u":true,"body":{"urgent":0.7}}` + "\n" +
			`{"id":"T-8","u":true,"body":{"urgent":1.5}}` + "\n" +
			`{"id":"T-9","u":true,"body":{"missing":true}}` + "\n"

		out, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "--usage", "-o", "json"),
			more, stub.url, false)
		if code != ExitRecords {
			t.Fatalf("exit code = %d, want %d, stderr:\n%s", code, ExitRecords, errOut)
		}

		if stub.count() != 3 {
			t.Errorf("the resume made %d requests, want 3", stub.count())
		}

		if !strings.Contains(out, `"usage":{"input_tokens":30,"output_tokens":6,"cost":0.75,"records":3}`) {
			t.Errorf("stdout = %s, want the three answers asked counted, both unusable ones included", out)
		}
	})

	t.Run("should put usage on each line and a summed usage in the json report, stored lines included", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		stub := newCalibrateStub(t)
		stub.usage = true

		without := strings.Replace(urgentSet, `{"id":"T-6","u":false,"body":{"urgent":0.2}}`+"\n", "", 1)

		first, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--usage", "-o", "json"),
			without, stub.url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		for i, line := range answerLines(t, answers) {
			if !strings.Contains(line, `"usage":{"input_tokens":10,"output_tokens":2,"cost":0.25}`) {
				t.Errorf("line %d = %s, want its usage", i+1, line)
			}
		}

		if !strings.Contains(first, `"usage":{"input_tokens":50,"output_tokens":10,"cost":1.25,"records":5}`) {
			t.Errorf("stdout = %s, want the usage of five records summed", first)
		}

		again := newCalibrateStub(t)
		again.usage = true

		second, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--usage", "-o", "json", "--resume"),
			urgentSet, again.url, false)
		if code != ExitOK {
			t.Fatalf("resume exit code = %d, stderr:\n%s", code, errOut)
		}

		if again.count() != 1 || !strings.Contains(second, `"usage":{"input_tokens":60,"output_tokens":12,"cost":1.5,"records":6}`) {
			t.Errorf("stdout = %s after %d requests, want one request and six records summed", second, again.count())
		}

		plain, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "-o", "json", "--resume"),
			urgentSet, newCalibrateStub(t).url, false)
		if code != ExitOK || strings.Contains(plain, `"usage"`) {
			t.Errorf("exit code %d, stdout = %s, want no usage without --usage", code, plain)
		}
	})

	t.Run("should answer a repeated calibrate --cache from the cache and print the same report", func(t *testing.T) {
		t.Parallel()

		stub := newCalibrateStub(t)
		env := cacheEnv(t)

		first, errOut, code := calibrateWithEnv(t, env, urgent("--cache"), urgentSet, stub.url)
		if code != ExitOK {
			t.Fatalf("first run exit %d\n%s", code, errOut)
		}

		second, errOut, code := calibrateWithEnv(t, env, urgent("--cache"), urgentSet, stub.url)
		if code != ExitOK || second != first {
			t.Fatalf("second run exit %d, stdout %q, want %q\n%s", code, second, first, errOut)
		}

		if stub.count() != 6 {
			t.Errorf("%d requests, want 6, all from the first run", stub.count())
		}
	})

	t.Run("should exit 2 before any request for a cache dir others can read", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("windows carries no unix permission bits, so the mode check does not apply")
		}

		stub := newCalibrateStub(t)
		env := cacheEnv(t)
		mustMkdirMode(t, env["ONESIE_CACHE_DIR"], 0o755)

		out, errOut, code := calibrateWithEnv(t, env, urgent("--cache", "-j", "4"), urgentSet, stub.url)
		if code != ExitUsage || out != "" || !strings.Contains(errOut, "chmod 700") {
			t.Errorf("exit %d, stdout %q, stderr %q, want exit 2 with the chmod advice and no report", code, out, errOut)
		}

		if stub.count() != 0 {
			t.Errorf("%d requests, want none", stub.count())
		}
	})

	t.Run("should leave the cache dir absent under calibrate --offline and ONESIE_CACHE=1", func(t *testing.T) {
		t.Parallel()

		stub := newCalibrateStub(t)
		env := cacheEnv(t)
		answers := filepath.Join(t.TempDir(), "answers.jsonl")

		if _, errOut, code := calibrateWithEnv(t, env, urgent("--out", answers), urgentSet, stub.url); code != ExitOK {
			t.Fatalf("first run exit %d\n%s", code, errOut)
		}

		env[envCache] = "1"

		_, errOut, code := calibrateWithEnv(t, env, urgent("--out", answers, "--resume", "--offline"), urgentSet, stub.url)
		if code != ExitOK {
			t.Fatalf("offline run exit %d\n%s", code, errOut)
		}

		if _, err := os.Stat(env["ONESIE_CACHE_DIR"]); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the cache dir exists: %v", err)
		}
	})
}

func TestReportOf(t *testing.T) {
	t.Parallel()

	yes := &calibrate.Label{Yes: true}
	billing := &calibrate.Label{Name: "billing"}

	built := &plan.Plan{Questions: []plan.Question{
		{ID: "urgent", Shape: plan.Noul},
		{ID: "team", Shape: plan.Pick, Options: []plan.Option{{Name: "billing"}, {Name: "shipping"}}},
	}}

	confidence := func(value float64) *float64 {
		return &value
	}

	answeredWith := func(value any, picked any, conf *float64) output.Record {
		return output.Record{Model: "m", Answers: []output.Named{
			{ID: "urgent", Answer: &answer.Answer{Value: value}},
			{ID: "team", Answer: &answer.Answer{Value: picked, Confidence: conf}},
		}}
	}

	tests := []struct {
		name       string
		record     output.Record
		wantFailed int
	}{
		{name: "should score a value and a confidence inside [0,1]", record: answeredWith(0.5, "billing", confidence(0.5))},
		{name: "should fail a yes/no value that is NaN", record: answeredWith(math.NaN(), "billing", confidence(0.5)), wantFailed: 1},
		{name: "should fail a yes/no value above 1", record: answeredWith(1.01, "billing", confidence(0.5)), wantFailed: 1},
		{name: "should fail a yes/no value below 0", record: answeredWith(-0.01, "billing", confidence(0.5)), wantFailed: 1},
		{name: "should fail a yes/no value that is not a number", record: answeredWith("yes", "billing", confidence(0.5)), wantFailed: 1},
		{name: "should fail a confidence that is NaN", record: answeredWith(0.5, "billing", confidence(math.NaN())), wantFailed: 1},
		{name: "should fail a confidence above 1", record: answeredWith(0.5, "billing", confidence(2)), wantFailed: 1},
		{name: "should fail a missing confidence", record: answeredWith(0.5, "billing", nil), wantFailed: 1},
		{name: "should fail a pick that is not a string", record: answeredWith(0.5, 3, confidence(0.5)), wantFailed: 1},
		{
			name:       "should fail a record whose request failed",
			record:     failureRecord(built, errors.New("boom")),
			wantFailed: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			set := labelledSet{
				records: []labelledRecord{{line: 1, id: "T-1", labels: []*calibrate.Label{yes, billing}}},
				total:   1,
			}

			report := reportOf(built, set, []output.Record{tc.record}, cutsFor([]float64{0.5}, nil, len(built.Questions)))

			if report.Failed != tc.wantFailed {
				t.Errorf("report failed = %d, want %d", report.Failed, tc.wantFailed)
			}

			urgent, team := report.Questions[0].YesNo, report.Questions[1].Pick
			if urgent.Failed != tc.wantFailed || team.Failed != tc.wantFailed {
				t.Errorf("question failed = %d and %d, want %d each", urgent.Failed, team.Failed, tc.wantFailed)
			}

			if urgent.Labelled != 1-tc.wantFailed || team.Labelled != 1-tc.wantFailed {
				t.Errorf("question labelled = %d and %d, want %d each", urgent.Labelled, team.Labelled,
					1-tc.wantFailed)
			}

			if tc.wantFailed == 0 {
				if miss := urgent.Cuts[0]; miss.Flagged != 1 {
					t.Errorf("flagged at 0.5 = %d, want the value 0.5 flagged", miss.Flagged)
				}

				if !slices.Equal(report.Models, []string{"m"}) {
					t.Errorf("models = %v, want [m]", report.Models)
				}
			}
		})
	}

	t.Run("should name a case by its id text with --id and leave the id nil without one", func(t *testing.T) {
		t.Parallel()

		noul := &plan.Plan{Questions: []plan.Question{{ID: "urgent", Shape: plan.Noul}}}
		no := &calibrate.Label{Yes: false}
		missed := output.Record{Model: "m", Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.9}}}}

		set := labelledSet{records: []labelledRecord{
			{line: 1, id: json.Number("7"), labels: []*calibrate.Label{no}},
			{line: 2, labels: []*calibrate.Label{no}},
		}, total: 2}

		misses := reportOf(noul, set, []output.Record{missed, missed}, cutsFor([]float64{0.5}, nil, len(noul.Questions))).Questions[0].YesNo.Misses

		if len(misses) != 2 {
			t.Fatalf("misses = %+v, want two", misses)
		}

		if misses[0].Name != "7" || misses[0].ID != json.Number("7") || misses[0].Line != 1 {
			t.Errorf("first miss = %+v, want name 7, id 7, line 1", misses[0])
		}

		if misses[1].Name != "" || misses[1].ID != nil || misses[1].Line != 2 {
			t.Errorf("second miss = %+v, want no name, a nil id and line 2", misses[1])
		}
	})
}

func runCalibrateAgainst(
	ctx context.Context, t *testing.T, args []string, stdin, baseURL string, shared bool,
) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	stderr := io.Writer(&errOut)
	if shared {
		stderr = &out
	}

	code := executeCalibrate(ctx, t, args, stdin, baseURL, &out, stderr)

	return out.String(), errOut.String(), code
}

func executeCalibrate(
	ctx context.Context, t *testing.T, args []string, stdin, baseURL string, stdout, stderr io.Writer,
) int {
	t.Helper()

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithClientFactory(stubFactory(baseURL)),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
	)

	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)

	return Execute(ctx, root)
}

func newCalibrateStub(t *testing.T) *calibrateStub {
	t.Helper()

	stub := &calibrateStub{t: t}
	srv := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(srv.Close)
	stub.url = srv.URL

	return stub
}

func (s *calibrateStub) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State     json.RawMessage `json:"state"`
		Questions map[string]struct {
			Type string `json:"type"`
		} `json:"questions"`
	}

	var fields map[string]json.RawMessage

	err := json.NewDecoder(r.Body).Decode(&body)
	if err == nil {
		err = json.Unmarshal(body.State, &fields)
	}

	if err != nil {
		s.t.Errorf("stub could not read the request: %v", err)
		w.WriteHeader(http.StatusBadRequest)

		return
	}

	if s.counted(string(body.State)) == 1 && s.onRequest != nil {
		s.onRequest()
	}

	if status, found := fields["status"]; found {
		code, _ := strconv.Atoi(string(status))
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":{"message":"stub"}}`))

		return
	}

	if _, found := fields["missing"]; found {
		response := map[string]any{"model": "onesie-1.13.0", "answers": map[string]any{}}
		if s.usage {
			response["usage"] = map[string]any{"input_tokens": 10, "output_tokens": 2, "cost": 0.25}
		}

		encoded, encodeErr := json.Marshal(response)
		if encodeErr == nil {
			_, encodeErr = w.Write(encoded)
		}

		if encodeErr != nil {
			s.t.Errorf("writing stub response: %v", encodeErr)
		}

		return
	}

	if _, found := fields["block"]; found {
		if s.onBlock != nil {
			s.onBlock()
		}

		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}

		return
	}

	answers := map[string]any{}

	for id, question := range body.Questions {
		answers[id] = stubAnswer(s.t, question.Type, fields[id], s.raw)
	}

	response := map[string]any{"model": "onesie-1.13.0", "answers": answers}
	if s.usage {
		response["usage"] = map[string]any{"input_tokens": 10, "output_tokens": 2, "cost": 0.25}
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		s.t.Errorf("stub could not encode its answer: %v", err)
	}

	if _, err := w.Write(encoded); err != nil {
		s.t.Errorf("writing stub response: %v", err)
	}
}

func (s *calibrateStub) counted(state string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests++
	s.states = append(s.states, state)

	return s.requests
}

func stubAnswer(t *testing.T, kind string, raw json.RawMessage, verbatim bool) map[string]any {
	t.Helper()

	if kind == "noul" && verbatim {
		return map[string]any{"type": "noul", "noul": raw}
	}

	if kind == "noul" {
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Errorf("stub state %s is not a yes/no value", raw)
		}

		return map[string]any{"type": "noul", "noul": value}
	}

	var pair []any
	if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
		t.Errorf("stub state %s is not a pair", raw)

		return nil
	}

	if kind == "choice" {
		return map[string]any{
			"type": "choice", "choice": pair[0], "confidence": pair[1],
			"probabilities": map[string]any{fmt.Sprint(pair[0]): pair[1]},
		}
	}

	return map[string]any{
		"type": "score", "score": pair[0], "confidence": pair[1],
		"probabilities": map[string]any{fmt.Sprint(pair[0]): pair[1]},
	}
}

func (s *calibrateStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.requests
}

func (s *calibrateStub) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.states)
}

type calibrateStub struct {
	t         *testing.T
	url       string
	onRequest func()
	onBlock   func()
	raw       bool
	usage     bool

	mu       sync.Mutex
	requests int
	states   []string
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func answerLines(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the answers file: %v", err)
	}

	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}

	return strings.Split(text, "\n")
}

func writeAnswerLines(t *testing.T, path string, lines []string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("writing the answers file: %v", err)
	}
}

func lineIDs(t *testing.T, lines []string) []string {
	t.Helper()

	ids := make([]string, 0, len(lines))

	for _, line := range lines {
		var record struct {
			ID string `json:"id"`
		}

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("answers line %s is not json: %v", line, err)
		}

		ids = append(ids, record.ID)
	}

	return ids
}

const (
	requireStdin = `{"id":"a","body":"x","u":true,"t":"ops"}
{"id":"b","body":"x","u":true,"t":"ops"}
{"id":"c","body":"x","u":true,"t":"dev"}
{"id":"d","body":"x","u":true,"t":"dev"}
{"id":"e","body":"x","u":false,"t":"ops"}
{"id":"f","body":"x","u":false,"t":"ops"}
{"id":"g","body":"x","u":false,"t":"dev"}
{"id":"h","body":"x","u":false,"t":"dev"}
`
	requireMock = `{"id":"a","u":0.9,"t":"ops"}
{"id":"b","u":0.8,"t":"ops"}
{"id":"c","u":0.6,"t":"dev"}
{"id":"d","u":0.2,"t":"ops"}
{"id":"e","u":0.7,"t":"ops"}
{"id":"f","u":0.3,"t":"ops"}
{"id":"g","u":0.1,"t":"dev"}
{"id":"h","u":0.05,"t":"dev"}
`
	requireQuestions = "u:\n  ask: is it urgent\nt:\n  ask: which team\n  pick: [ops, dev]\n"
)

func TestCalibrateAnswers(t *testing.T) {
	t.Parallel()

	gated := writeCalibrateFile(t, t.TempDir(), "gated.yaml",
		"assert: u.value < 0.5\nabstain_if: u.value < 0.8\n"+requireQuestions)
	mockFile := writeMock(t, requireMock)
	failingMock := writeMock(t, strings.Replace(requireMock, `{"id":"h","u":0.05,"t":"dev"}`, `{"id":"h","error":500}`, 1))

	base := func(mock string, extra ...string) []string {
		return append([]string{
			"calibrate", "-f", gated, "-i", "jsonl", "--map", ".body", "--id", ".id",
			"--label", "u=.u", "--label", "t=.t", "--cuts", "0.5", "--mock", mock,
		}, extra...)
	}

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		stdout     []string
		stderr     []string
		noStderr   []string
		wantReport []requireJSON
		wantCuts   [][]float64
	}{
		{
			name:     "should print the report and exit 0 when every requirement holds",
			args:     base(mockFile, "--require", "u.catches >= 0.75", "--require", "t.agreement >= 0.75"),
			wantCode: ExitOK,
			stdout:   []string{"u, yes/no: labelled 8", "t, pick: labelled 8"},
			noStderr: []string{"did not hold"},
		},
		{
			name:     "should name a failing requirement on stderr and exit 1",
			args:     base(mockFile, "--require", "u.catches >= 0.9", "--require", "u.auc >= 0.5"),
			wantCode: ExitRejected,
			stdout:   []string{"u, yes/no: labelled 8"},
			stderr:   []string{"onesie: 'u.catches >= 0.9' did not hold: 3/4 75% 30-95% at cut 0.50\n"},
			noStderr: []string{"u.auc"},
		},
		{
			name:     "should exit 6 when a record failed beside a failing requirement",
			args:     base(failingMock, "--require", "u.catches >= 0.9"),
			wantCode: ExitRecords,
			stderr:   []string{"'u.catches >= 0.9' did not hold", "onesie: record h: "},
		},
		{
			name:     "should add a cut the requirement names to that question only",
			args:     base(mockFile, "-o", "json", "--require", "u.catches >= 0.5 at 0.33"),
			wantCode: ExitOK,
			wantCuts: [][]float64{{0.33, 0.5}, {0.5}},
			wantReport: []requireJSON{
				{Expr: "u.catches >= 0.5 at 0.33", Held: true, Value: ptr(0.75), Interval: true, Cut: ptr(0.33)},
			},
		},
		{
			name:     "should read the cut of at abstain from abstain_if",
			args:     base(mockFile, "-o", "json", "--require", "u.catches >= 0.5 at abstain"),
			wantCode: ExitOK,
			wantReport: []requireJSON{
				{Expr: "u.catches >= 0.5 at abstain", Held: true, Value: ptr(0.5), Interval: true, Cut: ptr(0.8)},
			},
		},
		{
			name:     "should read the cut from the file's assert without at",
			args:     base(mockFile, "-o", "json", "--require", "lower(u.catches) >= 0.5"),
			wantCode: ExitRejected,
			wantReport: []requireJSON{
				{Expr: "lower(u.catches) >= 0.5", Value: ptr(0.75), Interval: true, Cut: ptr(0.5)},
			},
		},
		{
			name: "should write null for what a measure lacks, and a reason for no records",
			args: base(mockFile, "-o", "json", "--require", "u.auc >= 0.5", "--require", "t.agreement >= 0.5",
				"--require", "u.right_when_flagged >= 0.5 at 0.95"),
			wantCode: ExitRejected,
			wantReport: []requireJSON{
				{Expr: "u.auc >= 0.5", Held: true, Value: ptr(0.8125)},
				{Expr: "t.agreement >= 0.5", Held: true, Value: ptr(0.875), Interval: true},
				{Expr: "u.right_when_flagged >= 0.5 at 0.95", Cut: ptr(0.95), Reason: "no record is flagged"},
			},
		},
		{
			name:     "should leave the require key out and never exit 1 without --require",
			args:     base(mockFile, "-o", "json"),
			wantCode: ExitOK,
		},
		{
			name:     "should check a requirement and print the bodies under --print-request",
			args:     []string{"calibrate", "-f", gated, "-i", "jsonl", "--map", ".body", "--label", "u=.u", "--label", "t=.t", "--print-request", "--require", "u.catches >= 0.9"},
			wantCode: ExitOK,
			stdout:   []string{`"state":"x"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runMocked(t, t.Context(), tc.args, requireStdin, nil, nil)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, out, errOut)
			}

			checkMocked(t, out, errOut, code, tc.wantCode, nil, tc.stdout, nil, tc.stderr)

			for _, unwanted := range tc.noStderr {
				if strings.Contains(errOut, unwanted) {
					t.Errorf("stderr holds %q\n%s", unwanted, errOut)
				}
			}

			if !slices.Contains(tc.args, "json") {
				return
			}

			var report struct {
				Require   []map[string]any `json:"require"`
				Questions []struct {
					Cuts []struct {
						Cut float64 `json:"cut"`
					} `json:"cuts"`
				} `json:"questions"`
			}

			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("decoding the report: %v\n%s", err, out)
			}

			if tc.wantReport == nil && strings.Contains(out, `"require"`) {
				t.Errorf("report holds a require key\n%s", out)
			}

			checkRequireJSON(t, report.Require, tc.wantReport)

			for q, want := range tc.wantCuts {
				got := make([]float64, 0, len(report.Questions[q].Cuts))
				for _, row := range report.Questions[q].Cuts {
					got = append(got, row.Cut)
				}

				if !slices.Equal(got, want) {
					t.Errorf("question %d cuts = %v, want %v", q, got, want)
				}
			}
		})
	}
}

type requireJSON struct {
	Expr     string
	Held     bool
	Value    *float64
	Interval bool
	Cut      *float64
	Reason   string
}

func checkRequireJSON(t *testing.T, got []map[string]any, want []requireJSON) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("require = %v, want %d entries", got, len(want))
	}

	for i, w := range want {
		entry := got[i]
		if entry["expr"] != w.Expr || entry["held"] != w.Held {
			t.Errorf("require[%d] = %v, want expr %q and held %v", i, entry, w.Expr, w.Held)
		}

		for _, key := range []string{"value", "interval", "cut"} {
			if _, found := entry[key]; !found {
				t.Errorf("require[%d] = %v, has no %s key", i, entry, key)
			}
		}

		if value, _ := entry["value"].(float64); (w.Value == nil) != (entry["value"] == nil) || w.Value != nil && value != *w.Value {
			t.Errorf("require[%d] value = %v, want %v", i, entry["value"], w.Value)
		}

		interval, _ := entry["interval"].([]any)
		if w.Interval != (len(interval) == 2) || !w.Interval && entry["interval"] != nil {
			t.Errorf("require[%d] interval = %v, want present %v", i, entry["interval"], w.Interval)
		}

		if cut, _ := entry["cut"].(float64); (w.Cut == nil) != (entry["cut"] == nil) || w.Cut != nil && cut != *w.Cut {
			t.Errorf("require[%d] cut = %v, want %v", i, entry["cut"], w.Cut)
		}

		if reason, _ := entry["reason"].(string); reason != w.Reason {
			t.Errorf("require[%d] reason = %q, want %q", i, reason, w.Reason)
		}
	}
}

func ptr(value float64) *float64 {
	return &value
}

func TestResolveRequirements(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := func(name, gate string) string {
		return writeCalibrateFile(t, dir, name, gate+requireQuestions)
	}

	tests := []struct {
		name    string
		file    string
		require string
		extra   []string
		wantErr string
	}{
		{name: "should refuse a gate cut outside 0 to 1", file: file("wide.yaml", "assert: u.value < 1.5\n"), require: "u.catches >= 0.5", wantErr: "compares u.value with 1.5, and a cut lies between 0 and 1"},
		{name: "should refuse a bad requirement under --print-request", file: file("print.yaml", ""), require: "u.catches >= 95", extra: []string{"--print-request"}, wantErr: "write 0.95, not 95"},
		{name: "should refuse a requirement with no cut under --print-request", file: file("print2.yaml", "assert: u.value > 0.5\n"), require: "u.catches >= 0.5", extra: []string{"--print-request"}, wantErr: "with >"},
		{name: "should refuse a gate that reads the value through max", file: file("max.yaml", "assert: max(u.value, u.value) < 0.5\n"), require: "u.catches >= 0.5", wantErr: "max()"},
		{name: "should refuse a gate that compares with greater", file: file("gt.yaml", "assert: u.value > 0.5\n"), require: "u.catches >= 0.5", wantErr: "with >"},
		{name: "should refuse a gate that tests with in", file: file("in.yaml", `assert: t.value in ["ops"] and u.value in [0.5]`+"\n"), require: "u.catches >= 0.5", wantErr: "with in"},
		{name: "should refuse a cut with no file", require: "u.catches >= 0.5", wantErr: "no gate"},
		{name: "should refuse at abstain with no abstain_if", file: file("assert.yaml", "assert: u.value < 0.5\n"), require: "u.catches >= 0.5 at abstain", wantErr: "abstain_if"},
		{name: "should refuse an unknown id", file: file("plain.yaml", ""), require: "x.catches >= 0.5 at 0.5", wantErr: "unknown question 'x'"},
		{name: "should refuse agreement on a yes/no question", file: file("plain2.yaml", ""), require: "u.agreement >= 0.5", wantErr: "'u' is a yes/no question"},
		{name: "should refuse catches on a pick question", file: file("plain3.yaml", ""), require: "t.catches >= 0.5 at 0.5", wantErr: "'t' is a pick question"},
		{name: "should refuse within_one on a pick question", file: file("plain4.yaml", ""), require: "t.within_one >= 0.5", wantErr: "'t' is a pick question"},
		{name: "should refuse a requirement that does not parse", file: file("plain5.yaml", ""), require: "u.catches >= 95", wantErr: "write 0.95, not 95"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := []string{"calibrate", "-i", "jsonl", "--map", ".body", "--id", ".id", "--require", tc.require}
			if tc.file != "" {
				args = append(args, "-f", tc.file, "--label", "u=.u", "--label", "t=.t")
			} else {
				args = append(args, "--ask", "u=is it urgent", "--label", "u=.u")
			}

			args = append(args, tc.extra...)

			// No --mock, and runMocked fails the test if a client is built, so any request would show.
			out, errOut, code := runMocked(t, t.Context(), args, requireStdin, nil, nil)
			if code != ExitUsage || !strings.Contains(errOut, tc.wantErr) || !strings.Contains(errOut, "'"+tc.require+"'") {
				t.Errorf("exit %d, want %d with %q naming the requirement\nstdout:\n%s\nstderr:\n%s",
					code, ExitUsage, tc.wantErr, out, errOut)
			}

			if out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}
		})
	}
}

func TestCutsFor(t *testing.T) {
	t.Parallel()

	bound := func(texts ...string) []boundRequirement {
		reqs, err := parseRequirements(texts)
		if err != nil {
			t.Fatal(err)
		}

		out := make([]boundRequirement, 0, len(reqs))
		for _, req := range reqs {
			out = append(out, boundRequirement{Requirement: req, question: 0, cut: req.At.Value, hasCut: true})
		}

		return out
	}

	tests := []struct {
		name  string
		base  []float64
		bound []boundRequirement
		want  [][]float64
	}{
		{
			name:  "should hold a cut once when 0.50, 0.5 and --cuts 0.5 meet",
			base:  []float64{0.5},
			bound: bound("u.catches >= 0.5 at 0.50", "u.false_alarms <= 0.5 at 0.5"),
			want:  [][]float64{{0.5}, {0.5}},
		},
		{
			name:  "should add a new cut in order to its own question only",
			base:  []float64{0.25, 0.75},
			bound: bound("u.catches >= 0.5 at 0.5", "u.catches >= 0.5 at 0.5"),
			want:  [][]float64{{0.25, 0.5, 0.75}, {0.25, 0.75}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := cutsFor(tc.base, tc.bound, 2)
			if !slices.EqualFunc(got, tc.want, slices.Equal[[]float64]) {
				t.Errorf("cutsFor = %v, want %v", got, tc.want)
			}
		})
	}
}

func calibrateWithEnv(t *testing.T, env map[string]string, args []string, stdin, baseURL string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithClientFactory(stubFactory(baseURL)),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(maps.Clone(env))),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}
