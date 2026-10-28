package skillcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCheckWriting(t *testing.T) {
	t.Parallel()

	t.Run("should check the four root docs and the skill prose, naming the text and the line", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeRootDocs(t, root, map[string]string{
			"REFERENCE.md":   "# Reference\n\nA built-in set.\n",
			"MAINTAINING.md": "Maintain it; carefully.\n",
		})
		writeCheckFixture(t, root, "demo", `{
			"name": "demo",
			"description": "Use when it rains (or snows).",
			"intro": "intro.md",
			"rules": [{"id": "r1", "short": "Ask once.", "why": "Why ask twice?"}]
		}`)

		if err := os.WriteFile(filepath.Join(root, "skills", "demo", "intro.md"), []byte("A one-line intro.\n"), 0o600); err != nil {
			t.Fatalf("write intro.md: %v", err)
		}

		report, err := CheckWriting(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []string{
			"onesie: REFERENCE.md, line 3: a hyphen between two letters in 'built-in'",
			"onesie: MAINTAINING.md, line 1: a semicolon in 'it;'",
			"onesie: skill 'demo' description, line 1: a parenthesis in '(or'",
			"onesie: skill 'demo' description, line 1: a parenthesis in 'snows).'",
			"onesie: skill 'demo' rule 'r1', line 3: a question mark in 'twice?'",
			"onesie: skill 'demo' intro.md, line 1: a hyphen between two letters in 'one-line'",
		}
		if !reflect.DeepEqual(report.Failures, want) || report.Checked != 8 {
			t.Errorf("report = %+v, want %q over 8 texts", report, want)
		}
	})

	t.Run("should not read a rule's good and bad examples", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeRootDocs(t, root, nil)
		writeCheckFixture(t, root, "demo", `{
			"name": "demo",
			"description": "A demo skill.",
			"rules": [{"id": "r1", "short": "Ask once.", "why": "Because.", "bad": "onesie 'is it bad?'", "good": "onesie 'is it good (really)'"}]
		}`)

		report, err := CheckWriting(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(report.Failures) != 0 {
			t.Errorf("Failures = %q, want none", report.Failures)
		}
	})

	t.Run("should return the error when a root doc is missing", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeRootDocs(t, root, nil)

		if err := os.Remove(filepath.Join(root, "CONTRIBUTING.md")); err != nil {
			t.Fatal(err)
		}

		if _, err := CheckWriting(root); err == nil {
			t.Fatalf("CheckWriting(...) error = nil, want an error")
		}
	})
}

func writeRootDocs(t *testing.T, root string, docs map[string]string) {
	t.Helper()

	for _, name := range []string{"README.md", "REFERENCE.md", "CONTRIBUTING.md", "MAINTAINING.md"} {
		body, found := docs[name]
		if !found {
			body = "Fine.\n"
		}

		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestWritingProblems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "should pass a sentence that uses only commas, periods, colons and exclamation marks",
			text: "Ask once, then gate: it works! Done.",
		},
		{
			name: "should fail a semicolon, a question mark and a parenthesis",
			text: "Ask once; then why? (Because.)",
			want: []string{
				"1: a semicolon in 'once;'", "1: a question mark in 'why?'",
				"1: a parenthesis in '(Because.)'",
			},
		},
		{
			name: "should fail an em dash and an en dash",
			text: "Ask once \u2014 then gate.\nPages 1\u20132.",
			want: []string{"1: an em dash in '\u2014'", "2: an en dash in '1\u20132.'"},
		},
		{
			name: "should fail a hyphen between two letters and pass one beside a digit",
			text: "A built-in set, 81-99% caught, IPv4-mapped.",
			want: []string{"1: a hyphen between two letters in 'built-in'"},
		},
		{
			name: "should not count a list marker or a word that starts with a dash",
			text: "- Pass --stop-on-error or -o json.",
		},
		{
			name: "should skip code spans, one that runs across lines included",
			text: "Run `a; b?` and ``c `(d)` e``.\nThen `the\nprovider's re-run` ends.",
		},
		{
			name: "should check the text after a backtick that never closes",
			text: "A stray ` then a re-run.",
			want: []string{"1: a hyphen between two letters in 're-run.'"},
		},
		{
			name: "should skip fenced blocks and table rows",
			text: "```sh\nonesie 'is it (really) urgent?'\n```\n| a-b | c; d |\n|---|---|",
		},
		{
			name: "should skip link targets and URLs",
			text: "See [the table](REFERENCE.md#exit-codes), <https://x.dev/a-b?c> and https://x.dev/(d)?e.",
		},
		{
			name: "should pass a hyphenated name on the allow list",
			text: "Load onesie-stream, then ask shell-safety.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, problem := range writingProblems(tc.text) {
				got = append(got, fmt.Sprintf("%d: %s", problem.line, problem.what))
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("writingProblems(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
