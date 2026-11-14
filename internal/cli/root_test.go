package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/cli"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

var updateHelp = flag.Bool("update-help", false, "rewrite testdata/help from the help each command prints")

func TestNewRootCmd(t *testing.T) {
	t.Parallel()

	t.Run("should expose topics only through help and preserve ordinary command help", func(t *testing.T) {
		for _, tc := range []struct {
			args     []string
			code     int
			contains string
		}{
			{[]string{"help", "decisions"}, cli.ExitOK, "Decisions"},
			{[]string{"help", "calibrate", "evaluation"}, cli.ExitOK, "Evaluation"},
			{[]string{"help", "questions"}, cli.ExitOK, "questions lists"},
			{[]string{"help", "profile", "create"}, cli.ExitOK, "onesie profile create"},
			{[]string{"help", "decisions", "extra"}, cli.ExitUsage, "onesie help"},
			{[]string{"help", "calibrate", "decisions"}, cli.ExitUsage, "onesie help calibrate"},
			{[]string{"asking", "--print-request", "--state", "text"}, cli.ExitOK, `"instructions":"asking"`},
		} {
			t.Run("should route "+strings.Join(tc.args, " "), func(t *testing.T) {
				out, code := runWithEnv(t, map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()}, tc.args)
				if code != tc.code || !strings.Contains(out, tc.contains) {
					t.Fatalf("exit %d, output %s", code, out)
				}
			})
		}
	})
	t.Run("should share the overview among help entry points", func(t *testing.T) {
		for _, paths := range [][][]string{{nil, {"--help"}, {"help"}}, {{"calibrate"}, {"calibrate", "--help"}, {"help", "calibrate"}}} {
			var first string
			for _, args := range paths {
				out, code := runWithEnv(t, map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()}, args)
				if code != cli.ExitOK {
					t.Fatal(out)
				}
				if first == "" {
					first = out
				} else if out != first {
					t.Fatal("overviews differ")
				}
			}
		}
	})

	t.Run("should refuse every changed mode command before writing", func(t *testing.T) {
		t.Parallel()

		data, err := os.Open("testdata/mode_refusals.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		defer data.Close()

		decoder := json.NewDecoder(data)
		for i := 0; decoder.More(); i++ {
			var tc struct {
				Args  []string
				Error string
			}
			if err := decoder.Decode(&tc); err != nil {
				t.Fatal(err)
			}
			t.Run(fmt.Sprintf("should refuse command %d", i), func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				outFile := filepath.Join(dir, "out")
				args := make([]string, len(tc.Args))
				for i, arg := range tc.Args {
					switch arg {
					case "OUT":
						args[i] = outFile
					case "MISSING":
						args[i] = filepath.Join(dir, "missing")
					case "ENDPOINT":
						args[i] = "http://127.0.0.1:1"
					default:
						args[i] = arg
					}
				}
				env := map[string]string{
					"ONESIE_CONFIG_DIR": filepath.Join(dir, "config"),
					"ONESIE_CACHE_DIR":  filepath.Join(dir, "cache"),
					"ONESIE_BASE_URL":   "http://127.0.0.1:1",
					"TYPESAFE_API_KEY":  "fake-mode-refusal-key",
				}
				root := cli.NewRootCmd(cli.BuildInfo{}, cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader("")), cli.WithStdinTTY(false), cli.WithStdoutTTY(false),
					cli.WithLookupEnv(func(name string) (string, bool) { value, ok := env[name]; return value, ok }))
				var out, errOut bytes.Buffer
				root.Command.SetOut(&out)
				root.Command.SetErr(&errOut)
				root.Command.SetArgs(args)
				if code := root.Execute(t.Context()); code != cli.ExitUsage || out.Len() != 0 || errOut.String() != tc.Error {
					t.Errorf("exit %d, stdout %q, stderr %q, want exit 2 and %q", code, out.String(), errOut.String(), tc.Error)
				}
				if _, err := os.Stat(outFile); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("output file exists: %v", err)
				}
			})
		}
	})

	// A pass, an unsure, then a fail still in flight when the unsure stops the run, then a pass.
	const (
		stopRecords = "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"b\"}\n{\"id\":3,\"body\":\"c\"}\n" +
			"{\"id\":4,\"body\":\"d\"}\n"
		stopMock = "{\"id\":1,\"answer\":0.1}\n{\"id\":2,\"answer\":0.8}\n{\"id\":3,\"answer\":0.9}\n" +
			"{\"id\":4,\"answer\":0.1}\n"
	)

	const answered = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.92}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	const costed = `{"model":"typesafe/jev-1.13-20260917","answers":{"answer":{"type":"noul","noul":0.92}},` +
		`"usage":{"input_tokens":10,"output_tokens":2,"cost":0.000012},"id":"gen-dec-1","provider":"TypeSafe"}`

	const free = `{"model":"typesafe/jev-1.13-20260917","answers":{"answer":{"type":"noul","noul":0.92}},` +
		`"usage":{"input_tokens":10,"output_tokens":2,"cost":0}}`

	const twoAnswers = `{"model":"onesie-1.13.0","answers":` +
		`{"a":{"type":"noul","noul":0.92},"extra":{"type":"noul","noul":0.92}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	const picked = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"choice",` +
		`"choice":"billing","confidence":0.91,` +
		`"probabilities":{"billing":0.91,"technical":0.09}}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	markdownOut := filepath.Join(t.TempDir(), "answers.md")
	missingOut := filepath.Join(t.TempDir(), "missing", "answers.jsonl")

	tests := []struct {
		name     string
		args     []string
		stdin    string
		files    map[string]string
		status   int
		response string
		wantCode int
		contains []string
		absent   []string
		sends    []string
		tty      bool
	}{
		{
			name:     "should keep a table on a terminal with no --out",
			args:     []string{"is this urgent", "--state", "site down"},
			response: answered,
			tty:      true,
			wantCode: cli.ExitOK,
			contains: []string{"answer"},
			absent:   []string{`{"`},
		},
		{
			name:     "should keep the last value of -m typed twice, with a notice naming both",
			args:     []string{"is this urgent", "--state", "x", "-m", "jev-1.13.0", "-m", "jev-preview", "--print-request"},
			wantCode: cli.ExitOK,
			contains: []string{`"model":"jev-preview"`, "onesie: -m is given twice, 'jev-preview' overrides 'jev-1.13.0'\n"},
			absent:   []string{`"model":"jev-1.13.0"`},
		},
		{
			name:     "should print nothing for a setting typed twice with the same value",
			args:     []string{"is this urgent", "--state", "x", "-m", "jev-1.13.0", "--model", "jev-1.13.0", "--print-request"},
			wantCode: cli.ExitOK,
			contains: []string{`"model":"jev-1.13.0"`},
			absent:   []string{"given twice"},
		},
		{
			name:     "should print only the refusal of a refused run that types a setting twice",
			args:     []string{"is this urgent", "-m", "a", "-m", "b", "-i", "jsonl", "-o", "table"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -o table reads one record. Drop -i jsonl or use -o json\n"},
			absent:   []string{"given twice"},
		},
		{
			name: "should note a setting typed twice on calibrate",
			args: []string{
				"calibrate", "--ask", "u=is it urgent", "-i", "jsonl", "--map", ".body", "--label", "u=.u",
				"--model", "a", "-m", "b", "--print-request",
			},
			stdin:    `{"u":true,"body":"down"}` + "\n",
			wantCode: cli.ExitOK,
			contains: []string{"onesie: -m is given twice, 'b' overrides 'a'\n"},
		},
		{
			name:     "should print a multi line --state typed twice as one notice line",
			args:     []string{"is this urgent", "--state", "line one\nline two", "--state", "x", "--print-request"},
			wantCode: cli.ExitOK,
			contains: []string{`onesie: --state is given twice, 'x' overrides 'line one\nline two'` + "\n"},
			absent:   []string{"line two'\n\n", "\nline two"},
		},
		{
			name:     "should report a flag clash before a bad provider",
			args:     []string{"is this urgent", "--provider", "bogus", "-i", "jsonl", "-o", "table"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -o table reads one record. Drop -i jsonl or use -o json\n"},
			absent:   []string{"unknown provider"},
		},
		{
			name:     "should report a flag clash before --profile beside --provider",
			args:     []string{"is this urgent", "--profile", "work", "--provider", "typesafe", "-i", "jsonl", "-o", "table"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -o table reads one record. Drop -i jsonl or use -o json\n"},
			absent:   []string{"--profile and --provider"},
		},
		{
			name:     "should report a missing -f file before a bad provider",
			args:     []string{"-f", "missing.yaml", "--provider", "bogus"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: reading missing.yaml: "},
			absent:   []string{"unknown provider"},
		},
		{
			name:     "should report a --print-questions refusal before a bad provider",
			args:     []string{"is this urgent", "--print-questions", "--provider", "bogus", "-j", "2"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -j does not apply to --print-questions, which reads no input and writes the questions as a question file\n"},
			absent:   []string{"unknown provider"},
		},
		{
			name:     "should refuse -j on one record before a bad provider",
			args:     []string{"is this urgent", "--state", "s", "-j", "4", "--provider", "bogus"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -j applies to streaming input. -i text reads one record. Pass -i jsonl, -i lines, -i csv or -i tsv, or drop -j\n"},
			absent:   []string{"unknown provider"},
		},
		{
			name:     "should name -o for -o jsno --threshold 2",
			args:     []string{"is this urgent", "-o", "jsno", "--threshold", "2"},
			stdin:    "site down",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -o takes auto, json, values, table, raw, csv, tsv or markdown, got 'jsno'\n"},
			absent:   []string{"threshold"},
		},
		{
			name:     "should name -o before -i, in table order",
			args:     []string{"is this urgent", "-i", "bogus", "-o", "jsno"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -o takes auto, json, values, table, raw, csv, tsv or markdown, got 'jsno'\n"},
		},
		{
			name:     "should name a bad -o before a flag clash",
			args:     []string{"is this urgent", "-o", "jsno", "-i", "jsonl", "--state", "x"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -o takes auto, json, values, table, raw, csv, tsv or markdown, got 'jsno'\n"},
		},
		{
			name:     "should name a number out of range before a mode refusal",
			args:     []string{"is this urgent", "--print-questions", "-j", "0"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -j takes a positive number of records in flight, got 0\n"},
		},
		{
			name:     "should name a bad --delimiter before a bad -i",
			args:     []string{"is this urgent", "--delimiter", "ab", "-i", "bogus"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --delimiter takes auto or one character, got 'ab'\n"},
		},
		{
			name:     "should refuse -q under -o markdown",
			args:     []string{"is this urgent", "-o", "markdown", "-q"},
			stdin:    "site down",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -q suppresses output, which leaves -o markdown nothing to write"},
		},
		{
			name:     "should refuse --merge under -o md",
			args:     []string{"is this urgent", "-i", "jsonl", "-o", "md", "--merge"},
			stdin:    "{}\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --merge needs -o json, values, csv or tsv"},
		},
		{
			name:     "should refuse --resume under -o markdown",
			args:     []string{"is this urgent", "-i", "jsonl", "-o", "markdown", "--out", markdownOut, "--resume"},
			stdin:    "{}\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --resume reads each record's outcome back from --out, " +
				"which a markdown table does not keep. Use -o json, values, csv or tsv"},
		},
		{
			name:     "should refuse an empty --base-url",
			args:     []string{"is this urgent", "--state", "site down", "--base-url", ""},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --base-url is empty. Name one, or leave the flag out"},
		},
		{
			name:     "should refuse a whitespace --base-url on a dry run",
			args:     []string{"is this urgent", "--state", "site down", "--print-request", "--base-url", " "},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --base-url is empty. Name one, or leave the flag out"},
		},
		{
			name:     "should refuse --merge-key state on a text state",
			args:     []string{"is this urgent", "--state", "hi", "--merge-key", "state"},
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: --merge-key state does not apply to -i text, which wraps every record under a state key",
			},
			absent: []string{`"state":"hi"`},
		},
		{
			name:     "should refuse --merge-key state on a json state that is not an object",
			args:     []string{"is this urgent", "-i", "json", "--state", `"hi"`, "--merge-key", "state"},
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --merge wraps an input that is not an object under 'state', " +
				"so --merge-key state would repeat that key. Pass another --merge-key"},
			absent: []string{`"state":"hi"`},
		},
		{
			name:     "should refuse --merge-key state on a jsonl object that has a state key",
			args:     []string{"is this urgent", "-i", "jsonl", "--merge-key", "state"},
			stdin:    "{\"state\":1}\n",
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: line 1: --merge would overwrite the input's 'state' key, pass --merge-key"},
			absent:   []string{`{"state":{"state":1}`},
		},
		{
			name:     "should refuse --merge-key state on a jsonl record that is not an object",
			args:     []string{"is this urgent", "-i", "jsonl", "--merge-key", "state"},
			stdin:    "{\"a\":1}\n\"hello\"\n",
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: line 2: --merge wraps an input that is not an object under 'state', " +
				"so --merge-key state would repeat that key. Pass another --merge-key"},
			absent: []string{`"state":"hello"`},
		},
		{
			name:     "should refuse --merge-key state on a jsonl line onesie could not read",
			args:     []string{"is this urgent", "-i", "jsonl", "--merge-key", "state"},
			stdin:    "{\"a\":\n",
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: line 1: --merge-key state cannot write this record's error, since its line " +
				"wraps under state: line is not one complete JSON value"},
			absent: []string{`"state":{"error"`},
		},
		{
			name:     "should refuse --merge-key state on a scalar whose --id fails",
			args:     []string{"is this urgent", "-i", "jsonl", "--id", ".id", "--merge-key", "state"},
			stdin:    "\"hello\"\n",
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: line 1: --merge-key state cannot write this record's error, since its line " +
				"wraps under state: --id fails: "},
			absent: []string{`"state":{"error"`},
		},
		{
			name:     "should refuse --merge-key state on a csv row onesie could not read",
			args:     []string{"is this urgent", "-i", "csv", "-o", "json", "--merge-key", "state"},
			stdin:    "a,b\n\"1,2\n",
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: line 2: --merge-key state cannot write this record's error, since its line " +
				"wraps under state: row is not valid csv"},
			absent: []string{`"state":{"error"`},
		},
		{
			name:     "should merge a csv row under --merge-key state",
			args:     []string{"is this urgent", "-i", "csv", "-o", "json", "--merge-key", "state"},
			stdin:    "a,b\n1,2\n",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"state":{`},
		},
		{
			name:     "should merge --map .body on an object under --merge-key state",
			args:     []string{"is this urgent", "-i", "jsonl", "--map", ".body", "--merge-key", "state"},
			stdin:    "{\"body\":\"hi\"}\n",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`{"body":"hi","state":{`},
		},
		{
			name:     "should refuse --map on a scalar record under --merge-key state",
			args:     []string{"is this urgent", "-i", "jsonl", "--map", ".", "--merge-key", "state"},
			stdin:    "\"hello\"\n",
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: line 1: --merge wraps an input that is not an object under 'state'"},
			absent:   []string{`"state":"hello"`},
		},
		{
			name:     "should merge --merge-key state into a jsonl object",
			args:     []string{"is this urgent", "-i", "jsonl", "--merge-key", "state"},
			stdin:    "{\"a\":1}\n",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`{"a":1,"state":{`},
		},
		{
			name:     "should refuse a whitespace -m given as the value of a combined short flag",
			args:     []string{"is this urgent", "--state", "site down", "-qm", " "},
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --model is empty. Name one, or leave the flag out"},
		},
		{
			name:     "should refuse an empty --sep rather than split the options into characters",
			args:     []string{"is it billing", "--sep", "", "--pick", "a,b", "--state", "x"},
			response: picked,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --sep is empty. Name one, or leave the flag out"},
		},
		{
			name:     "should split the options on a --sep of one space",
			args:     []string{"is it billing", "--sep", " ", "--pick", "billing technical", "--print-request", "--state", "x"},
			wantCode: cli.ExitOK,
			contains: []string{`"criteria":{"billing":null,"technical":null}`},
		},
		{
			name: "should refuse an empty --label on calibrate",
			args: []string{
				"calibrate", "--ask", "u=is this urgent", "-i", "jsonl", "--map", ".body", "--label", "u=.u",
				"--label", "",
			},
			stdin:    `{"body":"x","u":true}` + "\n",
			response: answered,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --label is empty. Name one, or leave the flag out"},
		},
		{
			name:     "should name the question argument when it clashes with a file's id",
			args:     []string{"is it", "-f", "ab.json", "--state", "x", "--print-request"},
			files:    map[string]string{"ab.json": `{"questions":{"answer":{"type":"noul","instructions":"q"}}}`},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: 'answer' is defined in ab.json and by the question argument. Pass --replace to override\n"},
		},
		{
			name:     "should refuse a bare word one transposition from a subcommand",
			args:     []string{"questoins"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: questoins is not a subcommand. The closest is questions. " +
					"To ask it as a question, put it after --, as in onesie -- questoins",
			},
		},
		{
			name:     "should refuse a subcommand name in another case",
			args:     []string{"Questions", "--print-request"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"The closest is questions."},
		},
		{
			name:     "should refuse a bare word one letter from a short subcommand",
			args:     []string{"cahce"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"The closest is cache."},
		},
		{
			name:     "should refuse a bare word two edits from a long subcommand",
			args:     []string{"calbirat"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"The closest is calibrate."},
		},
		{
			name:     "should refuse a near miss of the help subcommand",
			args:     []string{"hlep"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"The closest is help."},
		},
		{
			name:     "should ask a one word question that is no near miss",
			args:     []string{"urgent"},
			stdin:    "x",
			response: answered,
			wantCode: cli.ExitOK,
			sends:    []string{`"instructions":"urgent"`},
		},
		{
			name:     "should ask a one word question two edits from a seven letter subcommand",
			args:     []string{"profit", "--print-request"},
			stdin:    "x",
			wantCode: cli.ExitOK,
			contains: []string{`"instructions":"profit"`},
		},
		{
			name:     "should ask a near miss placed after --",
			args:     []string{"--", "questoins"},
			stdin:    "x",
			response: answered,
			wantCode: cli.ExitOK,
			sends:    []string{`"instructions":"questoins"`},
		},
		{
			name:     "should leave a near miss beside --ask to the positional rule",
			args:     []string{"questoins", "--ask", "b=is it billing", "--print-request"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"a positional question cannot be combined with --ask or -f"},
			absent:   []string{"is not a subcommand"},
		},
		{
			name:     "should ask a multi word question that starts with a near miss",
			args:     []string{"questoins about billing", "--print-request"},
			stdin:    "x",
			wantCode: cli.ExitOK,
			contains: []string{`"instructions":"questoins about billing"`},
		},
		{
			name:     "should ask a word with punctuation beside a near miss",
			args:     []string{"cached?", "--print-request"},
			stdin:    "x",
			wantCode: cli.ExitOK,
			contains: []string{`"instructions":"cached?"`},
		},
		{
			name:     "should say a dash question read as flags needs -- before it",
			args:     []string{"-qm is this bad"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: no question given. Pass a question, --ask, or -f. '-qm is this bad' was read as flags. " +
					"A question that starts with - needs -- before it, with the flags placed first, " +
					"as in onesie -- '-qm is this bad'",
			},
		},
		{
			name:     "should say so when a dash question gave a flag a bad value",
			args:     []string{"-is this urgent", "--print-request"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"-i takes", "'-is this urgent' was read as flags"},
		},
		{
			name:     "should say so when a dash question fails to parse",
			args:     []string{"--print-request", "-x is it bad"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag -x. '-x is it bad' was read as flags"},
		},
		{
			name:     "should leave a dash value given to a flag alone",
			args:     []string{"--state", "-qm is this bad"},
			wantCode: cli.ExitUsage,
			contains: []string{"no question given"},
			absent:   []string{"was read as flags"},
		},
		{
			name:     "should leave a dash value given to a shorthand alone",
			args:     []string{"-qm", "-x is it"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"no question given"},
			absent:   []string{"was read as flags"},
		},
		{
			name:     "should leave a dash word with no space alone",
			args:     []string{"-urgent"},
			stdin:    "x",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag -u in -urgent"},
			absent:   []string{"was read as flags"},
		},
		{
			name:     "should say flags go before -- when they follow it",
			args:     []string{"--", "-rm is this bad", "--mock", "m.json", "--state", "x"},
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: expected one question, got 5 arguments. Everything after -- is the question, " +
					"so flags go before --, as in onesie --mock m.json --state x -- '-rm is this bad'",
			},
		},
		{
			name:     "should say flags go before -- under calibrate",
			args:     []string{"calibrate", "--", "-x y", "--map", ".a"},
			wantCode: cli.ExitUsage,
			contains: []string{"so flags go before --"},
		},
		{
			name:     "should not blame flags for plain words after --",
			args:     []string{"--", "is", "urgent"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: expected one question, got 2 arguments. " +
				"Quote a question that holds spaces, as in onesie 'is this urgent'"},
			absent: []string{"flags go before"},
		},
		{
			name:     "should name the near subcommand when a misspelled one takes arguments",
			args:     []string{"profle", "list"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: expected one question, got 2 arguments. profle is not a subcommand, and the closest is profile\n"},
		},
		{
			name:     "should name no subcommand for a near word after --",
			args:     []string{"--", "profle", "list"},
			wantCode: cli.ExitUsage,
			contains: []string{"Quote a question that holds spaces"},
			absent:   []string{"not a subcommand"},
		},
		{
			name:     "should name calibrate when it is given two questions",
			args:     []string{"calibrate", "is", "urgent"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: expected one question, got 2 arguments. " +
				"Quote a question that holds spaces, as in onesie calibrate 'is this urgent'"},
		},
		{
			name:     "should refuse an argument to version in a plain sentence",
			args:     []string{"version", "extra"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: version takes no argument, got 'extra'\n"},
		},
		{
			name:     "should refuse an argument to a completion subcommand in a plain sentence",
			args:     []string{"completion", "bash", "extra"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: completion bash takes no argument, got 'extra'\n"},
		},
		{
			name:     "should name the long form of a shorthand whose value is refused",
			args:     []string{"-j", "abc", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --jobs takes a whole number, got 'abc'\n"},
		},
		{
			name:     "should say what a flag that takes a number got",
			args:     []string{"--jobs", "1.5", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --jobs takes a whole number, got '1.5'\n"},
		},
		{
			name:     "should name the long form when a dash value looks like its shorthand",
			args:     []string{"--state", "-just", "--jobs", "1.5", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --jobs takes a whole number, got '1.5'\n"},
		},
		{
			name:     "should say what a flag that takes true or false got",
			args:     []string{"--cache=maybe", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --cache takes true or false, got 'maybe'\n"},
		},
		{
			name:     "should say a flag needs a value",
			args:     []string{"q", "--state"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --state needs a value\n"},
		},
		{
			name:     "should name the long form of a shorthand that needs a value",
			args:     []string{"q", "-m"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --model needs a value\n"},
		},
		{
			name:     "should name an --out file it cannot open once",
			args:     []string{"q", "-i", "jsonl", "--out", missingOut},
			stdin:    `{"a":1}` + "\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: opening " + missingOut + ": "},
			absent:   []string{"open " + missingOut},
		},
		{
			name:     "should refuse a question id line under -i lines",
			args:     []string{"--ask", "line=is this urgent", "-i", "lines"},
			stdin:    "first\n",
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: question id 'line' is reserved under -i lines, which writes the input line number as line",
			},
		},
		{
			name:     "should refuse a question id line under -i jsonl, which writes the line number too",
			args:     []string{"--ask", "line=is this urgent", "-i", "jsonl", "--print-request"},
			stdin:    "\"first\"\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: question id 'line' is reserved under -i jsonl, which writes the input line number as line"},
		},

		{
			name:     "should refuse an unknown help topic",
			args:     []string{"help", "foo"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: unknown help topic 'foo'. Run onesie help to see the topics and subcommands"},
			absent:   []string{"Usage:"},
		},
		{
			name:     "should refuse an unknown topic under a subcommand",
			args:     []string{"help", "profile", "nope"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: unknown help topic 'profile nope'"},
			absent:   []string{"Usage:"},
		},
		{
			name:     "should refuse true written after --box, since it takes no value",
			args:     []string{"--ask", "mug=the mug", "--box=true", "--print-request", "--state", "s"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --box takes no value, got 'true'\n"},
		},
		{
			name:     "should refuse a flag value that is not valid UTF-8 when the command line is read",
			args:     []string{"--ask", "q=is it", "--image", "n\xff=photo.png", "--print-request", "--state", "{}"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --image is not valid UTF-8\n"},
		},
		{
			name:     "should refuse a question that is not valid UTF-8",
			args:     []string{"is \xff it", "--print-request", "--state", "s"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: the question is not valid UTF-8\n"},
		},
		{
			name:     "should name an unknown long flag in a plain sentence",
			args:     []string{"--nope"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag --nope\n"},
		},
		{
			name:     "should refuse -V as an unknown flag",
			args:     []string{"-V"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag -V\n"},
		},
		{
			name:     "should refuse --version as an unknown flag",
			args:     []string{"--version"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag --version\n"},
		},
		{
			name: "should exit 7 at the first unsure under -j 4, counting only the written records",
			args: []string{
				"is it urgent", "-i", "jsonl", "--map", ".body", "--id", ".id", "--mock", "mock.jsonl", "-o", "json",
				"--assert", "answer.value < 0.5", "--abstain-if", "answer.value < 0.85", "--stop-on=unsure", "--stats",
				"-j", "4",
			},
			stdin:    stopRecords,
			files:    map[string]string{"mock.jsonl": stopMock},
			wantCode: cli.ExitAbstain,
			contains: []string{`{"id":2,"line":2,"verdict":"unsure"`, ", 1 unsure,"},
			absent:   []string{`"id":3`, `"id":4`, " fail"},
		},
		{
			name: "should exit 7 at the first unsure under --unordered, counting only the written records",
			args: []string{
				"is it urgent", "-i", "jsonl", "--map", ".body", "--id", ".id", "--mock", "mock.jsonl", "-o", "json",
				"--assert", "answer.value < 0.5", "--abstain-if", "answer.value < 0.85", "--stop-on=unsure", "--stats",
				"--unordered",
			},
			stdin:    stopRecords,
			files:    map[string]string{"mock.jsonl": stopMock},
			wantCode: cli.ExitAbstain,
			contains: []string{`{"id":2,"line":2,"verdict":"unsure"`, ", 1 unsure,"},
			absent:   []string{`"id":3`, `"id":4`, " fail"},
		},
		{
			name:     "should let a later --state - win over --state with a notice",
			args:     []string{"is this urgent", "--state", "typed", "--state", "-"},
			stdin:    "site down\n",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{"onesie: --state is given twice, '-' overrides 'typed'\n"},
			absent:   []string{"mutually exclusive"},
		},
		{
			name:     "should let a later -o win over -o raw with a notice",
			args:     []string{"is this urgent", "--state", "x", "-o", "raw", "-o", "json"},
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{"onesie: -o is given twice, 'json' overrides 'raw'\n", `"answer":`},
			absent:   []string{"mutually exclusive"},
		},
		{
			name:     "should refuse a verdict --stop-on does not know",
			args:     []string{"is this urgent", "-i", "lines", "--stop-on=warn"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --stop-on takes error, fail or unsure, comma separated, got 'warn'\n"},
		},
		{
			name:     "should refuse --stop-on=fail without --assert",
			args:     []string{"is this urgent", "-i", "lines", "--stop-on=error,fail"},
			stdin:    "a\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --stop-on=fail needs --assert or --keep, since without one no record can fail\n"},
		},
		{
			name:     "should refuse --stop-on=unsure without --assert",
			args:     []string{"is this urgent", "-i", "lines", "--stop-on=unsure"},
			stdin:    "a\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --stop-on=unsure needs --assert, since without one no record can be unsure\n"},
		},
		{
			name:     "should refuse --stop-on=unsure without --abstain-if",
			args:     []string{"is this urgent", "-i", "lines", "--assert", "answer.value < 0.5", "--stop-on=unsure"},
			stdin:    "a\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --stop-on=unsure needs --abstain-if, since without one no record is unsure\n"},
		},
		{
			name:     "should refuse --stop-on=unsure under -i request",
			args:     []string{"-i", "request", "--stop-on=unsure"},
			stdin:    `{"questions":{"answer":{"type":"noul","instructions":"q"}},"state":"x"}` + "\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --stop-on=unsure does not apply to -i request, which sends each body as written and prints each raw response\n"},
		},
		{
			name:     "should refuse --stop-on=unsure under --print-request",
			args:     []string{"is this urgent", "-i", "lines", "--print-request", "--stop-on=unsure"},
			stdin:    "a\n",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --stop-on=unsure does not apply to --print-request, which writes the request bodies and sends none\n"},
		},
		{
			name:     "should name the closest flag to a misspelled one",
			args:     []string{"--modl", "x", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag --modl. The closest is --model\n"},
		},
		{
			name:     "should name the closest flag to a cut short one",
			args:     []string{"--stat", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag --stat. The closest is --state or --stats\n"},
		},
		{
			name:     "should name no closest flag when none is near",
			args:     []string{"--bogus", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag --bogus\n"},
		},
		{
			name:     "should name an unknown shorthand in a plain sentence",
			args:     []string{"-z", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag -z\n"},
		},
		{
			name:     "should name an unknown shorthand at the end of a cluster",
			args:     []string{"-qz", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag -z\n"},
		},
		{
			name:     "should point a long flag given one dash at two",
			args:     []string{"-stats", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag -s in -stats. The closest is --stats\n"},
		},
		{
			name:     "should name an unknown flag on a subcommand in a plain sentence",
			args:     []string{"profile", "list", "--bogus"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: profile list has no flag --bogus\n"},
		},
		{
			name:     "should read a dashed word after a profile name as flags",
			args:     []string{"profile", "create", "work", "--provider", "typesafe", "-xy"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: profile create has no flag -x in -xy\n"},
			absent:   []string{"not a profile name"},
		},
		{
			name:     "should read a dashed word after a profile name as flags beside the run flag note",
			args:     []string{"profile", "update", "work", "-fz"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: profile update has no flag -f in -fz. --file belongs to a run, not to profile update\n"},
			absent:   []string{"not a profile name"},
		},
		{
			name:     "should say a run flag does not belong to profile create",
			args:     []string{"profile", "create", "z", "--provider", "typesafe", "--state", "x"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: profile create has no flag --state. --state belongs to a run, not to profile create\n"},
		},
		{
			name:     "should name the run flag when a subcommand also got a word",
			args:     []string{"questions", "extra", "--stats"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: questions has no flag --stats. --stats belongs to a run, not to questions\n"},
		},
		{
			name:     "should read a dashed word after a flag value as flags",
			args:     []string{"profile", "create", "--provider", "typesafe", "-work"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: profile create has no flag -w in -work\n"},
		},
		{
			name:     "should name no flag one edit past the allowance, even when two tie there",
			args:     []string{"--x", "q"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: there is no flag --x\n"},
		},
		{
			name:     "should read a profile name that starts with a dash as flags",
			args:     []string{"profile", "create", "-work", "--provider", "typesafe"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: profile create has no flag -w in -work\n"},
		},
		{
			name:     "should answer a question that begins with a dash after --",
			args:     []string{"-o", "json", "--", "-is this urgent"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"answer":{"value":0.92}`},
		},
		{
			name:     "should answer a positional question from stdin",
			args:     []string{"is this urgent", "-o", "json"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"answer":{"value":0.92}`, `"model":"onesie-1.13.0"`},
			absent:   []string{`"usage"`},
		},
		{
			name:     "should add usage when asked",
			args:     []string{"is this urgent", "-o", "json", "--usage"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"usage":{"input_tokens":10,"output_tokens":2}`},
		},
		{
			name:     "should refuse --usage with -o values before any request",
			args:     []string{"is this urgent", "-o", "values", "--usage"},
			stdin:    "the server is down",
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --usage does not apply to -o values, which writes only the answers. Use -o json"},
		},
		{
			name:     "should refuse --usage with -q before any request",
			args:     []string{"is this urgent", "-q", "--usage"},
			stdin:    "the server is down",
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --usage does not apply to -q, which suppresses output"},
		},
		{
			name:     "should read a semicolon csv and write tsv under --delimiter ';'",
			args:     []string{"is this urgent", "-i", "csv", "--map", ".body", "--delimiter", ";", "-o", "tsv", "--mock", "m.json"},
			files:    map[string]string{"m.json": `{"answer":0.9}`},
			stdin:    "id;body\n1;site down, now\n",
			contains: []string{"answer\terror\n0.9\t\n"},
		},
		{
			name:     "should write the detected separator for -i csv --delimiter auto -o csv",
			args:     []string{"is this urgent", "-i", "csv", "--map", ".body", "--delimiter", "auto", "-o", "csv", "--mock", "m.json"},
			files:    map[string]string{"m.json": `{"answer":0.9}`},
			stdin:    "id;body\n1;site down\n",
			contains: []string{"answer;error\n0.9;\n"},
		},
		{
			name:     "should refuse --merge-key with --merge and -o csv before any request",
			args:     []string{"is this urgent", "-i", "csv", "-o", "csv", "--merge", "--merge-key", "k"},
			stdin:    "id,body\n1,the server is down\n",
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --merge-key does not apply to -o csv, which puts the answers in columns"},
		},
		{
			name:     "should refuse --usage with -o raw before any request",
			args:     []string{"is this urgent", "-o", "raw", "--usage"},
			stdin:    "the server is down",
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --usage does not apply to -o raw, which writes only the answers. Use -o json"},
		},
		{
			name:     "should refuse --usage with -o raw over a stream before any request",
			args:     []string{"is this urgent", "-i", "jsonl", "-o", "raw", "--usage"},
			stdin:    "{\"a\":1}\n",
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --usage does not apply to -o raw, which writes only the answers. Use -o json"},
		},
		{
			name:     "should exit two before any request when one record maps to an empty string",
			args:     []string{"is this urgent", "-i", "json", "--map", ".body"},
			stdin:    `{"body":""}`,
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --map: empty string, an empty state is a request the model cannot answer"},
		},
		{
			name:     "should exit two before any request on an empty json string --state",
			args:     []string{"is this urgent", "-i", "json", "--state", `""`},
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: empty string, an empty state is a request the model cannot answer"},
		},
		{
			name:     "should exit two before any request on an empty object on stdin",
			args:     []string{"is this urgent", "-i", "json"},
			stdin:    "{}",
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: empty object, an empty state is a request the model cannot answer"},
		},
		{
			name:     "should exit two before any request when one record maps to an empty array",
			args:     []string{"is this urgent", "-i", "json", "--map", ".body"},
			stdin:    `{"body":[]}`,
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --map: empty array, an empty state is a request the model cannot answer"},
		},
		{
			name:     "should exit two before any request on an empty --state",
			args:     []string{"is this urgent", "--state", ""},
			status:   http.StatusInternalServerError,
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --state is blank, an empty state is a request the model cannot answer"},
		},
		{
			name:     "should add the cost to usage when the response carries one",
			args:     []string{"is this urgent", "-o", "json", "--usage"},
			stdin:    "the server is down",
			response: costed,
			wantCode: cli.ExitOK,
			contains: []string{`"usage":{"input_tokens":10,"output_tokens":2,"cost":0.000012}`},
		},
		{
			name:     "should add the cost to the table's usage when the response carries one",
			args:     []string{"is this urgent", "-o", "table", "--usage"},
			stdin:    "the server is down",
			response: costed,
			wantCode: cli.ExitOK,
			contains: []string{"10 in / 2 out, $0.000012\n"},
		},
		{
			name:     "should add the cost to --stats when the response carries one",
			args:     []string{"is this urgent", "--stats"},
			stdin:    "the server is down",
			response: costed,
			wantCode: cli.ExitOK,
			contains: []string{"1 request, 1 question asked, 10 in / 2 out tokens, $0.000012, model "},
		},
		{
			name:     "should keep a zero cost in usage",
			args:     []string{"is this urgent", "-o", "json", "--usage"},
			stdin:    "the server is down",
			response: free,
			wantCode: cli.ExitOK,
			contains: []string{`"usage":{"input_tokens":10,"output_tokens":2,"cost":0}`},
		},
		{
			name:     "should print the bare value with -o raw",
			args:     []string{"is this urgent", "-o", "raw"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{"0.92"},
		},
		{
			name:     "should answer a pick question",
			args:     []string{"which team", "--pick", "billing,technical", "-o", "raw"},
			stdin:    "i was charged twice",
			response: picked,
			wantCode: cli.ExitOK,
			contains: []string{"billing"},
		},
		{
			name:     "should exit ok with -q above the threshold",
			args:     []string{"is this urgent", "-q", "--threshold", "0.9"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			absent:   []string{"0.92"},
		},
		{
			name:     "should exit fail with -q below the threshold",
			args:     []string{"is this urgent", "-q", "--threshold", "0.95"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitFail,
		},
		{
			name:     "should refuse a state-less invocation, saying the stdin it read was empty",
			args:     []string{"is this urgent"},
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: no state given, since stdin was empty. Pipe one to stdin, or pass --state",
			},
		},
		{
			name:     "should say stdin was empty under --state -",
			args:     []string{"is this urgent", "--state", "-"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: no state given, since stdin was empty"},
		},
		{
			name:     "should refuse a flagged invocation that carries no question",
			args:     []string{"-o", "json"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: no question given. Pass a question, --ask, or -f"},
			absent:   []string{"Usage:", "Flags:"},
		},
		{
			name:     "should refuse -o raw with two questions",
			args:     []string{"--ask", "a=one", "--ask", "b=two", "-o", "raw"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: -o raw needs a single question. 'a', 'b' were asked"},
		},
		{
			name:     "should refuse two --ask flags sharing an id",
			args:     []string{"--ask", "a=one", "--ask", "a=two"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: question id 'a' is given twice"},
		},
		{
			name:     "should refuse a reserved question id",
			args:     []string{"--ask", "model=one"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"'model' is reserved"},
		},
		{
			name: "should warn about a partly described option set",
			args: []string{
				"which team", "--pick", "billing,technical", "--desc", "billing=payments", "-o", "raw",
			},
			stdin:    "i was charged twice",
			response: picked,
			wantCode: cli.ExitOK,
			contains: []string{"warning:", "but not technical"},
		},
		{
			name: "should print the fallback word when the request fails",
			args: []string{
				"is this safe", "--pick", "safe,refuse",
				"--min-confidence", "0.7", "--fallback", "refuse", "-o", "raw",
			},
			stdin:    "rm -rf /",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{"refuse"},
		},
		{
			name:     "should print an empty line when a failed question has no fallback",
			args:     []string{"is this urgent", "-o", "raw"},
			stdin:    "body",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			absent:   []string{"refuse"},
		},
		{
			name:  "should exit unavailable when the answer has the wrong shape",
			args:  []string{"is this urgent", "-o", "json"},
			stdin: "body",
			response: `{"model":"onesie-1.13.0","answers":{"answer":{"type":"choice",` +
				`"choice":"a","confidence":0.5,"probabilities":{"a":1}}}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{
				`"kind":"response"`, `"status":200`,
				"expects a noul answer, got choice",
			},
			absent: []string{`"kind":"transport"`},
		},
		{
			name: "should emit a response error for a 200 body it cannot use",
			args: []string{
				"--ask", "team=which team", "--pick", "billing,technical",
				"--min-confidence", "0.7", "--fallback", "human", "-o", "json",
			},
			stdin:    "body",
			response: "not json at all",
			wantCode: cli.ExitUnavailable,
			contains: []string{
				`"error"`, `"kind":"response"`, `"status":200`,
				`"decision":"human"`, `"fallback":"error"`, "the response is not JSON, check the base URL",
			},
			absent: []string{`"kind":"transport"`, `"kind":"http"`},
		},
		{
			name: "should emit the error key in json on failure",
			args: []string{
				"--ask", "team=which team", "--pick", "billing,technical",
				"--min-confidence", "0.7", "--fallback", "human", "-o", "json",
			},
			stdin:    "body",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{
				`"error"`, `"kind":"http"`, `"status":500`,
				`"decision":"human"`, `"fallback":"error"`,
			},
		},
		{
			name:  "should keep the usage of an answer it cannot use on the error record under --usage",
			args:  []string{"is this urgent", "--usage", "-o", "json"},
			stdin: "body",
			response: `{"model":"onesie-1.13.0","usage":{"input_tokens":7,"output_tokens":3},"answers":{"answer":` +
				`{"type":"choice","choice":"a","confidence":0.5,"probabilities":{"a":1}}}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{`"kind":"response"`, `"usage":{"input_tokens":7,"output_tokens":3}`},
		},
		{
			name:  "should answer questions from a file",
			args:  []string{"-f", "q.yaml", "-o", "json"},
			stdin: "the server is down",
			files: map[string]string{"q.yaml": "urgent: does this convey urgency\n"},
			response: `{"model":"onesie-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":1,"output_tokens":1}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"urgent":{"value":0.92}`},
		},
		{
			name:  "should apply policy from a file",
			args:  []string{"-f", "q.yaml", "-o", "json"},
			stdin: "the server is down",
			files: map[string]string{
				"q.yaml": "urgent:\n  ask: does this convey urgency\n  threshold: 0.5\n",
			},
			response: `{"model":"onesie-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"decision":true`},
		},
		{
			name:     "should refuse an id defined in both the file and --ask",
			args:     []string{"-f", "q.yaml", "--ask", "urgent=different"},
			stdin:    "body",
			files:    map[string]string{"q.yaml": "urgent: does this convey urgency\n"},
			wantCode: cli.ExitUsage,
			contains: []string{"q.yaml", "Pass --replace to override"},
		},
		{
			name:     "should let --replace override a file question",
			args:     []string{"-f", "q.yaml", "--ask", "urgent=different", "--replace", "-o", "raw"},
			stdin:    "body",
			files:    map[string]string{"q.yaml": "urgent: does this convey urgency\n"},
			response: `{"model":"onesie-1.13.0","answers":{"urgent":{"type":"noul","noul":0.5}}}`,
			wantCode: cli.ExitOK,
			contains: []string{"0.5"},
			sends:    []string{"different"},
		},
		{
			name:     "should report a missing file",
			args:     []string{"-f", "absent.yaml"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"absent.yaml"},
		},
		{
			name: "should take the state from a request body",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"from the body","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"a":{"value":0.3}`},
			sends:    []string{`"state":"from the body"`},
		},
		{
			name: "should send a body's state in the order it was written",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":{"ticket_id":12345678901234567890,"zebra":1,"alpha":2},` +
					`"questions":{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"state":{"ticket_id":12345678901234567890,"zebra":1,"alpha":2}`,
			},
		},
		{
			name:  "should keep a body's state and leave stdin unread",
			args:  []string{"-f", "body.json", "-o", "json"},
			stdin: "from stdin",
			files: map[string]string{
				"body.json": `{"state":"from the body","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"a":{"value":0.3}`},
			sends:    []string{`"state":"from the body"`},
		},
		{
			name: "should refuse --state beside a body's state, naming both",
			args: []string{"-f", "body.json", "--state", "from the flag"},
			files: map[string]string{
				"body.json": `{"state":"from the body","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --state and the state in body.json both give a state. Drop one"},
		},
		{
			name: "should refuse --state - beside a body's state",
			args: []string{"-f", "body.json", "--state", "-"},
			files: map[string]string{
				"body.json": `{"state":"from the body","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			stdin:    "from stdin",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --state and the state in body.json both give a state. Drop one"},
		},
		{
			name:  "should read stdin beside a body with no state",
			args:  []string{"-f", "body.json", "-o", "json"},
			stdin: "from stdin",
			files: map[string]string{
				"body.json": `{"questions":{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"state":"from stdin"`},
		},
		{
			name: "should take the model from a request body",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","model":"onesie-1.9.9","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"onesie-1.9.9","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			contains: []string{`"model":"onesie-1.9.9"`},
			sends:    []string{`"model":"onesie-1.9.9"`},
		},
		{
			name: "should let -m override a request body's model",
			args: []string{"-f", "body.json", "-o", "json", "-m", "onesie-1.2.0"},
			files: map[string]string{
				"body.json": `{"state":"x","model":"onesie-1.9.9","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: `{"model":"onesie-1.2.0","answers":{"a":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"model":"onesie-1.2.0"`},
		},
		{
			name: "should refuse a null state in a request body",
			args: []string{"-f", "body.json"},
			files: map[string]string{
				"body.json": `{"state":null,"questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitUsage,
			contains: []string{"state must be a string, object or array"},
		},
		{
			name: "should refuse a policy flag on a multi question body",
			args: []string{"-f", "body.json", "--threshold", "0.5"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"a":{"type":"noul","instructions":"q"},` +
					`"b":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --threshold applies to one question, and 2 were asked: 'a', 'b'. " +
				"Put it after --for ID to set one, as in --for a --threshold 0.5\n"},
		},
		{
			name: "should set each --for group's setting on its question only, with a notice each",
			args: []string{
				"-f", "mod.yaml", "--for", "tone", "--fallback", "human", "--for", "spam", "--threshold", "0.6",
				"--print-questions",
			},
			files: map[string]string{
				"mod.yaml": "tone:\n  ask: what tone\n  pick: [calm, angry]\nspam: is this spam\n",
			},
			wantCode: cli.ExitOK,
			contains: []string{
				"onesie: --fallback sets the question file's fallback for 'tone'\n" +
					"onesie: --threshold sets the question file's threshold for 'spam'\n",
				"tone:\n  ask: what tone\n  pick:\n  - calm\n  - angry\n  fallback: human\n",
				"spam:\n  ask: is this spam\n  threshold: 0.6\n",
			},
		},
		{
			name: "should print the --for notices in the order typed",
			args: []string{
				"-f", "mod.yaml", "--for", "tone", "--fallback", "human", "--for", "spam", "--threshold", "0.6",
				"--print-questions",
			},
			files: map[string]string{
				"mod.yaml": "spam: is this spam\ntone:\n  ask: what tone\n  pick: [calm, angry]\n",
			},
			wantCode: cli.ExitOK,
			contains: []string{
				"onesie: --fallback sets the question file's fallback for 'tone'\n" +
					"onesie: --threshold sets the question file's threshold for 'spam'\n",
			},
		},
		{
			name:     "should replace a lone file question's threshold, with a notice",
			args:     []string{"-f", "one.yaml", "--threshold", "0.9", "--print-questions"},
			files:    map[string]string{"one.yaml": "urgent:\n  ask: is this urgent\n  threshold: 0.7\n"},
			wantCode: cli.ExitOK,
			contains: []string{
				"onesie: --threshold overrides the question file's threshold for 'urgent'\n",
				"  threshold: 0.9\n",
			},
			absent: []string{"0.7"},
		},
		{
			name:     "should give a lone file question a threshold it lacked, with a sets notice",
			args:     []string{"-f", "one.yaml", "--threshold", "0.9", "--print-questions"},
			files:    map[string]string{"one.yaml": "urgent: is this urgent\n"},
			wantCode: cli.ExitOK,
			contains: []string{"onesie: --threshold sets the question file's threshold for 'urgent'\n", "  threshold: 0.9\n"},
		},
		{
			name:     "should refuse --for naming no question of the file, listing the file's",
			args:     []string{"-f", "two.yaml", "--for", "nope", "--threshold", "0.9", "--print-questions"},
			files:    map[string]string{"two.yaml": "a: is it a\nb: is it b\n"},
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: --for nope names no question in two.yaml, which has: 'a', 'b'. " +
					"Define a new question with --ask nope=QUESTION\n",
			},
		},
		{
			name:     "should refuse --for without -f",
			args:     []string{"--ask", "a=is it a", "--for", "a", "--threshold", "0.9", "--print-questions"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --for selects a question from -f, which was not given\n"},
		},
		{
			name:     "should refuse --for without -f before the values after it",
			args:     []string{"--ask", "a=is it a", "--for", "a", "--threshold", "high", "--print-questions"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --for selects a question from -f, which was not given\n"},
			absent:   []string{"takes a number"},
		},
		{
			name:     "should refuse --for beside --replace",
			args:     []string{"-f", "two.yaml", "--replace", "--for", "a", "--threshold", "0.9", "--print-questions"},
			files:    map[string]string{"two.yaml": "a: is it a\nb: is it b\n"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --for never replaces a question, so --replace has nothing to do beside it"},
		},
		{
			name: "should set a policy on one question of a request body through --for",
			args: []string{"-f", "body.json", "--for", "b", "--threshold", "0.8", "--print-questions"},
			files: map[string]string{
				"body.json": `{"questions":{"a":{"type":"noul","instructions":"q"},` +
					`"b":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitOK,
			contains: []string{"onesie: --threshold sets the request body's threshold for 'b'\n", "  threshold: 0.8\n"},
		},
		{
			name: "should refuse --pick after --for on a request body question",
			args: []string{"-f", "body.json", "--for", "b", "--pick", "x,y", "--print-questions"},
			files: map[string]string{
				"body.json": `{"questions":{"a":{"type":"noul","instructions":"q"},` +
					`"b":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --pick cannot reshape a request body's question. A body carries its own type and criteria\n"},
		},
		{
			name:     "should keep the last of a question setting typed twice for one --ask, with a notice",
			args:     []string{"--ask", "a=is it a", "--threshold", "0.5", "--threshold", "0.6", "--print-questions"},
			wantCode: cli.ExitOK,
			contains: []string{"onesie: --threshold is given twice for 'a', '0.6' overrides '0.5'\n", "  threshold: 0.6\n"},
		},
		{
			name: "should keep the last of a question setting typed twice across --for groups, with a notice",
			args: []string{
				"-f", "mod.yaml", "--for", "tone", "--fallback", "human", "--for", "tone", "--fallback", "x",
				"--print-questions",
			},
			files:    map[string]string{"mod.yaml": "tone:\n  ask: what tone\n  pick: [calm, angry]\nspam: is this spam\n"},
			wantCode: cli.ExitOK,
			contains: []string{
				"onesie: --fallback sets the question file's fallback for 'tone'\n" +
					"onesie: --fallback is given twice for 'tone', 'x' overrides 'human'\n",
				"  fallback: x\n",
			},
		},
		{
			name:     "should refuse --pick sales on a one option result",
			args:     []string{"-f", "team.yaml", "--pick", "sales", "--print-questions"},
			files:    map[string]string{"team.yaml": "team:\n  ask: who owns this\n  pick: [billing, technical]\n"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --pick needs at least two options in question 'team', got 1: sales\n"},
			absent:   []string{"overrides"},
		},
		{
			name:     "should refuse --pick on a yes or no file question",
			args:     []string{"-f", "yesno.yaml", "--pick", "a,b", "--print-questions"},
			files:    map[string]string{"yesno.yaml": "urgent: is this urgent\n"},
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: --pick cannot make 'urgent' a choice question, since a flag never changes the shape of a " +
					"question from yesno.yaml. Redefine it whole with --ask urgent=QUESTION --replace\n",
			},
		},
		{
			name:     "should refuse --rate after --for on a file pick question",
			args:     []string{"-f", "two.yaml", "--for", "team", "--rate", "low,high", "--print-questions"},
			files:    map[string]string{"two.yaml": "team:\n  ask: who owns this\n  pick: [billing, technical]\nb: is it b\n"},
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --rate cannot make 'team' a score question"},
		},
		{
			name: "should allow policy on an ask added beside a one question body",
			args: []string{
				"-f", "body.json", "--ask", "extra=is this urgent",
				"--threshold", "0.8", "-o", "json",
			},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"a":{"type":"noul","instructions":"q"}}}`,
			},
			response: twoAnswers,
			wantCode: cli.ExitOK,
			contains: []string{`"a":{"value":0.92`, `"extra":{"value":0.92`},
		},
		{
			name: "should refuse a top level flag when a file and one ask both ask, naming --for",
			args: []string{
				"-f", "one.yaml", "--threshold", "0.8", "--ask", "other=is this urgent",
			},
			files:    map[string]string{"one.yaml": "urgent: is this urgent\n"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"--threshold applies to one question, and 2 were asked: 'urgent', 'other'. " +
				"Put it after --for ID to set one, as in --for urgent --threshold 0.8"},
		},
		{
			name:     "should name --for for --min-confidence beside a file of several questions",
			args:     []string{"-f", "two.yaml", "--min-confidence", "0.5", "--fallback", "x", "--print-questions"},
			files:    map[string]string{"two.yaml": "a:\n  ask: which\n  pick: [x, y]\nb:\n  ask: which too\n  pick: [x, y]\n"},
			stdin:    "s",
			wantCode: cli.ExitUsage,
			contains: []string{
				"onesie: --min-confidence applies to one question, and 2 were asked: 'a', 'b'. " +
					"Put it after --for ID to set one, as in --for a --min-confidence 0.5",
			},
		},
		{
			name:     "should name --for for --threshold beside a file of several questions",
			args:     []string{"-f", "two.yaml", "--threshold", "0.5", "--print-questions"},
			files:    map[string]string{"two.yaml": "a: is it a\nb: is it b\n"},
			stdin:    "s",
			wantCode: cli.ExitUsage,
			contains: []string{"Put it after --for ID to set one, as in --for a --threshold 0.5"},
		},
		{
			name:     "should name --for for a shape flag beside a file of several questions",
			args:     []string{"-f", "two.yaml", "--pick", "x,y", "--print-request"},
			files:    map[string]string{"two.yaml": "a: is it a\nb: is it b\n"},
			stdin:    "s",
			wantCode: cli.ExitUsage,
			contains: []string{"--pick applies to one question, and 2 were asked: 'a', 'b'. No question from the " +
				"file takes it, so put it after the --ask it belongs to, as in --ask ID=QUESTION --pick x,y"},
		},
		{
			name:     "should refuse a question file holding a second document",
			args:     []string{"-f", "md.yaml"},
			files:    map[string]string{"md.yaml": "a: q1\n---\nb: q2\n"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"a question file is one document"},
		},
		{
			name:  "should load a question file holding a separator inside a block scalar",
			args:  []string{"-f", "block.yaml", "-o", "json"},
			files: map[string]string{"block.yaml": "a:\n  ask: |-\n    one\n    ---\n    two\n"},
			stdin: "the server is down",
			response: `{"model":"onesie-1.13.0","answers":{"a":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"instructions":"one\n---\ntwo"`},
		},
		{
			name: "should merge a top level desc into the file's yes/no criteria",
			args: []string{"-f", "y.yaml", "--desc", "yes=CLI YES", "-o", "json"},
			files: map[string]string{
				"y.yaml": "urgent:\n  ask: q\n  yes_means: FILE YES\n  no_means: FILE NO\n",
			},
			stdin: "the server is down",
			response: `{"model":"onesie-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"true":"CLI YES"`, `"false":"FILE NO"`},
		},
		{
			name: "should refuse a shape flag aimed at a request body",
			args: []string{"-f", "body.json", "--pick", "x,y"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"noul","instructions":"q"}}}`,
			},
			wantCode: cli.ExitUsage,
			contains: []string{"--pick cannot reshape a request body's question"},
		},
		{
			name: "should keep a body's null score criteria null on the wire",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"score","instructions":"q","criteria":[null,null]}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"bq":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"Calm","1":"Frustrated"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":[null,null]`},
			absent:   []string{`"criteria":["",""]`},
		},
		{
			name: "should keep a question file's score criteria in file order",
			args: []string{"-f", "q.yaml", "-o", "json"},
			files: map[string]string{
				"q.yaml": "mood:\n  ask: how is it\n  rate:\n    urgent: sky falling\n" +
					"    normal: a normal day\n    cold: nothing happening\n",
			},
			stdin: "the server is down",
			response: `{"model":"onesie-1.13.0","answers":{"mood":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"a","1":"b","2":"c"},` +
				`"probabilities":{"0":0.1,"1":0.1,"2":0.8}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":["sky falling","a normal day","nothing happening"]`,
			},
		},
		{
			name:     "should refuse replace with no file to override",
			args:     []string{"--replace", "is this urgent"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"onesie: --replace applies to -f, which was not given"},
		},
		{
			name: "should refuse a structured fallback in a question file",
			args: []string{"-f", "policy.yaml"},
			files: map[string]string{
				"policy.yaml": "team:\n  ask: q\n  pick: [a, b]\n" +
					"  min_confidence: 0.7\n  fallback:\n    x: 1\n",
			},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"'fallback' in question 'team' must be a string"},
			absent:   []string{`"decision"`},
		},
		{
			name: "should stay quiet about a replayed body's partly described criteria",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"choice","instructions":"q",` +
					`"criteria":{"x":"X","y":null}}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"bq":{"type":"choice",` +
				`"choice":"x","confidence":0.91,"probabilities":{"x":0.91,"y":0.09}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			absent:   []string{"warning:"},
		},
		{
			name: "should replay a body's mixed score criteria without a rubric complaint",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"score","instructions":"q","criteria":["x",null]}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"bq":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"x","1":"y"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":["x",null]`},
		},
		{
			name: "should show the legend beside the index in the table",
			args: []string{"-f", "body.json", "-o", "table"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"score","instructions":"q","criteria":[null,null]}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"bq":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"Calm","1":"Frustrated"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			contains: []string{"0 Calm", "1 Frustrated"},
		},
		{
			name: "should keep flag choice criteria in the order they were given",
			args: []string{
				"--ask", "team=who owns this", "--pick", "zebra,mike,alpha", "-o", "json",
			},
			stdin: "the server is down",
			response: `{"model":"onesie-1.13.0","answers":{"team":{"type":"choice",` +
				`"choice":"zebra","confidence":0.9,` +
				`"probabilities":{"zebra":0.9,"mike":0.05,"alpha":0.05}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"criteria":{"zebra":null,"mike":null,"alpha":null}`},
		},
		{
			name: "should keep a structured body choice criterion in written order",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"choice","instructions":"q",` +
					`"criteria":{"zebra":{"what":"z","examples":["zz"]},"alpha":"a"}}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"bq":{"type":"choice",` +
				`"choice":"zebra","confidence":0.9,` +
				`"probabilities":{"zebra":0.9,"alpha":0.1}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":{"zebra":{"what":"z","examples":["zz"]},"alpha":"a"}`,
			},
		},
		{
			name: "should keep a structured body noul rubric in written order",
			args: []string{"-f", "body.json", "-o", "json"},
			files: map[string]string{
				"body.json": `{"state":"x","questions":` +
					`{"bq":{"type":"noul","instructions":"q",` +
					`"criteria":{"true":{"what":"y","examples":["yy"]},"false":"n"}}}}`,
			},
			response: `{"model":"onesie-1.13.0","answers":{"bq":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":{"true":{"what":"y","examples":["yy"]},"false":"n"}`,
			},
		},
		{
			name: "should keep a question file's structured rubric in file order",
			args: []string{"-f", "q.yaml", "-o", "json"},
			files: map[string]string{
				"q.yaml": "urgent:\n  ask: q\n  yes_means:\n    what: y\n" +
					"    examples: [yy]\n  no_means: n\n",
			},
			stdin: "the server is down",
			response: `{"model":"onesie-1.13.0","answers":{"urgent":{"type":"noul","noul":0.92}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":{"true":{"what":"y","examples":["yy"]},"false":"n"}`,
			},
		},
		{
			name: "should keep a question file's structured level description in file order",
			args: []string{"-f", "q.yaml", "-o", "json"},
			files: map[string]string{
				"q.yaml": "mood:\n  ask: how is it\n  rate:\n    low:\n      what: z\n" +
					"      examples: [zz]\n    high: h\n",
			},
			stdin: "the server is down",
			response: `{"model":"onesie-1.13.0","answers":{"mood":{"type":"score","score":1.0,` +
				`"confidence":0.92,"legend":{"0":"a","1":"b"},` +
				`"probabilities":{"0":0.1,"1":0.9}}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends: []string{
				`"criteria":[{"what":"z","examples":["zz"]},"h"]`,
			},
		},
		{
			name: "should keep question ids in the order they were given",
			args: []string{
				"--ask", "zebra=one", "--ask", "mike=two", "--ask", "alpha=three", "-o", "json",
			},
			stdin: "the server is down",
			response: `{"model":"onesie-1.13.0","answers":{"zebra":{"type":"noul","noul":0.1},` +
				`"mike":{"type":"noul","noul":0.2},"alpha":{"type":"noul","noul":0.3}},` +
				`"usage":{"input_tokens":10,"output_tokens":2}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"questions":{"zebra":`, `"mike":`, `"alpha":`},
		},
		{
			name: "should keep structured instructions in file order",
			args: []string{"-f", "q.yaml", "-o", "json"},
			files: map[string]string{
				"q.yaml": "urgent:\n  ask:\n    zebra: z\n    mike: m\n    alpha: a\n",
			},
			stdin:    "the server is down",
			response: `{"model":"onesie-1.13.0","answers":{"urgent":{"type":"noul","noul":0.3}}}`,
			wantCode: cli.ExitOK,
			sends:    []string{`"instructions":{"zebra":"z","mike":"m","alpha":"a"}`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var sent []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					t.Errorf("reading the request body: %v", readErr)
				}

				sent = body

				status := tc.status
				if status == 0 {
					status = http.StatusOK
				}

				w.WriteHeader(status)

				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-01-01"},
				cli.WithKeychain(offKeychain()),
				cli.WithClientFactory(func(_ context.Context, opts ...onesie.Option) (*onesie.Client, error) {
					return onesie.New(append([]onesie.Option{
						onesie.WithAPIKey("k"), onesie.WithBaseURL(srv.URL), onesie.WithClock(&recordingClock{}),
					}, opts...)...)
				}),
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(tc.tty),
				cli.WithArgs(tc.args),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
				cli.WithReadFile(func(name string) ([]byte, error) {
					body, ok := tc.files[name]
					if !ok {
						return nil, fmt.Errorf("open %s: no such file or directory", name)
					}

					return []byte(body), nil
				}),
			)

			root.Command.SetOut(&out)
			root.Command.SetErr(&out)

			code := root.Execute(t.Context())

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\noutput:\n%s", code, tc.wantCode, out.String())
			}

			for _, want := range tc.contains {
				actual, expected := out.String(), want
				if len(tc.args) > 0 && tc.args[0] == "help" {
					actual = strings.Join(strings.Fields(actual), " ")
					expected = strings.Join(strings.Fields(expected), " ")
				}
				if !strings.Contains(actual, expected) {
					t.Errorf("output missing %q\ngot:\n%s", want, out.String())
				}
			}

			for _, unwanted := range tc.absent {
				if strings.Contains(out.String(), unwanted) {
					t.Errorf("output should not contain %q\ngot:\n%s", unwanted, out.String())
				}
			}

			for _, want := range tc.sends {
				if !strings.Contains(string(sent), want) {
					t.Errorf("request missing %q\nsent:\n%s", want, sent)
				}
			}
		})
	}

	t.Run("should report a flag clash before what the run would open", func(t *testing.T) {
		t.Parallel()

		const clash = "onesie: -o table reads one record. Drop -i jsonl or use -o json\n"

		tests := []struct {
			name   string
			config func(t *testing.T, dir string) []string
		}{
			{name: "should report a flag clash before a locked --out", config: func(t *testing.T, dir string) []string {
				t.Helper()

				path := filepath.Join(dir, "answers.jsonl")

				held, _, err := answersfile.Open(answersfile.Options{}, answersfile.Config{Path: path})
				if err != nil {
					t.Fatalf("holding the lock: %v", err)
				}

				t.Cleanup(func() {
					if err := held.Release(); err != nil {
						t.Errorf("releasing the held lock: %v", err)
					}
				})

				return []string{"--out", path}
			}},
			{name: "should report a flag clash before --out names a directory", config: func(t *testing.T, dir string) []string {
				t.Helper()

				return []string{"--out", t.TempDir()}
			}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				args := append([]string{"is this urgent", "-i", "jsonl", "-o", "table"}, tc.config(t, dir)...)

				out, code := runWithEnv(t, map[string]string{"ONESIE_CONFIG_DIR": dir, "TYPESAFE_API_KEY": "k"}, args)
				if code != cli.ExitUsage || out != clash {
					t.Errorf("exit %d, output %q, want exit %d and only %q", code, out, cli.ExitUsage, clash)
				}
			})
		}
	})

	t.Run("should report a broken or exposed profiles.json before a flag clash", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			mode     os.FileMode
			stored   string
			unixOnly bool
			wantCode int
			want     string
		}{
			{
				name: "should report an exposed profiles.json first", mode: 0o644, unixOnly: true, wantCode: cli.ExitAuth,
				stored: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"k"}}}`,
				want:   "is readable or writable by its group or others, mode 644",
			},
			{
				name: "should report a broken profiles.json first", mode: 0o600, stored: "{", wantCode: cli.ExitUsage,
				want: "is not valid JSON",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				if tc.unixOnly && runtime.GOOS == "windows" {
					t.Skip("windows has no group or other mode bits to expose the file through")
				}

				dir := t.TempDir()
				path := filepath.Join(dir, "profiles.json")

				if err := os.WriteFile(path, []byte(tc.stored), tc.mode); err != nil {
					t.Fatalf("writing profiles.json: %v", err)
				}

				// A umask may trim the mode WriteFile asks for.
				if err := os.Chmod(path, tc.mode); err != nil {
					t.Fatalf("setting the mode of profiles.json: %v", err)
				}

				out, code := runWithEnv(t, map[string]string{"ONESIE_CONFIG_DIR": dir, "TYPESAFE_API_KEY": "k"},
					[]string{"is this urgent", "-i", "jsonl", "-o", "table"})
				if code != tc.wantCode || !strings.Contains(out, tc.want) || strings.Contains(out, "reads one record") {
					t.Errorf("exit %d, output %q, want exit %d naming %q and no clash", code, out, tc.wantCode, tc.want)
				}
			})
		}
	})

	t.Run("should bind --assert to no question group", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			args []string
		}{
			{
				name: "should not bind an assertion to the group it sits between",
				args: []string{
					"-o", "json", "--ask", "a=first", "--assert", "a.value > 0.5",
					"--ask", "b=second",
				},
			},
			{
				name: "should accept an assertion over a group opened after it",
				args: []string{
					"-o", "json", "--ask", "a=first", "--assert", "b.value > 0.5",
					"--ask", "b=second",
				},
			},
			{
				name: "should accept an assertion before the first group",
				args: []string{
					"-o", "json", "--assert", "a.value > 0.5", "--ask", "a=first",
					"--ask", "b=second",
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, _ := runAsserted(t, tc.args, 0, both, cli.ExitOK)

				if errOut != "" {
					t.Errorf("stderr = %q, want nothing", errOut)
				}

				for _, want := range []string{`"a":{"value":0.9}`, `"b":{"value":0.9}`} {
					if !strings.Contains(out, want) {
						t.Errorf("stdout missing %q\ngot:\n%s", want, out)
					}
				}
			})
		}
	})

	t.Run("should refuse a bad timing flag before any request", func(t *testing.T) {
		t.Parallel()

		const answered = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.92}},` +
			`"usage":{"input_tokens":10,"output_tokens":2}}`

		tests := []struct {
			name     string
			args     []string
			code     int
			contains string
			requests int64
		}{
			{
				name:     "should refuse a timeout of zero",
				args:     []string{"--timeout", "0"},
				code:     cli.ExitUsage,
				contains: "onesie: --timeout takes a positive number of seconds, got 0",
			},
			{
				name:     "should refuse a timeout above the ceiling",
				args:     []string{"--timeout", "86401"},
				code:     cli.ExitUsage,
				contains: "onesie: --timeout takes at most 86400 seconds, got 86401",
			},
			{
				name:     "should refuse a timeout that would overflow a duration",
				args:     []string{"--timeout", "18446744074"},
				code:     cli.ExitUsage,
				contains: "onesie: --timeout takes at most 86400 seconds, got 18446744074",
			},
			{
				name:     "should refuse a negative retry count",
				args:     []string{"--retries=-1"},
				code:     cli.ExitUsage,
				contains: "onesie: --retries takes a retry count of zero or more, got -1",
			},
			{
				name:     "should refuse a negative retry after cap",
				args:     []string{"--max-retry-after=-5"},
				code:     cli.ExitUsage,
				contains: "onesie: --max-retry-after must be a positive number of seconds, got -5",
			},
			{
				name:     "should refuse a retry after cap that would overflow a duration",
				args:     []string{"--max-retry-after", "18446744074"},
				code:     cli.ExitUsage,
				contains: "onesie: --max-retry-after takes at most 86400 seconds, got 18446744074",
			},
			{
				name:     "should refuse a job count of zero",
				args:     []string{"-j", "0"},
				code:     cli.ExitUsage,
				contains: "onesie: -j takes a positive number of records in flight, got 0",
			},
			{
				name:     "should print the warnings when validation fails",
				args:     []string{"--pick", "a,b", "--desc", "a=first", "-q"},
				code:     cli.ExitUsage,
				contains: "onesie: warning: the question describes a but not b",
			},
			{
				name: "should reach the request with every flag in range",
				args: []string{
					"--timeout", "86400", "--retries", "0", "--max-retry-after", "86400", "-j", "1",
				},
				code:     cli.ExitOK,
				requests: 1,
			},
			{
				name:     "should accept the long spelling of --jobs",
				args:     []string{"--input", "lines", "--jobs", "2"},
				code:     cli.ExitOK,
				requests: 1,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var requests atomic.Int64

				srv := httptest.NewServer(http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.Header().Set("Content-Type", "application/json")

						if _, err := io.WriteString(w, answered); err != nil {
							t.Errorf("writing the response: %v", err)
						}
					}))
				defer srv.Close()

				args := append([]string{
					"is this urgent", "--base-url", srv.URL,
				}, tc.args...)

				var out bytes.Buffer

				// No WithClientFactory, since these flags reach the client through the real factory
				// and the point of the test is the plumbing a stub would replace.
				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader("a ticket")),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(apiKeyEnv("test")),
				)

				root.Command.SetOut(&out)
				root.Command.SetErr(&out)
				root.Command.SetArgs(args)

				if code := root.Execute(t.Context()); code != tc.code {
					t.Errorf("exit code = %d, want %d. output:\n%s", code, tc.code, out.String())
				}

				if tc.contains != "" && !strings.Contains(out.String(), tc.contains) {
					t.Errorf("output = %q, want it to contain %q", out.String(), tc.contains)
				}

				// A refused flag must be caught before anything is sent, which is what lets the
				// error cases run without a reachable server at all.
				if got := requests.Load(); got != tc.requests {
					t.Errorf("requests = %d, want %d", got, tc.requests)
				}
			})
		}
	})

	t.Run("should refuse an answer that does not fit the question on every run path", func(t *testing.T) {
		t.Parallel()

		const usage = `"usage":{"input_tokens":10,"output_tokens":2}`

		noul := []string{"is this urgent"}
		pick := []string{"who handles this", "--pick", "billing,technical"}

		tests := []struct {
			name     string
			question []string
			answer   string
			gate     []string
			want     string
		}{
			{
				name: "should refuse an answer that carries only its type", question: noul,
				answer: `{"type":"noul"}`, gate: []string{"-q"},
				want: "response could not be used",
			},
			{
				name: "should refuse an answer of the wrong type", question: noul,
				answer: `{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":1}}`, gate: []string{"-q"},
				want: "response could not be used: question 'answer' expects a noul answer, got choice",
			},
			{
				name: "should refuse a choice that is not an option", question: pick,
				answer: `{"type":"choice","choice":"sales","confidence":1,"probabilities":{"billing":0.5,"technical":0.5}}`,
				gate:   []string{"--assert", `answer.value != "billing"`},
				want:   "response could not be used: question 'answer' picked 'sales', which is not one of its options: billing, technical",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var requests atomic.Int64

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")

					if _, err := io.WriteString(w, `{"model":"jev-1.14.0","answers":{"answer":`+tc.answer+`},`+usage+`}`); err != nil {
						t.Errorf("writing the stub response: %v", err)
					}
				}))
				t.Cleanup(srv.Close)

				env := map[string]string{
					"ONESIE_CONFIG_DIR":            t.TempDir(),
					"ONESIE_CACHE_DIR":             filepath.Join(t.TempDir(), "cache"),
					onesie.TypeSafe().EnvAPIKey():  "SECRET-TEST",
					onesie.TypeSafe().EnvBaseURL(): srv.URL,
				}

				run := func(stdin string, args ...string) (string, string, int) {
					var out, errOut bytes.Buffer

					root := cli.NewRootCmd(
						cli.BuildInfo{Version: "1.2.3"},
						cli.WithKeychain(offKeychain()),
						cli.WithStdin(strings.NewReader(stdin)),
						cli.WithStdinTTY(false),
						cli.WithStdoutTTY(false),
						cli.WithLookupEnv(func(name string) (string, bool) {
							value, ok := env[name]

							return value, ok
						}),
					)

					root.Command.SetOut(&out)
					root.Command.SetErr(&errOut)
					root.Command.SetArgs(append(slices.Clone(tc.question), append(args, "--retries", "0")...))

					code := root.Execute(t.Context())

					return out.String(), errOut.String(), code
				}

				_, errOut, code := run("", "--state", "x", "--stats")
				if code != cli.ExitUnavailable || !strings.Contains(errOut, tc.want) {
					t.Errorf("plain run = %d, stderr %q, want %d and %q", code, errOut, cli.ExitUnavailable, tc.want)
				}

				if !strings.Contains(errOut, "10 in / 2 out tokens") {
					t.Errorf("plain run stderr = %q, want the usage counted", errOut)
				}

				if _, gateErr, gateCode := run("", append([]string{"--state", "x"}, tc.gate...)...); gateCode != cli.ExitUnavailable {
					t.Errorf("gate = %d, want %d\nstderr:\n%s", gateCode, cli.ExitUnavailable, gateErr)
				}

				out, errOut, code := run("a\n", "-i", "lines", "-o", "json", "--usage")
				if code != cli.ExitRecords || !strings.Contains(out, `"kind":"response"`) || !strings.Contains(out, tc.want) ||
					!strings.Contains(out, `"input_tokens":10`) {
					t.Errorf("stream = %d, stdout %q, stderr %q, want %d and an error line with its usage", code, out, errOut,
						cli.ExitRecords)
				}

				before := requests.Load()

				for range 2 {
					if _, cacheErr, cacheCode := run("", "--state", "x", "--cache"); cacheCode != cli.ExitUnavailable {
						t.Errorf("cached run = %d, want %d\nstderr:\n%s", cacheCode, cli.ExitUnavailable, cacheErr)
					}
				}

				if got := requests.Load() - before; got != 2 {
					t.Errorf("two cached runs made %d requests, want 2, since nothing unusable is stored", got)
				}
			})
		}
	})

	t.Run("should print the help of every command as pinned", func(t *testing.T) {
		t.Parallel()

		for _, pin := range []struct {
			args   []string
			golden string
		}{
			{args: []string{"--help"}, golden: "root.golden"},
			{args: []string{"calibrate", "--help"}, golden: "calibrate.golden"},
			{args: []string{"help", "asking"}, golden: "root-asking.golden"},
			{args: []string{"help", "input"}, golden: "root-input.golden"},
			{args: []string{"help", "decisions"}, golden: "root-decisions.golden"},
			{args: []string{"help", "output"}, golden: "root-output.golden"},
			{args: []string{"help", "connections"}, golden: "root-connections.golden"},
			{args: []string{"help", "execution"}, golden: "root-execution.golden"},
			{args: []string{"help", "exit-codes"}, golden: "root-exit-codes.golden"},
			{args: []string{"help", "calibrate", "asking"}, golden: "calibrate-asking.golden"},
			{args: []string{"help", "calibrate", "input"}, golden: "calibrate-input.golden"},
			{args: []string{"help", "calibrate", "evaluation"}, golden: "calibrate-evaluation.golden"},
			{args: []string{"help", "calibrate", "output"}, golden: "calibrate-output.golden"},
			{args: []string{"help", "calibrate", "connections"}, golden: "calibrate-connections.golden"},
			{args: []string{"help", "calibrate", "execution"}, golden: "calibrate-execution.golden"},
			{args: []string{"help", "calibrate", "exit-codes"}, golden: "calibrate-exit-codes.golden"},
			{args: []string{"version", "--help"}, golden: "version.golden"},
			{args: []string{"profile", "--help"}, golden: "profile.golden"},
			{args: []string{"profile", "create", "--help"}, golden: "profile-create.golden"},
			{args: []string{"profile", "update", "--help"}, golden: "profile-update.golden"},
			{args: []string{"profile", "delete", "--help"}, golden: "profile-delete.golden"},
			{args: []string{"profile", "use", "--help"}, golden: "profile-use.golden"},
			{args: []string{"profile", "list", "--help"}, golden: "profile-list.golden"},
			{args: []string{"profile", "show", "--help"}, golden: "profile-show.golden"},
			{args: []string{"profile", "test", "--help"}, golden: "profile-test.golden"},
			{args: []string{"questions", "--help"}, golden: "questions.golden"},
			{args: []string{"cache", "--help"}, golden: "cache.golden"},
			{args: []string{"cache", "clear", "--help"}, golden: "cache-clear.golden"},
		} {
			var out bytes.Buffer

			root := cli.NewRootCmd(cli.BuildInfo{Version: "1.2.3"},
				cli.WithStdin(strings.NewReader("")), cli.WithStdinTTY(false))
			root.Command.SetOut(&out)
			root.Command.SetErr(&out)
			root.Command.SetArgs(pin.args)

			if code := root.Execute(t.Context()); code != cli.ExitOK {
				t.Fatalf("onesie %s exit code = %d, output:\n%s", strings.Join(pin.args, " "), code, out.String())
			}

			path := filepath.Join("testdata", "help", pin.golden)
			if *updateHelp {
				if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if out.String() != string(want) {
				t.Errorf("onesie %s differs from testdata/help/%s, rerun with -update-help for a change made on purpose\ngot:\n%s",
					strings.Join(pin.args, " "), pin.golden, out.String())
			}
		}
	})

	t.Run("should print help all as its header and each topic page without the more help footer", func(t *testing.T) {
		t.Parallel()

		for _, pin := range []struct {
			args   []string
			header string
			pages  []string
		}{
			{
				args: []string{"help", "all"},
				header: "Ask questions about text and use the answers in scripts.\n\n" +
					"Usage:\n  onesie [question] [flags]\n  onesie [command]\n\n" +
					"Available Commands:\n" +
					"  cache       Report what the response cache holds\n" +
					"  calibrate   Score questions against records whose answers are known\n" +
					"  completion  Generate the autocompletion script for the specified shell\n" +
					"  help        Help about any command\n" +
					"  profile     Create, inspect and remove the named connections a run uses\n" +
					"  questions   List the question files -f finds by name\n" +
					"  version     Print version, commit, build date and the built in limits\n\n",
				pages: []string{
					"root-asking", "root-input", "root-decisions", "root-output", "root-connections",
					"root-execution", "root-exit-codes",
				},
			},
			{
				args: []string{"help", "calibrate", "all"},
				header: "Compare answers with labelled examples. Calibrate never chooses a threshold.\n\n" +
					"Usage:\n  onesie calibrate [question] [flags]\n\n",
				pages: []string{
					"calibrate-asking", "calibrate-input", "calibrate-evaluation", "calibrate-output",
					"calibrate-connections", "calibrate-execution", "calibrate-exit-codes",
				},
			},
		} {
			bodies := make([]string, 0, len(pin.pages))

			for _, page := range pin.pages {
				golden, err := os.ReadFile(filepath.Join("testdata", "help", page+".golden"))
				if err != nil {
					t.Fatal(err)
				}

				body, _, _ := strings.Cut(string(golden), "\n\nMore help:\n")
				bodies = append(bodies, strings.TrimSuffix(body, "\n"))
			}

			var out bytes.Buffer

			root := cli.NewRootCmd(cli.BuildInfo{Version: "1.2.3"},
				cli.WithStdin(strings.NewReader("")), cli.WithStdinTTY(false))
			root.Command.SetOut(&out)
			root.Command.SetErr(&out)
			root.Command.SetArgs(pin.args)

			if code := root.Execute(t.Context()); code != cli.ExitOK {
				t.Fatalf("onesie %s exit code = %d, output:\n%s", strings.Join(pin.args, " "), code, out.String())
			}

			if want := pin.header + strings.Join(bodies, "\n\n") + "\n"; out.String() != want {
				t.Errorf("onesie %s =\n%s\nwant\n%s", strings.Join(pin.args, " "), out.String(), want)
			}
		}
	})

	t.Run("should print one JSON object for version -o json and version --output json", func(t *testing.T) {
		t.Parallel()

		limited := map[string]string{}
		for _, entry := range limits.Report() {
			limited[entry.Name] = entry.Value
		}

		for _, args := range [][]string{{"version", "-o", "json"}, {"version", "--output", "json"}} {
			var out bytes.Buffer

			root := cli.NewRootCmd(cli.BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-10-27"},
				cli.WithStdin(strings.NewReader("")), cli.WithStdinTTY(false))
			root.Command.SetOut(&out)
			root.Command.SetErr(&out)
			root.Command.SetArgs(args)

			if code := root.Execute(t.Context()); code != cli.ExitOK {
				t.Fatalf("onesie %s exit code = %d, output:\n%s", strings.Join(args, " "), code, out.String())
			}

			var got struct {
				Version, Commit, Date, Go, Platform string
				Limits                              map[string]string
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil || strings.Count(out.String(), "\n") != 1 {
				t.Fatalf("onesie %s = %q, want one JSON line: %v", strings.Join(args, " "), out.String(), err)
			}

			if got.Version != "1.2.3" || got.Commit != "abc1234" || got.Date != "2026-10-27" ||
				got.Go != runtime.Version() || got.Platform != runtime.GOOS+"/"+runtime.GOARCH ||
				!maps.Equal(got.Limits, limited) {
				t.Errorf("onesie %s = %+v, want the build info and every limit", strings.Join(args, " "), got)
			}
		}
	})

	t.Run("should refuse an output mode other than json on version", func(t *testing.T) {
		t.Parallel()

		for args, want := range map[string]string{
			"version -o csv":     "onesie: version prints text, or JSON under -o json, so -o csv does not apply\n",
			"version --output x": "onesie: version prints text, or JSON under -o json, so -o x does not apply\n",
		} {
			var out, errOut bytes.Buffer

			root := cli.NewRootCmd(cli.BuildInfo{Version: "1.2.3"},
				cli.WithStdin(strings.NewReader("")), cli.WithStdinTTY(false))
			root.Command.SetOut(&out)
			root.Command.SetErr(&errOut)
			root.Command.SetArgs(strings.Fields(args))

			if code := root.Execute(t.Context()); code != cli.ExitUsage || errOut.String() != want || out.Len() != 0 {
				t.Errorf("onesie %s = exit %d, stdout %q, stderr %q, want exit 2 and %q", args, code, out.String(), errOut.String(), want)
			}
		}
	})

	t.Run("should print the build facts and the limits as limits.Report lists them, in one aligned block", func(t *testing.T) {
		t.Parallel()

		rows := [][2]string{
			{"version", "1.2.3"},
			{"commit", "abc1234"},
			{"built", "2026-10-27"},
			{"go", runtime.Version()},
			{"platform", runtime.GOOS + "/" + runtime.GOARCH},
		}
		for _, entry := range limits.Report() {
			rows = append(rows, [2]string{entry.Name, entry.Value})
		}

		var out bytes.Buffer

		root := cli.NewRootCmd(cli.BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-10-27"},
			cli.WithStdin(strings.NewReader("")), cli.WithStdinTTY(false))
		root.Command.SetOut(&out)
		root.Command.SetErr(&out)
		root.Command.SetArgs([]string{"version"})

		if code := root.Execute(t.Context()); code != cli.ExitOK {
			t.Fatalf("onesie version exit code = %d, output:\n%s", code, out.String())
		}

		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != len(rows) {
			t.Fatalf("onesie version printed %d lines, want %d:\n%s", len(lines), len(rows), out.String())
		}

		column := strings.Index(lines[0], rows[0][1])
		for i, line := range lines {
			name, value, found := strings.Cut(line, " ")
			if !found || name != rows[i][0] || strings.TrimLeft(value, " ") != rows[i][1] || strings.Index(line, rows[i][1]) != column {
				t.Errorf("onesie version line %d = %q, want %s and %s with the value at column %d", i+1, line, rows[i][0], rows[i][1], column)
			}
		}
	})

	t.Run("should keep the root's defaults after the subcommands register theirs", func(t *testing.T) {
		t.Parallel()

		root := cli.NewRootCmd(cli.BuildInfo{Version: "1.2.3"},
			cli.WithStdin(strings.NewReader("")), cli.WithStdinTTY(false))

		calibrate, _, err := root.Command.Find([]string{"calibrate"})
		if err != nil || calibrate == root.Command {
			t.Fatalf("finding calibrate: %v", err)
		}

		shared := 0

		calibrate.LocalFlags().VisitAll(func(flag *pflag.Flag) {
			rootFlag := root.Command.Flags().Lookup(flag.Name)
			if rootFlag == nil {
				return
			}

			shared++

			if got := rootFlag.Value.String(); got != rootFlag.DefValue {
				t.Errorf("--%s = %q after the tree is built, want the root's default %q",
					flag.Name, got, rootFlag.DefValue)
			}
		})

		if shared == 0 {
			t.Fatal("calibrate shares no flag with the root, want the stream flags")
		}
	})

	t.Run("should print the question file schema for --print-schema", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			args     []string
			env      map[string]string
			wantCode int
			stdout   string
			stderr   string
		}{
			{
				name:     "should print exactly the schema and exit 0",
				args:     []string{"--print-schema"},
				wantCode: cli.ExitOK,
				stdout:   string(qfile.Schema()),
			},
			{
				name:     "should still print the schema beside a bad provider",
				args:     []string{"--print-schema"},
				env:      map[string]string{"ONESIE_PROVIDER": "bogus"},
				wantCode: cli.ExitOK,
				stdout:   string(qfile.Schema()),
			},
			{
				name:     "should refuse a question beside it",
				args:     []string{"--print-schema", "is this urgent"},
				wantCode: cli.ExitUsage,
				stderr:   "onesie: --print-schema asks no question. Drop the question argument\n",
			},
			{
				name:     "should refuse another flag beside it",
				args:     []string{"--print-schema", "-o", "json"},
				wantCode: cli.ExitUsage,
				stderr:   "onesie: --print-schema takes no other flag. Drop --output\n",
			},
			{
				name:     "should refuse a group flag beside it",
				args:     []string{"--ask", "urgent=is this urgent", "--print-schema"},
				wantCode: cli.ExitUsage,
				stderr:   "onesie: --print-schema takes no other flag. Drop --ask\n",
			},
			{
				name:     "should not hint for --print-schema, which takes no question",
				args:     []string{"version", "--print-schema"},
				wantCode: cli.ExitUsage,
				stderr:   "onesie: version has no flag --print-schema\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var out, errOut bytes.Buffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader("")),
					cli.WithLookupEnv(func(name string) (string, bool) {
						value, found := tc.env[name]

						return value, found
					}),
				)

				root.Command.SetOut(&out)
				root.Command.SetErr(&errOut)
				root.Command.SetArgs(tc.args)

				if code := root.Execute(t.Context()); code != tc.wantCode {
					t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
				}

				if out.String() != tc.stdout {
					t.Errorf("stdout = %q, want %q", out.String(), tc.stdout)
				}

				if errOut.String() != tc.stderr {
					t.Errorf("stderr = %q, want %q", errOut.String(), tc.stderr)
				}
			})
		}
	})

	t.Run("should name a file it cannot read once", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		missing := filepath.Join(dir, "missing.txt")
		env := map[string]string{"ONESIE_CONFIG_DIR": dir, "TYPESAFE_API_KEY": "SECRET-key"}

		tests := []struct {
			name string
			args []string
		}{
			{name: "should name a missing -f once", args: []string{"-f", missing, "--print-request"}},
			{name: "should name a missing @FILE once", args: []string{"--ask", "a=@" + missing, "--print-request"}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, code := runWithEnv(t, env, tc.args)
				if code != cli.ExitUsage {
					t.Errorf("exit code = %d, want %d\noutput:\n%s", code, cli.ExitUsage, out)
				}

				if !strings.Contains(out, "reading "+missing+": ") || strings.Contains(out, "open "+missing) {
					t.Errorf("output = %q, want %s named once", out, missing)
				}
			})
		}
	})

	t.Run("should refuse --profile and --provider on every command that reads no profile, before or after it", func(t *testing.T) {
		t.Parallel()

		both := []string{"--profile", "--provider"}
		commands := []struct {
			path, args, flags []string
		}{
			{path: []string{"cache"}, flags: both},
			{path: []string{"cache", "clear"}, flags: both},
			{path: []string{"version"}, flags: both},
			{path: []string{"questions"}, flags: both},
			{path: []string{"completion", "bash"}, flags: both},
			{path: []string{"help"}, flags: both},
			{path: []string{"profile", "delete"}, args: []string{"work"}, flags: both},
			{path: []string{"profile", "use"}, args: []string{"work"}, flags: both},
			{path: []string{"profile", "list"}, flags: both},
			// Both take a separate --provider, the provider the profile stores.
			{path: []string{"profile", "create"}, args: []string{"work"}, flags: []string{"--profile"}},
			{path: []string{"profile", "update"}, args: []string{"work"}, flags: []string{"--profile"}},
		}

		for _, command := range commands {
			typed := append(append([]string{}, command.path...), command.args...)

			for _, flag := range command.flags {
				for _, args := range [][]string{
					append(append([]string{}, typed...), flag, "typesafe"),
					append([]string{flag, "typesafe"}, typed...),
				} {
					var out, errOut bytes.Buffer

					root := cli.NewRootCmd(
						cli.BuildInfo{Version: "1.2.3"},
						cli.WithKeychain(offKeychain()),
						cli.WithStdin(strings.NewReader("")),
						cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
					)

					root.Command.SetOut(&out)
					root.Command.SetErr(&errOut)
					root.Command.SetArgs(args)

					name := strings.Join(command.path, " ")
					want := fmt.Sprintf("onesie: %s has no flag %s. %s belongs to a run, not to %s\n", name, flag, flag, name)
					if code := root.Execute(t.Context()); code != cli.ExitUsage || errOut.String() != want {
						t.Errorf("onesie %s: exit %d, stderr %q, want 2 and %q", strings.Join(args, " "), code, errOut.String(), want)
					}
				}
			}
		}
	})

	t.Run("should read no ONESIE_PROFILE or ONESIE_PROVIDER on a command that reads no profile", func(t *testing.T) {
		t.Parallel()

		var out, errOut bytes.Buffer

		env := map[string]string{"ONESIE_PROFILE": "missing", "ONESIE_PROVIDER": "nonsense"}
		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithKeychain(offKeychain()),
			cli.WithStdin(strings.NewReader("")),
			cli.WithLookupEnv(func(name string) (string, bool) { value, found := env[name]; return value, found }),
		)

		root.Command.SetOut(&out)
		root.Command.SetErr(&errOut)
		root.Command.SetArgs([]string{"version"})

		if code := root.Execute(t.Context()); code != cli.ExitOK || errOut.Len() != 0 {
			t.Errorf("exit %d, stderr %q, want 0 and nothing", code, errOut.String())
		}
	})

	t.Run("should hint that a subcommand refused a flag the root knows", func(t *testing.T) {
		t.Parallel()

		const hint = "belongs to a run"

		tests := []struct {
			name   string
			args   []string
			want   string
			absent bool
		}{
			{
				name:   "should refuse --state on calibrate with its own reason in place of the hint",
				args:   []string{"calibrate", "--state", "x"},
				want:   "onesie: calibrate reads each state from its input through --map, so --state does not apply",
				absent: true,
			},
			{
				name: "should hint for a shorthand the root knows",
				args: []string{"version", "-q"},
				want: "--quiet belongs to a run, not to version\n",
			},
			{
				name:   "should not hint for -V, which no command knows",
				args:   []string{"version", "-V"},
				want:   "onesie: version has no flag -V\n",
				absent: true,
			},
			{
				name:   "should not hint for --version, which no command knows",
				args:   []string{"calibrate", "--version"},
				want:   "onesie: calibrate has no flag --version\n",
				absent: true,
			},
			{
				name:   "should not hint for --list-models, which takes no question",
				args:   []string{"calibrate", "--list-models"},
				want:   "onesie: calibrate has no flag --list-models\n",
				absent: true,
			},
			{
				name:   "should not hint for a flag the root does not know either",
				args:   []string{"version", "--bogus"},
				want:   "onesie: version has no flag --bogus\n",
				absent: true,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var out, errOut bytes.Buffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader("")),
					cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
				)

				root.Command.SetOut(&out)
				root.Command.SetErr(&errOut)
				root.Command.SetArgs(tc.args)

				if code := root.Execute(t.Context()); code != cli.ExitUsage {
					t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, cli.ExitUsage, errOut.String())
				}

				if !strings.Contains(errOut.String(), tc.want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.want)
				}

				if tc.absent && strings.Contains(errOut.String(), hint) {
					t.Errorf("stderr = %q, want no subcommand hint", errOut.String())
				}
			})
		}
	})

	t.Run("should refuse --cache where nothing goes through the cache", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := answered
			if strings.HasSuffix(r.URL.Path, "/models") {
				body = `{"models":[{"id":"jev-1.13.0"}]}`
			}

			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)

		const request = `{"state":"x","questions":{"answer":{"type":"noul","instructions":"is it"}}}` + "\n"

		tests := []struct {
			name     string
			args     []string
			env      map[string]string
			wantCode int
			want     string
		}{
			{name: "should ignore ONESIE_CACHE=1 beside -i request", args: []string{"-i", "request"}, env: map[string]string{"ONESIE_CACHE": "1"}},
			{name: "should ignore ONESIE_CACHE=1 beside --list-models", args: []string{"--list-models"}, env: map[string]string{"ONESIE_CACHE": "1"}},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				home := t.TempDir()
				env := map[string]string{
					onesie.TypeSafe().EnvAPIKey(): "k",
					"ONESIE_CONFIG_DIR":           filepath.Join(home, "config"),
					"ONESIE_CACHE_DIR":            filepath.Join(home, "cache"),
				}

				for name, value := range tc.env {
					env[name] = value
				}

				var out, errOut bytes.Buffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader(request)),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(func(name string) (string, bool) {
						value, ok := env[name]

						return value, ok
					}),
				)

				root.Command.SetOut(&out)
				root.Command.SetErr(&errOut)
				root.Command.SetArgs(append(tc.args, "--base-url", srv.URL))

				if code := root.Execute(t.Context()); code != tc.wantCode {
					t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
				}

				if tc.want != "" && !strings.Contains(errOut.String(), tc.want) {
					t.Errorf("stderr = %q, want %q", errOut.String(), tc.want)
				}
			})
		}
	})
}

