package cli

import (
	"encoding/json"
	"fmt"
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

	const labelledAngry = `{"id":"T-1","u":true,"angry":true,"body":"a"}
{"id":"T-2","u":true,"angry":false,"body":"b"}
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

	t.Run("should sum usage over the compared records, with no cost unless every one carries it", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a, b := answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)

		withUsage := func(path string, usage func(i int) string, drop int) {
			lines := answerLines(t, path)
			kept := lines[:0]

			for i, line := range lines {
				if i == drop {
					continue
				}

				kept = append(kept, strings.Replace(line, `"model":"jev-latest",`, `"model":"jev-latest","usage":`+usage(i)+",", 1))
			}

			writeAnswerLines(t, path, kept)
		}

		// T-6 is left out of b, so a's usage for it is not counted either.
		withUsage(a, func(i int) string {
			return fmt.Sprintf(`{"input_tokens":%d,"output_tokens":4,"cost":0.00005}`, 400+10*i)
		}, -1)
		withUsage(b, func(i int) string {
			if i == 0 {
				return `{"input_tokens":300,"output_tokens":4}`
			}

			return `{"input_tokens":300,"output_tokens":4,"cost":0.00002}`
		}, 5)

		out, errOut, code := runMocked(t, t.Context(), comparing("--compare", a, "--compare", b), labelled, nil, nil)
		want := "\nusage, mean per record over the 5 records any question compares\n\n" +
			"  " + padTo("run", a) + "  records  input tokens  output tokens  cost per 1000\n" +
			"  " + a + "        5           420              4          $0.05\n" +
			"  " + b + "        5           300              4              -\n"
		if code != ExitOK || !strings.HasSuffix(out, want) {
			t.Errorf("exit %d\nstdout:\n%s\nwant it to end\n%s\nstderr:\n%s", code, out, want, errOut)
		}

		jsonOut, _, _ := runMocked(t, t.Context(), comparing("--compare", a, "--compare", b, "-o", "json"), labelled, nil, nil)

		var report struct {
			Runs []struct {
				Usage map[string]float64 `json:"usage"`
			} `json:"runs"`
		}

		if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
			t.Fatalf("stdout is not json: %v\n%s", err, jsonOut)
		}

		first, second := report.Runs[0].Usage, report.Runs[1].Usage
		if first["records"] != 5 || first["input_tokens"] != 2100 || first["cost"] != 0.00025 {
			t.Errorf("baseline usage = %v, want 5 records, 2100 input tokens and a cost of 0.00025", first)
		}

		if _, costed := second["cost"]; costed || second["records"] != 5 {
			t.Errorf("second usage = %v, want 5 records and no cost", second)
		}
	})

	t.Run("should say when a file carries usage on only some compared records", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a, b := answered(t, dir, "a.jsonl", march), answered(t, dir, "b.jsonl", november)

		lines := answerLines(t, a)
		for i := range 3 {
			lines[i] = strings.Replace(lines[i], `"model":"jev-latest",`, `"model":"jev-latest","usage":{"input_tokens":10,"output_tokens":1},`, 1)
		}

		writeAnswerLines(t, a, lines)

		out, errOut, code := runMocked(t, t.Context(), comparing("--compare", a, "--compare", b), labelled, nil, nil)
		if want := "onesie: " + a + " carries usage on 3 of the 6 compared records, so its usage covers only those\n"; code != ExitOK ||
			!strings.Contains(errOut, want) || strings.Contains(errOut, b+" carries usage") {
			t.Errorf("exit %d, want 0 with %q and nothing on %s\nstdout:\n%s\nstderr:\n%s", code, want, b, out, errOut)
		}
	})

	t.Run("should cover the records any question compares, more than one question alone", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		twoQuestions := func(name, answers string) string {
			path := filepath.Join(dir, name)
			args := []string{
				"calibrate", "--ask", "urgent=is this urgent", "--ask", "angry=is this angry", "-i", "jsonl",
				"--map", ".body", "--label", "urgent=.u", "--label", "angry=.angry", "--id", ".id", "--out", path,
				"--mock", writeMock(t, answers),
			}

			if _, errOut, code := runMocked(t, t.Context(), args, labelledAngry, nil, nil); code != ExitOK {
				t.Fatalf("writing %s exit %d\nstderr:\n%s", name, code, errOut)
			}

			lines := answerLines(t, path)
			for i := range lines {
				lines[i] = strings.Replace(lines[i], `"model":"jev-latest",`, `"model":"jev-latest","usage":{"input_tokens":10,"output_tokens":1},`, 1)
			}

			writeAnswerLines(t, path, lines)

			return path
		}

		a, b := twoQuestions("a.jsonl", `{"urgent":0.9,"angry":0.2}`), twoQuestions("b.jsonl", `{"urgent":0.8,"angry":0.3}`)
		args := []string{
			"calibrate", "--ask", "urgent=is this urgent", "--ask", "angry=is this angry", "-i", "jsonl",
			"--map", ".body", "--label", "urgent=.u", "--label", "angry=.angry", "--id", ".id",
			"--compare", a, "--compare", b,
		}

		out, errOut, code := runMocked(t, t.Context(), args, labelledAngry, nil, nil)
		if code != ExitOK || !strings.Contains(out, "angry, yes/no: compared 2 of 2 labelled") ||
			!strings.Contains(out, "usage, mean per record over the 6 records any question compares") ||
			strings.Contains(errOut, "carries usage on") {
			t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
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

	t.Run("should compare a pick and a rate question, and check a requirement on each file", func(t *testing.T) {
		t.Parallel()

		const choices = `{"id":"T-1","team":"billing","tone":"3","body":"a"}
{"id":"T-2","team":"support","tone":"1","body":"b"}
{"id":"T-3","team":"billing","tone":"5","body":"c"}
{"id":"T-4","team":"support","tone":"2","body":"d"}
`

		choosing := []string{
			"calibrate", "--ask", "team=which team", "--pick", "billing,support", "--ask", "tone=how angry",
			"--rate", "1,2,3,4,5", "-i", "jsonl", "--map", ".body", "--label", "team=.team", "--label", "tone=.tone",
			"--id", ".id",
		}
		answer := func(id, team, tone string) string {
			return fmt.Sprintf(`{"id":%q,"team":{"value":%q,"confidence":0.9},"tone":{"value":%q,"confidence":0.8}}`, id, team, tone)
		}

		dir := t.TempDir()
		write := func(name string, answers ...string) string {
			path := filepath.Join(dir, name)
			args := append(append([]string{}, choosing...), "--out", path, "--mock", writeMock(t, strings.Join(answers, "\n")+"\n"))

			if _, errOut, code := runMocked(t, t.Context(), args, choices, nil, nil); code != ExitOK {
				t.Fatalf("writing %s exit %d\nstderr:\n%s", name, code, errOut)
			}

			return path
		}

		a := write("a.jsonl", answer("T-1", "billing", "3"), answer("T-2", "support", "1"), answer("T-3", "billing", "5"),
			answer("T-4", "support", "2"))
		b := write("b.jsonl", answer("T-1", "billing", "4"), answer("T-2", "billing", "1"), answer("T-3", "billing", "2"),
			answer("T-4", "support", "2"))

		args := append(append([]string{}, choosing...), "--compare", a, "--compare", b, "--require", "team.agreement >= 0.9")
		out, errOut, code := runMocked(t, t.Context(), args, choices, nil, nil)

		want := "team, pick: compared 4 of 4 labelled\n\n" +
			"  " + padTo("run", a) + "  model       agreement       change from baseline\n" +
			"  " + a + "  jev-latest  4/4 99% 51-99%  baseline\n" +
			"  " + b + "  jev-latest  3/4 75% 30-95%  -0.25  -0.70 to 0.28\n" +
			"\ntone, rate: compared 4 of 4 labelled\n\n" +
			"  " + padTo("run", a) + "  model       agreement       agreement change      within one level  within one change\n" +
			"  " + a + "  jev-latest  4/4 99% 51-99%  baseline              4/4 99% 51-99%    baseline\n" +
			"  " + b + "  jev-latest  2/4 50% 15-85%  -0.50  -0.85 to 0.10  3/4 75% 30-95%    -0.25  -0.70 to 0.28\n"
		if code != ExitFail || out != want {
			t.Errorf("exit %d, want 1\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", code, out, want, errOut)
		}

		if unmet := "onesie: " + b + ": 'team.agreement >= 0.9' did not hold: 3/4"; !strings.Contains(errOut, unmet) ||
			strings.Contains(errOut, a+": ") {
			t.Errorf("stderr lacks %q or names %s\nstderr:\n%s", unmet, a, errOut)
		}

		jsonOut, _, _ := runMocked(t, t.Context(), append(args, "-o", "json"), choices, nil, nil)

		var report struct {
			Questions []struct {
				Shape string `json:"shape"`
				Runs  []struct {
					AgreementChange *map[string]float64 `json:"agreement_change"`
					WithinOne       *map[string]any     `json:"within_one"`
				} `json:"runs"`
			} `json:"questions"`
		}

		if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
			t.Fatalf("stdout is not json: %v\n%s", err, jsonOut)
		}

		team, tone := report.Questions[0], report.Questions[1]
		if team.Shape != "pick" || team.Runs[0].AgreementChange != nil || (*team.Runs[1].AgreementChange)["value"] != -0.25 ||
			team.Runs[1].WithinOne != nil || tone.Shape != "rate" || tone.Runs[1].WithinOne == nil {
			t.Errorf("report = %+v", report)
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
