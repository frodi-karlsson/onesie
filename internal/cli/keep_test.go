package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestKeeper(t *testing.T) {
	t.Parallel()

	const (
		passFailError = "{\"answer\":0.9}\n{\"answer\":0.2}\n{\"error\":503}\n{\"answer\":0.7}\n"
		cause         = "onesie: line 3: 503 status code, no body\n"
	)

	tests := []struct {
		name       string
		args       []string
		stdin      string
		mock       string
		wantCode   int
		wantOut    string
		wantErr    string
		unordered  bool
		wantErrHas string
	}{
		{
			name: "should print the lines that pass and name the error on stderr",
			args: []string{"is this urgent", "-i", "lines", "--keep", "pass"}, stdin: "a\nb\nc\nd\n", mock: passFailError,
			wantCode: ExitRecords, wantOut: "a\nd\n", wantErr: cause,
		},
		{
			name:  "should print a jsonl record exactly as read",
			args:  []string{"is this urgent", "-i", "jsonl", "--map", ".t", "--keep", "pass"},
			stdin: "{\"t\": \"a\",  \"n\": 1.50}\n{\"t\":\"b\"}\n", mock: "{\"answer\":0.9}\n{\"answer\":0.1}\n",
			wantOut: "{\"t\": \"a\",  \"n\": 1.50}\n",
		},
		{
			name: "should print the errored lines under --keep error",
			args: []string{"is this urgent", "-i", "lines", "--keep", "fail,error"}, stdin: "a\nb\nc\nd\n", mock: passFailError,
			wantCode: ExitRecords, wantOut: "b\nc\n", wantErr: cause,
		},
		{
			name: "should drop a blank line under --keep",
			args: []string{"is this urgent", "-i", "lines", "--keep", "pass"}, stdin: "a\n\nb\n", mock: "{\"answer\":0.9}\n{\"answer\":0.1}\n",
			wantOut: "a\n",
		},
		{
			name: "should keep an error per blank line under --skip-blank=false",
			args: []string{"is this urgent", "-i", "lines", "--keep", "pass", "--skip-blank=false"}, stdin: "a\n\n",
			mock: "{\"answer\":0.9}\n{\"answer\":0.9}\n", wantCode: ExitRecords, wantOut: "a\n",
			wantErr: "onesie: line 2: blank line, an empty state is a request the model cannot answer. Pass --skip-blank to drop blank lines\n",
		},
		{
			name:  "should print the csv header, then each kept row with the input separator",
			args:  []string{"is this urgent", "-i", "csv", "--delimiter", "auto", "--map", ".text", "--keep", "pass"},
			stdin: "id;text\n1;\"a; x\"\n2;b\n3;c\n4;d\n", mock: passFailError,
			wantCode: ExitRecords, wantOut: "id;text\n1;\"a; x\"\n4;d\n", wantErr: "onesie: line 4: 503 status code, no body\n",
		},
		{
			name:  "should print the csv header when no row is kept",
			args:  []string{"is this urgent", "-i", "csv", "--map", ".text", "--keep", "pass"},
			stdin: "id,text\n1,a\n", mock: "{\"answer\":0.1}\n", wantOut: "id,text\n",
		},
		{
			name:  "should print the csv header of an input with no rows",
			args:  []string{"is this urgent", "-i", "csv", "--map", ".text", "--keep", "pass"},
			stdin: "id,text\n", mock: "{\"answer\":0.1}\n", wantOut: "id,text\n",
		},
		{
			name:  "should print tsv rows with tabs and no quoting",
			args:  []string{"is this urgent", "-i", "tsv", "--map", ".text", "--keep", "pass"},
			stdin: "id\ttext\n1\t\"q\"\n2\tb\n", mock: "{\"answer\":0.9}\n{\"answer\":0.1}\n", wantOut: "id\ttext\n1\t\"q\"\n",
		},
		{
			name:  "should exit 0 when the gate fails records it does not keep",
			args:  []string{"is this urgent", "-i", "lines", "--assert", "answer.value >= 0.8", "--keep", "fail"},
			stdin: "a\nb\n", mock: "{\"answer\":0.9}\n{\"answer\":0.2}\n", wantOut: "b\n",
		},
		{
			name: "should keep the records the gate calls unsure",
			args: []string{
				"is this urgent", "-i", "lines", "--assert", "answer.value >= 0.8", "--abstain-if", "answer.value >= 0.15",
				"--keep", "unsure",
			},
			stdin: "a\nb\nc\n", mock: "{\"answer\":0.9}\n{\"answer\":0.2}\n{\"answer\":0.1}\n", wantOut: "b\n",
		},
		{
			name: "should judge a pick under --min-confidence alone as -q does",
			args: []string{
				"which team", "--pick", "billing,support", "--min-confidence", "0.7", "--fallback", "review",
				"-i", "lines", "--keep", "fail",
			},
			stdin: "a\nb\n", mock: "{\"answer\":{\"value\":\"billing\",\"confidence\":0.9,\"p\":{\"billing\":0.95,\"support\":0.05}}}\n" +
				"{\"answer\":{\"value\":\"billing\",\"confidence\":0.1,\"p\":{\"billing\":0.55,\"support\":0.45}}}\n",
			wantOut: "b\n",
		},
		{
			name:  "should stop at the first fail without --assert and still exit 0",
			args:  []string{"is this urgent", "-i", "lines", "--keep", "pass", "--stop-on", "fail"},
			stdin: "a\nb\nc\n", mock: "{\"answer\":0.9}\n{\"answer\":0.2}\n{\"answer\":0.9}\n", wantOut: "a\n",
		},
		{
			name: "should leave a stopping error to the error onesie reports for it",
			args: []string{"is this urgent", "-i", "lines", "--keep", "pass", "--stop-on", "error"}, stdin: "a\nb\nc\nd\n",
			mock: passFailError, wantCode: ExitUnavailable, wantOut: "a\n", wantErr: "onesie: 503 status code, no body\n",
		},
		{
			name: "should name five causes, then count the rest",
			args: []string{"is this urgent", "-i", "lines", "--keep", "pass"}, stdin: "1\n2\n3\n4\n5\n6\n7\n",
			mock: "{\"error\":503}\n", wantCode: ExitRecords,
			wantErr: "onesie: line 1: 503 status code, no body\nonesie: line 2: 503 status code, no body\n" +
				"onesie: line 3: 503 status code, no body\nonesie: line 4: 503 status code, no body\n" +
				"onesie: line 5: 503 status code, no body\nonesie: and 2 more errors\n",
		},
		{
			name:  "should name a kept error it has no text to print",
			args:  []string{"is this urgent", "-i", "lines", "--keep", "error"},
			stdin: strings.Repeat("x", 8<<20+1) + "\nok\n", mock: "{\"answer\":0.9}\n", wantCode: ExitRecords,
			wantErr: "onesie: line 1: line is longer than 8388608 bytes, the max-line-bytes limit\n",
		},
		{
			name:  "should print every kept line under --unordered",
			args:  []string{"is this urgent", "-i", "lines", "--keep", "pass", "-j", "4", "--unordered"},
			stdin: "1\n2\n3\n4\n5\n6\n", mock: "{\"answer\":0.9}\n", wantOut: "1\n2\n3\n4\n5\n6\n", unordered: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{"ONESIE_MOCK": writeMock(t, tc.mock)}
			out, errOut, code := runMocked(t, t.Context(), tc.args, tc.stdin, env, nil)

			if tc.unordered {
				out = sortedLines(out)
			}

			if code != tc.wantCode || out != tc.wantOut || errOut != tc.wantErr {
				t.Errorf("exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q",
					code, out, errOut, tc.wantCode, tc.wantOut, tc.wantErr)
			}
		})
	}

	t.Run("should write to --out what the same run writes without --keep", func(t *testing.T) {
		t.Parallel()

		const stdin = "a\nb\nc\nd\n"

		dir := t.TempDir()
		env := map[string]string{"ONESIE_MOCK": writeMock(t, passFailError)}
		kept, without := filepath.Join(dir, "kept.jsonl"), filepath.Join(dir, "without.jsonl")

		out, errOut, code := runMocked(t, t.Context(),
			[]string{"is this urgent", "-i", "lines", "--keep", "pass", "--out", kept}, stdin, env, nil)
		if code != ExitRecords || out != "a\nd\n" || errOut != cause {
			t.Errorf("exit %d, stdout %q, stderr %q, want exit 6, the kept lines and the cause", code, out, errOut)
		}

		if _, errOut, code := runMocked(t, t.Context(),
			[]string{"is this urgent", "-i", "lines", "--out", without}, stdin, env, nil); code != ExitRecords {
			t.Fatalf("exit %d without --keep, stderr %q", code, errOut)
		}

		got, want := readText(t, kept), readText(t, without)
		if got != want || strings.Contains(got, "verdict") {
			t.Errorf("--out under --keep =\n%s\nwant what the run without --keep writes\n%s", got, want)
		}
	})
}