func TestClientBuilder(t *testing.T) {
	t.Parallel()

	t.Run("should exit 2 naming CLOUDFLARE_ACCOUNT_ID when cloudflare has a token and no account", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer

		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithKeychain(offKeychain()),
			cli.WithStdin(strings.NewReader("body")),
			cli.WithStdinTTY(false),
			cli.WithStdoutTTY(false),
			cli.WithLookupEnv(func(name string) (string, bool) {
				value, ok := map[string]string{"CLOUDFLARE_API_TOKEN": "t", "ONESIE_PROVIDER": "cloudflare"}[name]

				return value, ok
			}),
		)

		root.Command.SetOut(&out)
		root.Command.SetErr(&out)
		root.Command.SetArgs([]string{"is this urgent"})

		if code := root.Execute(t.Context()); code != cli.ExitUsage || !strings.Contains(out.String(), "CLOUDFLARE_ACCOUNT_ID") {
			t.Fatalf("exit code = %d, output:\n%s\nwant 2 naming CLOUDFLARE_ACCOUNT_ID", code, out.String())
		}
	})

	t.Run("should build a client from the flags", func(t *testing.T) {
		t.Parallel()

		t.Run("should build a client from the environment key and --base-url", func(t *testing.T) {
			t.Parallel()

			var gotAuth string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")

				if _, err := w.Write([]byte(
					`{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`,
				)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3"},
				cli.WithKeychain(offKeychain()),
				cli.WithStdin(strings.NewReader("body")),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(apiKeyEnv("test")),
			)

			root.Command.SetOut(&out)
			root.Command.SetErr(&out)
			root.Command.SetArgs([]string{
				"is this urgent", "-o", "raw", "--base-url", srv.URL,
			})

			if code := root.Execute(t.Context()); code != cli.ExitOK {
				t.Fatalf("exit code = %d, output:\n%s", code, out.String())
			}

			if !strings.Contains(gotAuth, "test") {
				t.Errorf("Authorization header = %q, want it to carry the environment key", gotAuth)
			}
		})
	})

	t.Run("should not warn under --allow-http for https, this machine or a refused base url", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			baseURL string
			refused bool
		}{
			{
				name:    "should refuse rather than warn for a base url with credentials",
				baseURL: "http://user:SECRET-pw@onesie.invalid/v1", refused: true,
			},
			{name: "should not warn for https", baseURL: "https://127.0.0.1:1"},
			{name: "should not warn for localhost", baseURL: "http://localhost:1"},
			{name: "should not warn for a loopback address", baseURL: "http://127.0.0.1:1"},
			{name: "should not warn for the ipv6 loopback", baseURL: "http://[::1]:1"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var out, errOut bytes.Buffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader("")),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(apiKeyEnv("test")),
				)

				root.Command.SetOut(&out)
				root.Command.SetErr(&errOut)
				root.Command.SetArgs([]string{
					"--list-models", "--base-url", tc.baseURL, "--allow-http", "--retries", "0",
					"--timeout", "1",
				})

				root.Execute(t.Context())

				if strings.Contains(errOut.String(), "SECRET-") {
					t.Error("stderr carries the base URL's password")
				}

				if refusal := "must not hold @, ? or #"; tc.refused != strings.Contains(errOut.String(), refusal) {
					t.Errorf("stderr = %q, want the refusal %v", errOut.String(), tc.refused)
				}

				if strings.Contains(errOut.String(), "warning") {
					t.Errorf("stderr = %q, want no warning", errOut.String())
				}
			})
		}
	})

	t.Run("should warn once when the base URL ends in /v1", func(t *testing.T) {
		t.Parallel()

		const answered = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`

		tests := []struct {
			name     string
			flag     string
			env      string
			stored   string
			spelled  string
			wantWarn bool
		}{
			{name: "should warn for --base-url ending in /v1", flag: "/v1", spelled: "--base-url", wantWarn: true},
			{name: "should warn for --base-url ending in /v1/", flag: "/v1/", spelled: "--base-url", wantWarn: true},
			{name: "should warn for TYPESAFE_BASE_URL ending in /v1", env: "/v1", spelled: onesie.TypeSafe().EnvBaseURL(), wantWarn: true},
			{
				name: "should name the profile's base_url when it ends in /v1", stored: "/v1",
				spelled: "the profile's base_url", wantWarn: true,
			},
			{name: "should not warn for a proxy prefix that is not /v1", flag: "/proxy"},
			{name: "should not warn for a bare host", flag: "/"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var paths sync.Map

				srv := stubAnswering(t, answered, func(r *http.Request) {
					paths.Store(r.URL.Path, true)
				})
				defer srv.Close()

				config := t.TempDir()
				env := map[string]string{onesie.TypeSafe().EnvAPIKey(): "test", "ONESIE_CONFIG_DIR": config}
				args := []string{"is this urgent", "-i", "lines", "-o", "values", "--retries", "0"}

				if tc.stored != "" {
					delete(env, onesie.TypeSafe().EnvAPIKey())

					stored := `{"default":"proxy","profiles":{"proxy":{"provider":"typesafe","api_key":"test",` +
						`"base_url":"` + srv.URL + tc.stored + `"}}}`
					if err := os.WriteFile(filepath.Join(config, "profiles.json"), []byte(stored), 0o600); err != nil {
						t.Fatalf("writing the profiles file: %v", err)
					}
				}

				if tc.flag != "" {
					args = append(args, "--base-url", srv.URL+tc.flag)
				}

				if tc.env != "" {
					env[onesie.TypeSafe().EnvBaseURL()] = srv.URL + tc.env
				}

				var out, errOut bytes.Buffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader("one\ntwo\n")),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(func(name string) (string, bool) {
						value, found := env[name]

						return value, found
					}),
				)

				root.Command.SetOut(&out)
				root.Command.SetErr(&errOut)
				root.Command.SetArgs(args)

				root.Execute(t.Context())

				want := "onesie: warning: " + tc.spelled + " ends in /v1 and onesie adds the API path itself, " +
					"so questions go to " + srv.URL + "/v1/v1/systemone\n"
				if !tc.wantWarn {
					if strings.Contains(errOut.String(), "ends in /v1") {
						t.Errorf("stderr = %q, want no /v1 warning", errOut.String())
					}

					return
				}

				if got := strings.Count(errOut.String(), want); got != 1 {
					t.Errorf("stderr = %q, want the warning %q once", errOut.String(), want)
				}

				if _, sent := paths.Load("/v1/v1/systemone"); !sent {
					t.Errorf("the stub saw no request on /v1/v1/systemone, so the run was refused rather than warned")
				}
			})
		}
	})

	t.Run("should build a client from the environment", func(t *testing.T) {
		t.Parallel()

		const answered = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`

		t.Run("should let --base-url override the environment", func(t *testing.T) {
			t.Parallel()

			var wanted, unwanted int

			flagged := stubAnswering(t, answered, func(*http.Request) { wanted++ })
			defer flagged.Close()

			fromEnv := stubAnswering(t, answered, func(*http.Request) { unwanted++ })
			defer fromEnv.Close()

			env := map[string]string{
				onesie.TypeSafe().EnvAPIKey():  "from-env",
				onesie.TypeSafe().EnvBaseURL(): fromEnv.URL,
			}

			out, code := runWithEnv(t, env,
				[]string{"is this urgent", "-o", "raw", "--base-url", flagged.URL})

			if code != cli.ExitOK {
				t.Fatalf("exit code = %d, output:\n%s", code, out)
			}

			if wanted != 1 {
				t.Errorf("the flag's base url took %d requests, want 1", wanted)
			}

			if unwanted != 0 {
				t.Errorf("the environment's base url took %d requests, want 0", unwanted)
			}
		})
	})

	t.Run("should wire the retry flags onto the client", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			args     []string
			attempts int
		}{
			{
				name:     "should make one attempt with retries disabled",
				args:     []string{"--retries", "0"},
				attempts: 1,
			},
			{
				name:     "should make three attempts by default",
				args:     []string{},
				attempts: 3,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var attempts atomic.Int64

				clock := &recordingClock{}

				srv := httptest.NewServer(http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) {
						attempts.Add(1)
						// 503 is retryable per DefaultRetryStatus, and Retry-After is omitted so the
						// run uses onesie's own backoff rather than a server requested wait.
						w.WriteHeader(http.StatusServiceUnavailable)
					}))
				defer srv.Close()

				args := append([]string{
					"is this urgent", "--base-url", srv.URL,
				}, tc.args...)

				var out bytes.Buffer

				// No WithClientFactory, since the flags are wired onto the client by the real factory
				// and a stub one would never see them.
				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader("the server is down")),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(apiKeyEnv("test")),
					cli.WithClock(clock),
				)

				root.Command.SetOut(&out)
				root.Command.SetErr(&out)
				root.Command.SetArgs(args)

				if code := root.Execute(t.Context()); code == cli.ExitOK {
					t.Fatalf("exit code = %d, want a failure. output:\n%s", code, out.String())
				}

				if got := attempts.Load(); got != int64(tc.attempts) {
					t.Errorf("attempts = %d, want %d", got, tc.attempts)
				}

				if waits := clock.waits(); len(waits) != tc.attempts-1 {
					t.Errorf("waits = %v, want one before each of the %d retries", waits, tc.attempts-1)
				}
			})
		}
	})

	t.Run("should wire --max-retry-after onto the client", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			args     []string
			attempts int
			waits    []time.Duration
		}{
			{
				name:     "should stop at the first attempt when Retry-After is above the cap",
				args:     []string{"--retries", "1", "--max-retry-after", "1"},
				attempts: 1,
			},
			{
				name:     "should retry when Retry-After is below the default cap",
				args:     []string{"--retries", "1"},
				attempts: 2,
				waits:    []time.Duration{2 * time.Second},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var attempts atomic.Int64

				clock := &recordingClock{}

				srv := httptest.NewServer(http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) {
						attempts.Add(1)
						// Two seconds, so the run is terminal under a cap of one and retryable under
						// the default of sixty.
						w.Header().Set("Retry-After", "2")
						w.WriteHeader(http.StatusServiceUnavailable)
					}))
				defer srv.Close()

				args := append([]string{
					"is this urgent", "--base-url", srv.URL,
				}, tc.args...)

				var out bytes.Buffer

				// No WithClientFactory, since the flag reaches the client through the real factory and
				// a stub one would never see it.
				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain()),
					cli.WithStdin(strings.NewReader("the server is down")),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(apiKeyEnv("test")),
					cli.WithClock(clock),
				)

				root.Command.SetOut(&out)
				root.Command.SetErr(&out)
				root.Command.SetArgs(args)

				if code := root.Execute(t.Context()); code == cli.ExitOK {
					t.Fatalf("exit code = %d, want a failure. output:\n%s", code, out.String())
				}

				if got := attempts.Load(); got != int64(tc.attempts) {
					t.Errorf("attempts = %d, want %d", got, tc.attempts)
				}

				// Jitter shortens each wait by up to BackoffJitter of it, so a wait is checked as a band.
				waits := clock.waits()
				if len(waits) != len(tc.waits) {
					t.Fatalf("waits = %v, want %v less up to the jitter", waits, tc.waits)
				}

				for i, want := range tc.waits {
					shortest := time.Duration(float64(want) * (1 - onesie.DefaultRetryPolicy().BackoffJitter))
					if waits[i] > want || waits[i] < shortest {
						t.Errorf("waits = %v, want %v less up to the jitter", waits, tc.waits)
					}
				}
			})
		}
	})
}

