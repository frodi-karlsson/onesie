package skillcheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	rootHelp = `onesie is a Unix filter.

Usage:
  onesie [question] [flags]
  onesie [command]

Available Commands:
  auth        Store the key
  calibrate   Score questions

Flags:
      --assert stringArray   boolean expression
      --cache                store responses
  -f, --file string          question file
  -o, --output string        auto, json
      --provider string      typesafe
  -q, --quiet                suppress output
      --state string         state to evaluate

Use "onesie [command] --help" for more information about a command.
`
	calibrateHelp = `calibrate asks Jev about labelled records.

--out keeps the answers as -o json lines.

Usage:
  onesie calibrate [question] [flags]

Examples:
  onesie calibrate --ask urgent='is this urgent' -i jsonl --map '.body' \
      --label urgent='.is_urgent' --id '.id' --out answers.jsonl --resume < labelled.jsonl

Flags:
      --label stringArray   [ID=]EXPR
      --offline             read every answer from the file
      --require stringArray   a requirement

Global Flags:
      --provider string   typesafe
`
	authHelp = `Usage:
  onesie auth [command]

Available Commands:
  set         Store a key

Flags:
  -h, --help   help for auth

Global Flags:
      --provider string   typesafe
`
	authSetHelp = `Usage:
  onesie auth set [flags]

Flags:
      --file   store the key in the credential file

Global Flags:
      --provider string   typesafe
`
)

func TestCheckProse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		intro string
		want  []string
	}{
		{
			name:  "should pass flags and commands every help lists",
			intro: "Run `onesie --provider openrouter auth set --file`, then `onesie -f triage calibrate`.",
		},
		{
			name:  "should fail a flag the root help lacks in a onesie code span, naming the skill and the fragment",
			intro: "Run `onesie --cahce --state x`.",
			want:  []string{"onesie: skill 'demo' intro.md: --cahce is not a flag in onesie --help"},
		},
		{
			name:  "should fail a flag written after a subcommand that its help lacks",
			intro: "Run `onesie calibrate --offline --cache`.",
			want:  []string{"onesie: skill 'demo' intro.md: --cache is not a flag in onesie calibrate --help"},
		},
		{
			name:  "should fail a subcommand the help lacks",
			intro: "Run `onesie auth sett`.",
			want:  []string{"onesie: skill 'demo' intro.md: sett is not a command in onesie auth --help"},
		},
		{
			name:  "should fail a bare flag no help lists, and pass one only a subcommand lists",
			intro: "Add --offline and --ofline.",
			want:  []string{"onesie: skill 'demo' intro.md: --ofline is in the --help of no onesie command"},
		},
		{
			name:  "should check a fenced line that starts with onesie",
			intro: "```sh\nonesie --state x \\\n    --nope\n```",
			want:  []string{"onesie: skill 'demo' intro.md: --nope is not a flag in onesie --help"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeCheckFixture(t, root, "demo", `{"name": "demo", "description": "a demo skill", "intro": "intro.md"}`)

			if err := os.WriteFile(filepath.Join(root, "skills", "demo", "intro.md"), []byte(tc.intro), 0o600); err != nil {
				t.Fatalf("write intro.md: %v", err)
			}

			report, err := CheckProse(context.Background(), root, &Runner{Binary: "onesie", help: fakeHelp})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(report.Failures, tc.want) {
				t.Errorf("Failures = %q, want %q", report.Failures, tc.want)
			}
		})
	}

	t.Run("should read the description and each rule's short and why", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeCheckFixture(t, root, "demo", `{
			"name": "demo",
			"description": "Covers --nope.",
			"rules": [{"id": "r1", "short": "Use --bad.", "why": "Because of `+"`--worse`"+`."}]
		}`)

		report, err := CheckProse(context.Background(), root, &Runner{Binary: "onesie", help: fakeHelp})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []string{
			"onesie: skill 'demo' description: --nope is in the --help of no onesie command",
			"onesie: skill 'demo' rule 'r1': --bad is in the --help of no onesie command",
			"onesie: skill 'demo' rule 'r1': --worse is in the --help of no onesie command",
		}
		if !reflect.DeepEqual(report.Failures, want) || report.Checked != 3 {
			t.Errorf("report = %+v, want %q over 3 mentions", report, want)
		}
	})

	t.Run("should return the error when a help cannot be read", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeCheckFixture(t, root, "demo", `{"name": "demo", "description": "Covers --cache."}`)

		runner := &Runner{Binary: "onesie", help: func(context.Context, string, []string) (string, error) {
			return "", errors.New("boom")
		}}

		if _, err := CheckProse(context.Background(), root, runner); err == nil {
			t.Fatalf("CheckProse(...) error = nil, want an error")
		}
	})
}

