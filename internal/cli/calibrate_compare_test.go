package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareCalibration(t *testing.T) {
	t.Parallel()

	const labelled = `{"id":"T-1","u":true,"body":"a"}
{"id":"T-2","u":true,"body":"b"}
{"id":"T-3","u":true,"body":"c"}
{"id":"T-4","u":false,"body":"d"}
{"id":"T-5","u":false,"body":"e"}
{"id":"T-6","u":false,"body":"f"}
`

	const (
		march = `{"id":"T-1","urgent":0.9}
{"id":"T-2","urgent":0.8}
{"id":"T-3","urgent":0.7}
{"id":"T-4","urgent":0.1}
{"id":"T-5","urgent":0.2}
{"id":"T-6","urgent":0.3}
`
		november = `{"id":"T-1","urgent":0.9}
{"id":"T-2","urgent":0.4}
{"id":"T-3","urgent":0.7}
{"id":"T-4","urgent":0.1}
{"id":"T-5","urgent":0.6}
{"id":"T-6","urgent":0.3}
`
	)

	asking := []string{"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body", "--label", "urgent=.u", "--id", ".id"}

	// answered writes an answers file the way a real run does, fingerprint included, answered from mock.
	answered := func(t *testing.T, dir, name, mock string) string {
		t.Helper()

		path := filepath.Join(dir, name)
		args := append(append([]string{}, asking...), "--out", path, "--mock", writeMock(t, mock))

		if _, errOut, code := runMocked(t, t.Context(), args, labelled, nil, nil); code != ExitOK {
			t.Fatalf("writing %s exit %d\nstderr:\n%s", name, code, errOut)
		}

		return path
	}

	comparing := func(extra ...string) []string {
		return append(append([]string{}, asking...), extra...)
	}

	t.Run("should print a row per file under the notices, asking nothing", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a, b := answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)

		// runMocked passes no key and fails the test if a client is built.
		out, errOut, code := runMocked(t, t.Context(),
			comparing("--compare", a, "--compare", b, "--false-alarms", "0,0.5"), labelled, nil, nil)

		want := "urgent, yes/no: compared 6 of 6 labelled, 3 yes, 3 no\n\n" +
			"  " + padTo("run", a) + "  model       AUC              change from baseline  catches at 0% false alarms  catches at 50% false alarms\n" +
			"  " + a + "  jev-latest  1.00  0.50-1.00  baseline              3/3 99% 44-99% at 0.70      3/3 99% 44-99% at 0.30\n" +
			"  " + b + "  jev-latest  0.89  0.40-0.99  -0.11  -0.60 to 0.40  2/3 67% 21-94% at 0.70      3/3 99% 44-99% at 0.40\n"
		if code != ExitOK || out != want {
			t.Errorf("exit %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", code, out, want, errOut)
		}

		if wantNotice := "onesie: comparing 2 answers files over 6 labelled records, " + a + " being the baseline\n"; errOut != wantNotice {
			t.Errorf("stderr = %q, want %q", errOut, wantNotice)
		}
	})

	t.Run("should write the comparison as one json object under -o json", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a, b := answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)

		out, errOut, code := runMocked(t, t.Context(), comparing("--compare", a, "--compare", b, "-o", "json"), labelled, nil, nil)
		if code != ExitOK || strings.Count(out, "\n") != 1 {
			t.Fatalf("exit %d, want 0 and one line\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}

		var report struct {
			Baseline  string `json:"baseline"`
			Questions []struct {
				Compared int `json:"compared"`
				Runs     []struct {
					File      string              `json:"file"`
					AUC       map[string]float64  `json:"auc"`
					AUCChange *map[string]float64 `json:"auc_change"`
				} `json:"runs"`
			} `json:"questions"`
		}

		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("stdout is not json: %v\n%s", err, out)
		}

		runs := report.Questions[0].Runs
		if report.Baseline != a || report.Questions[0].Compared != 6 || len(runs) != 2 || runs[0].AUCChange != nil ||
			runs[1].AUCChange == nil || (*runs[1].AUCChange)["value"] != -0.1111 || runs[1].AUC["low"] != 0.401 {
			t.Errorf("report = %+v", report)
		}
	})

	t.Run("should name the models and the unanswered records of a file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a, b := answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)

		lines := answerLines(t, b)
		lines[0] = strings.Replace(lines[0], "jev-latest", "jev-1.15.0", 1)
		lines[1] = `{"id":"T-2","error":{"status":503,"message":"unavailable"}}`
		lines = append(lines[:2], lines[3:]...)
		writeAnswerLines(t, b, append(lines, `{"id":"T-9","model":"jev-latest","urgent":{"value":0.5}}`))

		out, errOut, code := runMocked(t, t.Context(), comparing("--compare", a, "--compare", b), labelled, nil, nil)
		for _, want := range []string{
			"onesie: " + b + " was answered by jev-1.15.0 and jev-latest and " + a + " by jev-latest\n",
			"onesie: " + b + " was answered by 2 models, jev-1.15.0 on 1 record and jev-latest on 3 records, so it spans a model update\n",
			"onesie: " + b + " holds 1 answer for no labelled record of the input, left out\n",
			"onesie: left out of 'urgent': 2 unanswered in " + b + "\n",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("stderr lacks %q\nstderr:\n%s", want, errOut)
			}
		}

		if code != ExitOK || !strings.Contains(out, "compared 4 of 6 labelled") {
			t.Errorf("exit %d\nstdout:\n%s", code, out)
		}
	})

	t.Run("should name the parts of the run that differ from the baseline", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a := answered(t, dir, "a.jsonl", march)
		b := filepath.Join(dir, "b.jsonl")

		args := append(append([]string{}, asking...), "--out", b, "--mock", writeMock(t, november), "--long-context")
		if _, errOut, code := runMocked(t, t.Context(), args, labelled, nil, nil); code != ExitOK {
			t.Fatalf("writing b.jsonl exit %d\nstderr:\n%s", code, errOut)
		}

		_, errOut, code := runMocked(t, t.Context(), comparing("--compare", a, "--compare", b), labelled, nil, nil)
		if want := "onesie: " + b + " differs from " + a + " in --long-context\n"; code != ExitOK || !strings.Contains(errOut, want) {
			t.Errorf("exit %d, want 0 with %q\nstderr:\n%s", code, want, errOut)
		}
	})

	t.Run("should say a file without a fingerprint cannot be told apart", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a, b := answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)
		if err := os.Remove(b + ".onesie"); err != nil {
			t.Fatal(err)
		}

		_, errOut, code := runMocked(t, t.Context(), comparing("--compare", a, "--compare", b), labelled, nil, nil)
		if want := b + " has no fingerprint beside it, so onesie cannot say how it differs from " + a; code != ExitOK || !strings.Contains(errOut, want) {
			t.Errorf("exit %d, want 0 with %q\nstderr:\n%s", code, want, errOut)
		}
	})

	t.Run("should exit 1 and name the file when a requirement does not hold on it", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a, b := answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)

		_, errOut, code := runMocked(t, t.Context(),
			comparing("--compare", a, "--compare", b, "--require", "urgent.catches >= 0.9 at 0.5"), labelled, nil, nil)
		if want := "onesie: " + b + ": 'urgent.catches >= 0.9 at 0.5' did not hold: 2/3"; code != ExitFail ||
			!strings.Contains(errOut, want) || strings.Contains(errOut, a+": ") {
			t.Errorf("exit %d, want 1 with %q and nothing on %s\nstderr:\n%s", code, want, a, errOut)
		}
	})

	t.Run("should skip a pick question with a notice and compare the rest", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a, b := answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)
		args := comparing("--ask", "team=which team", "--pick", "billing,support", "--label", "team=\"billing\"",
			"--compare", a, "--compare", b)

		out, errOut, code := runMocked(t, t.Context(), args, labelled, nil, nil)
		if want := "skipping 'team', a pick question, since --compare compares only yes or no questions so far"; code != ExitOK ||
			!strings.Contains(errOut, want) || !strings.HasPrefix(out, "urgent, yes/no") {
			t.Errorf("exit %d, want 0 with %q\nstdout:\n%s\nstderr:\n%s", code, want, out, errOut)
		}
	})

	refusals := []struct {
		name  string
		files func(t *testing.T, dir string) []string
		extra []string
		want  string
	}{
		{
			name: "should refuse a file that does not exist",
			files: func(t *testing.T, dir string) []string {
				return []string{answered(t, dir, "a.jsonl", march), filepath.Join(dir, "gone.jsonl")}
			},
			want: "gone.jsonl does not exist, so --compare has nothing to read from it",
		},
		{
			name: "should refuse a file that answers the question for no labelled record",
			files: func(t *testing.T, dir string) []string {
				empty := filepath.Join(dir, "empty.jsonl")
				if err := os.WriteFile(empty, []byte(`{"id":"X-1","urgent":{"value":0.5}}`+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}

				return []string{answered(t, dir, "a.jsonl", march), empty}
			},
			want: "empty.jsonl holds no answer to question 'urgent' for a labelled record",
		},
		{
			name: "should name an answer that cannot be scored when no answer in a file can",
			files: func(t *testing.T, dir string) []string {
				b := answered(t, dir, "b.jsonl", november)
				lines := answerLines(t, b)
				for i := range lines {
					lines[i] = strings.Replace(lines[i], `"value":`, `"value":"high","was":`, 1)
				}

				writeAnswerLines(t, b, lines)

				return []string{answered(t, dir, "a.jsonl", march), b}
			},
			want: "b.jsonl answers question 'urgent' in no way that can be scored, as for record T-1: " +
				"question 'urgent' answered high, which lies outside [0,1]",
		},
		{
			name: "should refuse a requirement on a question it skips",
			files: func(t *testing.T, dir string) []string {
				return []string{answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)}
			},
			extra: []string{"--ask", "team=which team", "--pick", "billing,support", "--label", "team=\"billing\"", "--require", "team.agreement >= 0.5"},
			want:  "--require 'team.agreement >= 0.5' reads 'team', a pick question, and --compare compares only yes or no questions so far",
		},
		{
			name:  "should refuse a setting that asks, through the settings rules",
			files: func(t *testing.T, dir string) []string { return []string{"a.jsonl", "b.jsonl"} },
			extra: []string{"--out", "c.jsonl"},
			want:  "--compare reads saved answers and asks nothing, so --out does not apply. Drop it",
		},
	}

	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := comparing(tc.extra...)
			for _, file := range tc.files(t, t.TempDir()) {
				args = append(args, "--compare", file)
			}

			out, errOut, code := runMocked(t, t.Context(), args, labelled, nil, nil)
			if code != ExitUsage || !strings.Contains(errOut, tc.want) || out != "" || strings.Contains(errOut, "was answered by") {
				t.Errorf("exit %d, want 2 with %q and no report\nstdout:\n%s\nstderr:\n%s", code, tc.want, out, errOut)
			}
		})
	}
}

func padTo(text, like string) string {
	return text + strings.Repeat(" ", max(0, len(like)-len(text)))
}