func runWithEnv(t *testing.T, env map[string]string, args []string) (string, int) {
	t.Helper()

	var out bytes.Buffer

	// No WithClientFactory, so the real factory runs and the injected lookup is the only thing
	// standing between it and the process environment.
	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithKeychain(offKeychain()),
		cli.WithStdin(strings.NewReader("body")),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithLookupEnv(func(name string) (string, bool) {
			value, ok := env[name]

			return value, ok
		}),
	)

	root.Command.SetOut(&out)
	root.Command.SetErr(&out)
	root.Command.SetArgs(args)

	code := root.Execute(t.Context())

	return out.String(), code
}

func (c *recordingClock) Now() time.Time {
	return time.Unix(0, 0)
}

func (c *recordingClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.slept = append(c.slept, d)

	return ctx.Err()
}

func (c *recordingClock) waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.slept)
}

type recordingClock struct {
	mu    sync.Mutex
	slept []time.Duration
}

func apiKeyEnv(key string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if name == onesie.TypeSafe().EnvAPIKey() {
			return key, true
		}

		return "", false
	}
}

func stubAnswering(t *testing.T, body string, observe func(*http.Request)) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observe(r)

		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("writing stub response: %v", err)
		}
	}))
}

func offKeychain() *creds.Keychain {
	return creds.NewKeychain(creds.WithBackend(offBackend{}))
}

type offBackend struct{}

func (offBackend) Get(string, string) (string, error) {
	return "", errors.New("the live suite never uses the keychain")
}

func (offBackend) Set(string, string, string) error {
	return errors.New("the live suite never uses the keychain")
}

func (offBackend) Delete(string, string) error {
	return nil
}