func fakeHelp(_ context.Context, _ string, args []string) (string, error) {
	switch strings.Join(args, " ") {
	case "--help":
		return rootHelp, nil
	case "calibrate --help":
		return calibrateHelp, nil
	case "auth --help":
		return authHelp, nil
	case "auth set --help":
		return authSetHelp, nil
	default:
		return "", errors.New("unknown command " + strings.Join(args, " "))
	}
}

func TestMentions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "should take a bare flag from plain prose",
			text: "Covers --ask, --pick and --rate.",
			want: []string{"flag ask", "flag pick", "flag rate"},
		},
		{
			name: "should take only the first flag of a code span that starts with one",
			text: "Use `--ask NAME=@FILE --desc`, `--cache=false` and `--map: empty string`.",
			want: []string{"flag ask", "flag cache", "flag map"},
		},
		{
			name: "should keep every word of a code span that starts with onesie",
			text: "Run `onesie --provider openrouter auth set`.",
			want: []string{"command onesie --provider openrouter auth set"},
		},
		{
			name: "should ignore a code span that starts with another tool",
			text: "Use `jq -c --unbuffered`, `git push --force` and `curl -fsSL https://x`.",
		},
		{
			name: "should end a onesie command at a pipe, a chain or a redirection",
			text: "Run `onesie -q --state x | jq --unbuffered`, `onesie --cache && git push --force` " +
				"and `onesie --state x 2>&1`.",
			want: []string{"command onesie -q --state x", "command onesie --cache", "command onesie --state x"},
		},
		{
			name: "should keep a quoted word whole and mark it quoted",
			text: "Run `onesie --ask urgent='is --this urgent' 'auth'`.",
			want: []string{"command onesie --ask urgent='is --this urgent' 'auth'"},
		},
		{
			name: "should ignore a lone double dash",
			text: "Put the flags first, then `--`, then the question -- as here.",
		},
		{
			name: "should take the onesie lines of a fenced block and nothing else from it",
			text: "Before --cache.\n```sh\nonesie --state x \\\n  --cache\ntail -f log | onesie --merge\n--pick\n```\nAfter --merge.",
			want: []string{"flag cache", "flag merge", "command onesie --state x --cache"},
		},
		{
			name: "should read a double backtick span",
			text: "Run ``onesie `x` --cache``.",
			want: []string{"command onesie `x` --cache"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, m := range mentions(tc.text) {
				if m.command == nil {
					got = append(got, "flag "+m.flag)

					continue
				}

				raws := make([]string, len(m.command))
				for i, w := range m.command {
					raws[i] = w.raw
				}

				got = append(got, "command "+strings.Join(raws, " "))
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mentions(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestParseHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want helpPage
	}{
		{
			name: "should read flags, whether each takes a value, and the commands",
			text: rootHelp,
			want: helpPage{
				longTakesValue: map[string]bool{
					"assert": true, "cache": false, "file": true, "output": true, "provider": true,
					"quiet": false, "state": true,
				},
				shortTakesValue: map[string]bool{"f": true, "o": true, "q": false},
				commands:        map[string]bool{"auth": true, "calibrate": true},
			},
		},
		{
			name: "should read global flags and skip the long description and the examples",
			text: calibrateHelp,
			want: helpPage{
				longTakesValue: map[string]bool{
					"label": true, "offline": false, "require": true, "provider": true,
				},
				shortTakesValue: map[string]bool{},
				commands:        map[string]bool{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := parseHelp(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseHelp(...) = %+v, want %+v", got, tc.want)
			}
		})
	}
}
