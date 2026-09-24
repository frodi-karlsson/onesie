package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const resumeSet = `{"id":"T-1","u":true,"body":{"urgent":0.9}}
{"id":"T-2","u":true,"body":{"urgent":0.8}}
{"id":"T-3","u":true,"body":{"urgent":0.3}}
{"id":"T-4","u":false,"body":{"urgent":0.1}}
{"id":"T-5","u":false,"body":{"urgent":0.6}}
{"id":"T-6","u":false,"body":{"urgent":0.2}}
`

func TestResumeLabelled(t *testing.T) {
	t.Parallel()

	calibrating := func(answers string, extra ...string) []string {
		return append([]string{
			"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body",
			"--label", "urgent=.u", "--id", ".id", "--cuts", "0.5,0.7", "--out", answers,
		}, extra...)
	}

	t.Run("should write one json line per asked record with the id first, and a fingerprint beside it", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")

		_, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), resumeSet, newCalibrateStub(t).url, false)
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

		_, errOut, code := runCalibrateAgainst(t.Context(), t, args, resumeSet, newCalibrateStub(t).url, false)
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
		first, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), resumeSet,
			newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		stub := newCalibrateStub(t)

		second, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), resumeSet, stub.url, false)
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

	t.Run("should ask nothing when a label is fixed, and show the change", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), resumeSet,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		fixed := strings.Replace(resumeSet, `"id":"T-3","u":true`, `"id":"T-3","u":false`, 1)
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

		unlabelled := resumeSet + `{"id":"T-7","u":null,"body":{"urgent":0.7}}` + "\n"
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

		fresh, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume"), resumeSet,
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

		out, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "--stats"), resumeSet,
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

		failing := resumeSet + `{"id":"T-7","u":true,"body":{"status":500}}` + "\n"
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

		blocking := strings.Replace(resumeSet, `{"id":"T-3","u":true,"body":{"urgent":0.3}}`,
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

		_, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "-j", "4"), resumeSet,
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
			strings.Replace(resumeSet, `{"id":"T-6","u":false,"body":{"urgent":0.2}}`+"\n", "", 1)

		for _, tc := range []struct {
			prune bool
			want  []string
		}{
			{prune: false, want: []string{"T-0", "T-1", "T-2", "T-3", "T-4", "T-5", "T-6"}},
			{prune: true, want: []string{"T-0", "T-1", "T-2", "T-3", "T-4", "T-5"}},
		} {
			answers := filepath.Join(t.TempDir(), "answers.jsonl")
			if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), resumeSet,
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

		mixed := resumeSet + `{"id":"T-7","u":null,"body":{"urgent":0.7}}` + "\n"
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
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), resumeSet,
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

			out, errOut, code := runCalibrateAgainst(t.Context(), t, args, resumeSet, stub.url, false)
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

	t.Run("should leave --cuts out of the fingerprint", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), resumeSet,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		stub := newCalibrateStub(t)

		out, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "--cuts", "0.9"), resumeSet,
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
		if _, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers), resumeSet,
			newCalibrateStub(t).url, false); code != ExitOK {
			t.Fatalf("first run exit code = %d", code)
		}

		_, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--resume", "--stats"), resumeSet,
			newCalibrateStub(t).url, false)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		if !strings.Contains(errOut, "0 requests, 6 skipped, ") {
			t.Errorf("stderr =\n%s\nwant the stored records counted as skipped", errOut)
		}
	})

	t.Run("should put usage on each line and a summed usage in the json report, stored lines included", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		stub := newCalibrateStub(t)
		stub.usage = true

		without := strings.Replace(resumeSet, `{"id":"T-6","u":false,"body":{"urgent":0.2}}`+"\n", "", 1)

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

		if !strings.Contains(first, `"usage":{"input_tokens":50,"output_tokens":10,"cost":1.25}`) {
			t.Errorf("stdout = %s, want the usage of five records summed", first)
		}

		again := newCalibrateStub(t)
		again.usage = true

		second, errOut, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "--usage", "-o", "json", "--resume"),
			resumeSet, again.url, false)
		if code != ExitOK {
			t.Fatalf("resume exit code = %d, stderr:\n%s", code, errOut)
		}

		if again.count() != 1 || !strings.Contains(second, `"usage":{"input_tokens":60,"output_tokens":12,"cost":1.5}`) {
			t.Errorf("stdout = %s after %d requests, want one request and six records summed", second, again.count())
		}

		plain, _, code := runCalibrateAgainst(t.Context(), t, calibrating(answers, "-o", "json", "--resume"),
			resumeSet, newCalibrateStub(t).url, false)
		if code != ExitOK || strings.Contains(plain, `"usage"`) {
			t.Errorf("exit code %d, stdout = %s, want no usage without --usage", code, plain)
		}
	})
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
