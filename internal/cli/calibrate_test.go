package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
