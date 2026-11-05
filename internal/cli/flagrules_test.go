package cli

import (
	"cmp"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestValidatePlan(t *testing.T) {
	t.Parallel()

	readFile := func(string) ([]byte, error) { return nil, nil }

	tests := []struct {
		name        string
		events      []argv.Event
		file        []plan.Question
		positional  string
		flags       runFlags
		given       []string
		wantErr     string
		wantExact   string
		wantWarning string
	}{
		{
			name:    "should name only --assert for -q on a box",
			events:  []argv.Event{{Name: "ask", Value: "mug=x"}, {Name: "box", Value: "true"}},
			flags:   runFlags{quiet: true, jobs: 1},
			wantErr: "-q on 'mug' needs --assert. A box has no policy that could fail it",
		},
		{
			name: "should refuse a question named like a box column under -o csv",
			events: []argv.Event{
				{Name: "ask", Value: "mug=x"}, {Name: "box", Value: "true"}, {Name: "ask", Value: "mug_left=y"},
			},
			flags:   runFlags{output: "csv", jobs: 1},
			wantErr: "question 'mug_left' takes a column name box question 'mug' writes. Rename one",
		},
		{
			name:    "should reject an empty invocation",
			wantErr: "no question given. Pass a question, --ask, or -f",
		},
		{
			name: "should reject a question named like another question's csv fallback column",
			events: []argv.Event{
				{Name: "ask", Value: "a=q"},
				{Name: "fallback", Value: "no"},
				{Name: "ask", Value: "a_fallback=q2"},
			},
			flags:     runFlags{output: "csv", input: "text"},
			wantExact: "question 'a_fallback' has the name of the fallback column -o csv writes for 'a'",
		},
		{
			name: "should accept a question named like a fallback column outside csv and tsv",
			events: []argv.Event{
				{Name: "ask", Value: "a=q"},
				{Name: "fallback", Value: "no"},
				{Name: "ask", Value: "a_fallback=q2"},
			},
			flags: runFlags{output: "json", input: "text"},
		},
		{
			name: "should reject raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			flags:   runFlags{raw: true},
			wantErr: "-r needs a single question. 'a', 'b' were asked",
		},
		{
			name: "should reject -o raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			flags:   runFlags{output: "raw"},
			wantErr: "-o raw needs a single question. 'a', 'b' were asked",
		},
		{
			name:       "should accept -o raw with a single question",
			positional: "is this urgent",
			flags:      runFlags{output: "raw"},
		},
		{
			name: "should reject quiet on a pick with no policy",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
			},
			flags: runFlags{quiet: true},
			wantErr: "-q on 'team' needs --min-confidence and --fallback, or --assert. " +
				"Without one the exit code is always 0",
		},
		{
			name: "should accept quiet on a pick carrying only an assertion",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
			},
			flags: runFlags{assert: []string{"answer.value > 0.5"}, quiet: true},
		},
		{
			name:       "should reject raw combined with output",
			positional: "is this urgent",
			flags:      runFlags{raw: true, output: "json"},
			wantErr:    "-r and -o are mutually exclusive",
		},
		{
			name:       "should reject both state flags together",
			positional: "is this urgent",
			given:      []string{flagState, flagStateFile},
			wantErr:    "--state and --state-file are mutually exclusive",
		},
		{
			name:       "should reject replace with no file given",
			positional: "is this urgent",
			flags:      runFlags{replace: true},
			wantErr:    "--replace applies to -f, which was not given",
		},
		{
			name: "should accept replace alongside a file",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile},
			},
			flags: runFlags{replace: true, file: "triage.yaml"},
		},
		{
			name:  "should name the file's keys in the quiet policy remedy",
			file:  filePick("billing", "technical"),
			flags: runFlags{quiet: true},
			wantExact: "-q on 'team' needs 'min_confidence' and 'fallback', or --assert. " +
				"Without one the exit code is always 0",
		},
		{
			name:       "should reject unordered outside a stream",
			positional: "is this urgent",
			flags:      runFlags{unordered: true, input: "text"},
			wantErr:    "--unordered applies to streaming input. -i text reads one record",
		},
		{
			name:       "should reject -i beside --image with no state flag",
			positional: "is this urgent",
			flags:      runFlags{images: []string{"photo=photo.png"}, input: "json"},
			given:      []string{flagInput},
			wantErr:    "-i json holds one record, which beside --image reads stdin only through --state -. Pass --state - to read the state from stdin",
		},
		{
			name:       "should accept -i beside --image under --state",
			positional: "is this urgent",
			flags:      runFlags{images: []string{"photo=photo.png"}, input: "json"},
			given:      []string{flagInput, flagState},
		},
		{
			name:       "should reject stop on error outside a stream",
			positional: "is this urgent",
			flags:      runFlags{stopOnError: true, input: "json"},
			wantErr:    "--stop-on-error applies to streaming input. -i json reads one record",
		},
		{
			name:       "should reject stop on assert outside a stream",
			positional: "is this urgent",
			flags:      runFlags{stopOnAssert: true, input: "text"},
			wantErr:    "--stop-on-assert applies to streaming input. -i text reads one record",
		},
		{
			name:       "should accept stop on assert in a gated stream",
			positional: "is this urgent",
			flags:      runFlags{assert: []string{"answer.value > 0.5"}, stopOnAssert: true, input: "jsonl"},
		},
		{
			name:       "should reject skip blank outside a stream",
			positional: "is this urgent",
			flags:      runFlags{skipBlank: true, input: "text"},
			wantErr:    "--skip-blank applies to streaming input",
		},
		{
			name:       "should reject no dedup outside a stream",
			positional: "is this urgent",
			flags:      runFlags{noDedup: true, input: "text"},
			wantErr:    "--no-dedup applies to streaming input. -i text reads one record",
		},
		{
			name:       "should accept no dedup in a stream",
			positional: "is this urgent",
			flags:      runFlags{noDedup: true, input: "lines"},
		},
		{
			name:       "should accept unordered in a stream",
			positional: "is this urgent",
			flags:      runFlags{unordered: true, input: "lines"},
		},
		{
			name:       "should reject state with a streaming mode",
			positional: "is this urgent",
			flags:      runFlags{input: "jsonl"},
			given:      []string{flagState},
			wantErr:    "--state cannot be combined with -i jsonl",
		},
		{
			name:       "should reject state-file with a streaming mode naming the flag used",
			positional: "is this urgent",
			flags:      runFlags{input: "lines"},
			given:      []string{flagStateFile},
			wantErr:    "--state-file cannot be combined with -i lines",
		},
		{
			name:       "should reject merge with raw output",
			positional: "is this urgent",
			flags:      runFlags{merge: true, output: "raw", input: "lines"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should reject merge with table output outside a stream too",
			positional: "is this urgent",
			flags:      runFlags{merge: true, output: "table", input: "text"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should name --merge-key when that is the flag given",
			positional: "is this urgent",
			flags:      runFlags{mergeKey: "verdict", output: "table", input: "text"},
			wantErr:    "--merge-key needs -o json, values, csv or tsv",
		},
		{
			name:       "should reject merge with the -r spelling of raw",
			positional: "is this urgent",
			flags:      runFlags{merge: true, raw: true, input: "text"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should accept merge with values output",
			positional: "is this urgent",
			flags:      runFlags{merge: true, output: "values", input: "jsonl"},
		},
		{
			name:       "should accept merge with an explicit auto output",
			positional: "is this urgent",
			flags:      runFlags{merge: true, output: "auto", input: "jsonl"},
		},
		{
			name:       "should accept merge with json output",
			positional: "is this urgent",
			flags:      runFlags{merge: true, output: "json", input: "jsonl"},
		},
		{
			name:       "should accept merge with an absent output",
			positional: "is this urgent",
			flags:      runFlags{merge: true, input: "jsonl"},
		},
		{
			name:       "should reject quiet in a stream",
			positional: "is this urgent",
			flags:      runFlags{quiet: true, input: "jsonl"},
			wantErr:    "-q reads one record. Drop -i jsonl",
		},
		{
			name:       "should reject a table in a stream",
			positional: "is this urgent",
			flags:      runFlags{output: "table", input: "lines"},
			wantErr:    "-o table reads one record. Drop -i lines or use -o json",
		},
		{
			name:       "should accept a table outside a stream",
			positional: "is this urgent",
			flags:      runFlags{output: "table", input: "text"},
		},
		{
			name:        "should warn about jobs outside a stream",
			positional:  "is this urgent",
			flags:       runFlags{jobs: 8, input: "text"},
			given:       []string{flagJobs},
			wantWarning: "-j 8 ignored. -i text reads one record",
		},
		{
			name:       "should report the warnings alongside a fatal error",
			positional: "which team",
			events: []argv.Event{
				{Name: "pick", Value: "billing"},
			},
			flags:       runFlags{jobs: 8, input: "text"},
			given:       []string{flagJobs},
			wantErr:     "--pick needs at least two options, got 1: billing",
			wantWarning: "-j 8 ignored. -i text reads one record",
		},
		{
			name:       "should reject a zero job count",
			positional: "is this urgent",
			flags:      runFlags{jobs: 0, input: "lines"},
			given:      []string{flagJobs},
			wantErr:    "-j takes a positive number",
		},
		{
			name:       "should reject a negative job count",
			positional: "is this urgent",
			flags:      runFlags{jobs: -2, input: "lines"},
			given:      []string{flagJobs},
			wantErr:    "-j takes a positive number",
		},
		{
			name:       "should accept a config that never set jobs",
			positional: "is this urgent",
			flags:      runFlags{input: "lines"},
		},
		{
			name:       "should reject a timeout of zero",
			positional: "is this urgent",
			flags:      runFlags{timeout: 0, input: "text"},
			given:      []string{flagTimeout},
			wantErr:    "--timeout takes a positive number of seconds, got 0",
		},
		{
			name:       "should reject a negative retry count",
			positional: "is this urgent",
			flags:      runFlags{retries: -1, input: "text"},
			given:      []string{flagRetries},
			wantErr:    "--retries takes a retry count of zero or more, got -1",
		},
		{
			name:       "should reject a retry count above the ceiling",
			positional: "is this urgent",
			flags:      runFlags{retries: 100001, input: "text"},
			given:      []string{flagRetries},
			wantErr:    "--retries takes at most 100, got 100001",
		},
		{
			name:       "should accept a retry count at the ceiling",
			positional: "is this urgent",
			flags:      runFlags{retries: 100, input: "text"},
			given:      []string{flagRetries},
		},
		{
			name:       "should reject a max retry after of zero",
			positional: "is this urgent",
			flags:      runFlags{maxRetryAfter: 0, input: "text"},
			given:      []string{flagMaxRetryAfter},
			wantErr:    "--max-retry-after must be a positive number of seconds, got 0",
		},
		{
			name:       "should reject a negative max retry after",
			positional: "is this urgent",
			flags:      runFlags{maxRetryAfter: -5, input: "text"},
			given:      []string{flagMaxRetryAfter},
			wantErr:    "--max-retry-after must be a positive number of seconds, got -5",
		},
		{
			name:       "should reject a timeout above the ceiling",
			positional: "is this urgent",
			flags:      runFlags{timeout: 86401, input: "text"},
			given:      []string{flagTimeout},
			wantErr:    "--timeout takes at most 86400 seconds, got 86401",
		},
		{
			name:       "should reject a timeout that would wrap a duration to a fraction",
			positional: "is this urgent",
			flags:      runFlags{timeout: 18446744074, input: "text"},
			given:      []string{flagTimeout},
			wantErr:    "--timeout takes at most 86400 seconds, got 18446744074",
		},
		{
			name:       "should reject a max retry after above the ceiling",
			positional: "is this urgent",
			flags:      runFlags{maxRetryAfter: 86401, input: "text"},
			given:      []string{flagMaxRetryAfter},
			wantErr:    "--max-retry-after takes at most 86400 seconds, got 86401",
		},
		{
			name:       "should reject a max retry after that would wrap a duration to a fraction",
			positional: "is this urgent",
			flags:      runFlags{maxRetryAfter: 18446744074, input: "text"},
			given:      []string{flagMaxRetryAfter},
			wantErr:    "--max-retry-after takes at most 86400 seconds, got 18446744074",
		},
		{
			name:       "should accept a timeout at the ceiling",
			positional: "is this urgent",
			flags:      runFlags{timeout: 86400, input: "text"},
			given:      []string{flagTimeout},
		},
		{
			name:       "should accept a retry count of zero",
			positional: "is this urgent",
			flags:      runFlags{retries: 0, input: "text"},
			given:      []string{flagRetries},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			built, err := plan.Assemble(plan.Source{
				Events:     tc.events,
				File:       tc.file,
				Positional: tc.positional,
				ReadFile:   readFile,
			})
			if err != nil {
				t.Fatalf("assembling: %v", err)
			}

			config := testConfig(tc.flags, tc.given, tc.events, tc.positional)

			warnings, err := validatePlan(built, config, runFacts{})
			assertWarning(t, warnings, tc.wantWarning, tc.wantErr != "" || tc.wantExact != "")

			switch {
			case tc.wantExact != "":
				if err == nil || err.Error() != tc.wantExact {
					t.Errorf("error = %v\nwant %q", err, tc.wantExact)
				}
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v\nwant it to contain %q", err, tc.wantErr)
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCheckFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		flags      runFlags
		given      []string
		events     []argv.Event
		positional string
		facts      runFacts
		wantErr    string
	}{
		{
			name:    "should refuse --image-field on one record",
			flags:   runFlags{input: "text", imageFields: []string{"photo"}, jobs: 1},
			wantErr: "--image-field applies to streaming input. -i text reads one record",
		},
		{
			name:    "should refuse a second --image-field under -i lines",
			flags:   runFlags{input: "lines", imageFields: []string{"a", "b"}, jobs: 1},
			wantErr: "-i lines takes one --image-field, since a line holds one path",
		},
		{
			name:    "should reject -o csv with --merge over jsonl",
			flags:   runFlags{merge: true, input: "jsonl", output: "csv", jobs: 1},
			wantErr: "-o csv with --merge needs -i csv or -i tsv, since other input has no fixed columns",
		},
		{
			name:  "should accept -o tsv with --merge over csv",
			flags: runFlags{merge: true, input: "csv", output: "tsv", jobs: 1},
		},
		{
			name:    "should reject --merge-key with -o csv",
			flags:   runFlags{mergeKey: "verdict", input: "csv", output: "csv", jobs: 1},
			wantErr: "--merge-key does not apply to -o csv, which puts the answers in columns",
		},
		{
			name:    "should reject --merge-key with --merge and -o csv, which --merge would otherwise hide",
			flags:   runFlags{merge: true, mergeKey: "verdict", input: "csv", output: "csv", jobs: 1},
			wantErr: "--merge-key does not apply to -o csv, which puts the answers in columns",
		},
		{
			name:    "should reject --merge-key with --merge and -o tsv",
			flags:   runFlags{merge: true, mergeKey: "verdict", input: "tsv", output: "tsv", jobs: 1},
			wantErr: "--merge-key does not apply to -o tsv, which puts the answers in columns",
		},
		{
			name:    "should refuse --merge-key state under -i text",
			flags:   runFlags{mergeKey: "state", input: "text", jobs: 1},
			wantErr: "--merge-key state does not apply to -i text, which wraps every record under a state key",
		},
		{
			name:    "should refuse --merge-key state under -i lines",
			flags:   runFlags{mergeKey: "state", input: "lines", jobs: 1},
			wantErr: "--merge-key state does not apply to -i lines, which wraps every record under a state key",
		},
		{
			name:  "should accept --merge-key state under -i jsonl",
			flags: runFlags{mergeKey: "state", input: "jsonl", jobs: 1},
		},
		{
			name:    "should reject --usage with -q",
			flags:   runFlags{input: "text", quiet: true, usage: true, jobs: 1},
			wantErr: "--usage does not apply to -q, which suppresses output",
		},
		{
			name:    "should reject --usage with -o tsv",
			flags:   runFlags{input: "csv", output: "tsv", usage: true, jobs: 1},
			wantErr: "--usage does not apply to -o tsv, which has no column for it",
		},
		{
			name:    "should reject --usage with -o values",
			flags:   runFlags{input: "text", output: "values", usage: true, jobs: 1},
			wantErr: "--usage does not apply to -o values, which writes only the answers. Use -o json",
		},
		{
			name:    "should reject --usage with -o raw",
			flags:   runFlags{input: "jsonl", output: "raw", usage: true, jobs: 1},
			wantErr: "--usage does not apply to -o raw, which writes only the answers. Use -o json",
		},
		{
			name:    "should reject --usage with -r",
			flags:   runFlags{input: "text", raw: true, usage: true, jobs: 1},
			wantErr: "--usage does not apply to -r, which writes only the answers. Use -o json",
		},
		{
			name:  "should accept --usage with -o json",
			flags: runFlags{input: "text", output: "json", usage: true, jobs: 1},
		},
		{
			name:    "should reject -j above the cap",
			flags:   runFlags{input: "jsonl", jobs: 2000000},
			given:   []string{flagJobs},
			wantErr: "-j takes at most 256 records in flight, got 2000000",
		},
		{
			name:    "should reject --resume without --out",
			flags:   runFlags{input: "jsonl", resume: true},
			wantErr: "--resume needs --out, the file it picks up from",
		},
		{
			name:    "should reject --resume on a single record",
			flags:   runFlags{input: "text", resume: true, out: "answers.txt"},
			wantErr: "--resume applies to streaming input. -i text reads one record",
		},
		{
			name:    "should reject --merge with -o markdown",
			flags:   runFlags{merge: true, input: "jsonl", output: "markdown", jobs: 1},
			wantErr: "--merge needs -o json, values, csv or tsv",
		},
		{
			name:    "should reject --merge-key with -o md",
			flags:   runFlags{mergeKey: "verdict", input: "jsonl", output: "md", jobs: 1},
			wantErr: "--merge-key needs -o json, values, csv or tsv",
		},
		{
			name:    "should reject -r with -o markdown",
			flags:   runFlags{input: "text", raw: true, output: "markdown", jobs: 1},
			wantErr: "-r and -o are mutually exclusive",
		},
		{
			name:    "should reject -q with -o markdown on one record",
			flags:   runFlags{input: "text", output: "markdown", quiet: true, jobs: 1},
			wantErr: "-q suppresses output, which leaves -o markdown nothing to write",
		},
		{
			name:    "should reject -q with -o md on a stream, naming the spelling given",
			flags:   runFlags{input: "jsonl", output: "md", quiet: true, jobs: 1},
			wantErr: "-q suppresses output, which leaves -o md nothing to write",
		},
		{
			name:  "should reject --resume with -o markdown",
			flags: runFlags{input: "jsonl", output: "markdown", resume: true, out: "a.md", jobs: 1},
			wantErr: "--resume reads each record's outcome back from --out, " +
				"which a markdown table does not keep. Use -o json, values, csv or tsv",
		},
		{
			name:  "should accept --out without --resume under -o markdown",
			flags: runFlags{input: "jsonl", output: "markdown", out: "a.md", jobs: 1},
		},
		{
			name:  "should accept --usage with -o markdown",
			flags: runFlags{input: "text", output: "markdown", usage: true, jobs: 1},
		},
		{
			name:    "should reject --resume with --unordered",
			flags:   runFlags{input: "jsonl", resume: true, out: "a.jsonl", unordered: true},
			wantErr: "--resume relies on input order, which --unordered gives up. Pass --id to resume by id",
		},
		{
			name:  "should accept --resume with --unordered when --id names the records",
			flags: runFlags{input: "jsonl", resume: true, out: "a.jsonl", unordered: true, jobs: 1},
			given: []string{flagID},
		},
		{
			name:    "should reject --resume with --id and -r, which writes no id",
			flags:   runFlags{input: "jsonl", resume: true, out: "a.txt", raw: true},
			given:   []string{flagID},
			wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
		},
		{
			name:    "should reject --resume with --id and -o raw, which writes no id",
			flags:   runFlags{input: "jsonl", resume: true, out: "a.txt", output: "raw"},
			given:   []string{flagID},
			wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
		},
		{
			name:    "should reject a resume by position with -r and --assert, since a raw line keeps no outcome",
			flags:   runFlags{assert: []string{"answer.value > 0.5"}, input: "jsonl", resume: true, out: "a.txt", raw: true},
			wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
		},
		{
			name:    "should reject a resume by position with -o raw and a file's assert",
			flags:   runFlags{input: "jsonl", resume: true, out: "a.txt", output: "raw"},
			facts:   runFacts{assert: "answer.value > 0.5"},
			wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
		},
		{
			name:    "should reject a resume by id with -o raw and --assert",
			flags:   runFlags{assert: []string{"answer.value > 0.5"}, input: "jsonl", resume: true, out: "a.txt", output: "raw"},
			given:   []string{flagID},
			wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
		},
		{
			name:    "should reject a resume by position with -o raw and no gate, since a raw line keeps no failure",
			flags:   runFlags{input: "jsonl", resume: true, out: "a.txt", output: "raw", jobs: 1},
			wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
		},
		{
			name:    "should reject a resume by position with -r and no gate",
			flags:   runFlags{input: "jsonl", resume: true, out: "a.txt", raw: true, jobs: 1},
			wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
		},
		{
			name:  "should accept a tsv resume by position under --stop-on-error, since the file is read at runtime",
			flags: runFlags{input: "jsonl", resume: true, out: "a.tsv", output: "tsv", stopOnError: true, jobs: 1},
		},
		{
			name:  "should accept a csv resume by id under --stop-on-error",
			flags: runFlags{input: "jsonl", resume: true, out: "a.csv", output: "csv", stopOnError: true, jobs: 1},
			given: []string{flagID},
		},
		{
			name:    "should reject --prune without --resume",
			flags:   runFlags{input: "jsonl", out: "a.jsonl", prune: true, jobs: 1},
			given:   []string{flagID},
			wantErr: "--prune applies to --resume, which was not given",
		},
		{
			name:    "should reject --prune without --id",
			flags:   runFlags{input: "jsonl", resume: true, out: "a.jsonl", prune: true, jobs: 1},
			wantErr: "--prune drops the answered ids the input no longer has, so it needs --id",
		},
		{
			name:  "should accept --prune with --resume and --id",
			flags: runFlags{input: "jsonl", resume: true, out: "a.jsonl", prune: true, jobs: 1},
			given: []string{flagID},
		},
		{
			name:    "should reject --resume with --print-request",
			flags:   runFlags{input: "jsonl", resume: true, out: "a.jsonl", printRequest: true, jobs: 1},
			given:   []string{flagID},
			wantErr: "--print-request writes request bodies, not answers, so --resume has nothing to pick up. Drop --resume",
		},
		{
			name:    "should reject --resume with --list-models",
			flags:   runFlags{listModels: true, input: "text", resume: true, out: "m.txt"},
			wantErr: "--resume applies to streaming input, which --list-models does not read",
		},
		{
			name:    "should reject --resume with --print-questions",
			flags:   runFlags{printQuestions: true, input: "text", resume: true, out: "q.yaml"},
			wantErr: "--resume applies to streaming input, which --print-questions does not read",
		},
		{
			name:    "should reject --id with -i text",
			flags:   runFlags{input: "text"},
			given:   []string{flagID},
			wantErr: "--id applies to streaming input. -i text reads one record",
		},
		{
			name:    "should reject --id with -i json",
			flags:   runFlags{input: "json"},
			given:   []string{flagID},
			wantErr: "--id applies to streaming input. -i json reads one record",
		},
		{
			name:  "should accept --id with streaming input",
			flags: runFlags{input: "jsonl", jobs: 1},
			given: []string{flagID},
		},
		{
			name:    "should reject -q with -i request",
			flags:   runFlags{input: "request", jobs: 1, quiet: true},
			wantErr: "-q needs a policy to report, which -i request has none of",
		},
		{
			name:    "should still reject -j 0 with -i request",
			flags:   runFlags{input: "request", jobs: 0},
			given:   []string{flagJobs},
			wantErr: "-j takes a positive number of records in flight, got 0",
		},
		{
			name:    "should reject -r with -o outside request mode",
			flags:   runFlags{raw: true, output: "json", input: "text"},
			wantErr: "-r and -o are mutually exclusive",
		},
		{
			name:    "should reject --abstain-if without an assertion",
			flags:   runFlags{abstainIf: []string{"answer.value > 0.4"}, input: "text"},
			wantErr: "--abstain-if needs --assert, since without one every record is a yes",
		},
		{
			name:    "should name the file's key when only the file carried the abstain expression",
			flags:   runFlags{file: "q.yaml", input: "text"},
			facts:   runFacts{abstainIf: "answer.value > 0.4"},
			wantErr: "'abstain_if' needs --assert, since without one every record is a yes",
		},
		{
			name:  "should accept --abstain-if when the only assertion comes from the file",
			flags: runFlags{abstainIf: []string{"answer.value > 0.4"}, file: "q.yaml", input: "text"},
			facts: runFacts{assert: "answer.value > 0.5"},
		},
		{
			name:    "should reject --stop-on-assert without an assertion",
			flags:   runFlags{stopOnAssert: true, input: "jsonl"},
			wantErr: "--stop-on-assert needs --assert, since without one no assertion is false",
		},
		{
			name:  "should accept --stop-on-assert when the only assertion comes from the file",
			flags: runFlags{stopOnAssert: true, file: "q.yaml", input: "jsonl"},
			facts: runFacts{assert: "answer.value > 0.5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warning, err := checkFlags(testConfig(tc.flags, tc.given, tc.events, tc.positional), tc.facts)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if warning != "" {
					t.Errorf("warning = %q, want none", warning)
				}

				return
			}

			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}

	t.Run("should refuse --delimiter where no csv reads or writes it", func(t *testing.T) {
		t.Parallel()

		const elsewhere = "--delimiter applies to -i csv and -o csv. "

		tests := []struct {
			name      string
			input     string
			output    string
			delimiter string
			wantErr   string
		}{
			{name: "should accept --delimiter with -i csv", input: "csv"},
			{name: "should accept --delimiter auto with -i csv -o csv", input: "csv", output: "csv", delimiter: "auto"},
			{name: "should accept --delimiter with -i jsonl -o csv", input: "jsonl", output: "csv"},
			{name: "should accept --delimiter with -i tsv -o csv", input: "tsv", output: "csv"},
			{name: "should accept --delimiter with -o csv from text", output: "csv"},
			{
				name: "should refuse --delimiter beside -i tsv", input: "tsv",
				wantErr: elsewhere + "-i tsv is tab separated by definition",
			},
			{name: "should refuse --delimiter beside -i jsonl", input: "jsonl", wantErr: elsewhere + "-i jsonl has no delimiter"},
			{name: "should refuse --delimiter beside -i json", input: "json", wantErr: elsewhere + "-i json has no delimiter"},
			{name: "should refuse --delimiter beside -i lines", input: "lines", wantErr: elsewhere + "-i lines has no delimiter"},
			{name: "should refuse --delimiter beside -i text", input: "text", wantErr: elsewhere + "-i text has no delimiter"},
			{
				name: "should refuse --delimiter beside -i request", input: "request",
				wantErr: elsewhere + "-i request has no delimiter",
			},
			{
				name: "should refuse --delimiter auto without -i csv", input: "jsonl", output: "csv", delimiter: "auto",
				wantErr: "--delimiter auto needs -i csv, since it reads the separator from the input's header row",
			},
			{name: "should accept --delimiter with -i csv -o tsv", input: "csv", output: "tsv"},
			{
				name: "should refuse --delimiter with -i jsonl -o tsv", input: "jsonl", output: "tsv",
				wantErr: elsewhere + "-i jsonl has no delimiter",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				flags := runFlags{input: tc.input, output: tc.output, delimiter: cmp.Or(tc.delimiter, ";")}

				_, err := checkFlags(testConfig(flags, []string{flagInput, flagDelimiter}, nil, ""), runFacts{})
				if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr) {
					t.Errorf("checkFlags error = %v, want %q", err, tc.wantErr)
				}
			})
		}
	})

	t.Run("should refuse each flag -i request does not take through a run", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "should reject a positional question with -i request",
				args:    []string{"-i", "request", "is this urgent"},
				wantErr: "onesie: -i request carries its own questions. Drop the question argument",
			},
			{
				name:    "should reject --ask with -i request",
				args:    []string{"-i", "request", "--ask", "urgent=is this urgent"},
				wantErr: "onesie: -i request carries its own questions. Drop --ask",
			},
			{
				name: "should reject -f with -i request before it is read",
				args: []string{"-i", "request", "-f", "nowhere.yaml"},
				wantErr: "onesie: -f does not apply to -i request, " +
					"which carries its own questions",
			},
			{
				name:    "should reject --replace with -i request",
				args:    []string{"-i", "request", "--replace"},
				wantErr: "onesie: --replace applies to -f, which -i request does not accept",
			},
			{
				name:    "should reject --pick with -i request",
				args:    []string{"-i", "request", "--pick", "a,b"},
				wantErr: "onesie: -i request carries its own questions. Drop --pick",
			},
			{
				name:    "should reject --rate with -i request",
				args:    []string{"-i", "request", "--rate", "low,high"},
				wantErr: "onesie: -i request carries its own questions. Drop --rate",
			},
			{
				name:    "should reject --desc with -i request",
				args:    []string{"-i", "request", "--desc", "a=first"},
				wantErr: "onesie: -i request carries its own questions. Drop --desc",
			},
			{
				name:    "should reject --sep with -i request",
				args:    []string{"-i", "request", "--sep", ";"},
				wantErr: "onesie: -i request carries its own questions. Drop --sep",
			},
			{
				name: "should reject --threshold with -i request",
				args: []string{"-i", "request", "--threshold", "0.5"},
				wantErr: "onesie: --threshold does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name: "should reject --min-confidence with -i request",
				args: []string{"-i", "request", "--min-confidence", "0.5"},
				wantErr: "onesie: --min-confidence does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name: "should reject --fallback with -i request",
				args: []string{"-i", "request", "--fallback", "maybe"},
				wantErr: "onesie: --fallback does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name: "should reject --state with -i request",
				args: []string{"-i", "request", "--state", "x"},
				wantErr: "onesie: --state does not apply to -i request, " +
					"whose bodies carry their own state",
			},
			{
				name: "should reject --state-file with -i request",
				args: []string{"-i", "request", "--state-file", "x"},
				wantErr: "onesie: --state-file does not apply to -i request, " +
					"whose bodies carry their own state",
			},
			{
				name:    "should reject -o with -i request",
				args:    []string{"-i", "request", "-o", "json"},
				wantErr: "onesie: -o does not apply to -i request, which forwards raw responses",
			},
			{
				name:    "should reject -r with -i request",
				args:    []string{"-i", "request", "-r"},
				wantErr: "onesie: -r does not apply to -i request, which forwards raw responses",
			},
			{
				name:    "should reject -q with -i request",
				args:    []string{"-i", "request", "-q"},
				wantErr: "onesie: -q needs a policy to report, which -i request has none of",
			},
			{
				name: "should reject --assert with -i request",
				args: []string{"-i", "request", "--assert", "answer.value > 0.5"},
				wantErr: "onesie: --assert does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name: "should reject an unparseable --assert with -i request",
				args: []string{"-i", "request", "--assert", "nonsense syntax here !!"},
				wantErr: "onesie: --assert does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name: "should reject --usage with -i request",
				args: []string{"-i", "request", "--usage"},
				wantErr: "onesie: --usage does not apply to -i request, " +
					"whose response bodies already carry usage",
			},
			{
				name:    "should reject --merge with -i request",
				args:    []string{"-i", "request", "--merge"},
				wantErr: "onesie: --merge does not apply to -i request, which forwards raw responses",
			},
			{
				name: "should name --merge-key when that is the flag given",
				args: []string{"-i", "request", "--merge-key", "verdict"},
				wantErr: "onesie: --merge-key does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name: "should reject -m with -i request",
				args: []string{"-i", "request", "-m", "onesie-1.13.0"},
				wantErr: "onesie: -m does not apply to -i request, " +
					"whose bodies carry their own model",
			},
			{
				name: "should reject --stop-on-assert with -i request",
				args: []string{"-i", "request", "--stop-on-assert"},
				wantErr: "onesie: --stop-on-assert does not apply to -i request, " +
					"whose bodies carry no assertion",
			},
			{
				name: "should reject --print-questions with -i request",
				args: []string{"-i", "request", "--print-questions"},
				wantErr: "onesie: --print-questions needs questions of its own, " +
					"which -i request does not build",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, code := runOfflineStdin(t, tc.args, `{"state":"x"}`+"\n")

				if code != ExitUsage {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitUsage, out, errOut)
				}

				if strings.TrimSpace(errOut) != tc.wantErr {
					t.Errorf("stderr = %q, want %q", strings.TrimSpace(errOut), tc.wantErr)
				}

				if out != "" {
					t.Errorf("stdout should be empty, got:\n%s", out)
				}
			})
		}
	})

	t.Run("should print a flag warning once", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			args []string
			want string
		}{
			{
				name: "should print a flag warning once for a mode that builds a plan",
				args: []string{"is this urgent", "-j", "4"},
				want: "-j 4 ignored. -i text reads one record",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				_, out, errOut, code := runRequestMode(
					t, tc.args, "hello\n", 0,
					`{"model":"m","answers":{"answer":{"type":"noul","noul":0.9}}}`)

				if code != ExitOK {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitOK, out, errOut)
				}

				// checkFlags has two callers, and a run that reaches both prints the warning once.
				if got := strings.Count(errOut, tc.want); got != 1 {
					t.Errorf("warning printed %d times, want 1\nstderr:\n%s", got, errOut)
				}
			})
		}
	})

	t.Run("should reject every question flag with --list-models", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "should reject a positional question with --list-models",
				args:    []string{"--list-models", "is this urgent"},
				wantErr: "onesie: --list-models asks no question. Drop the question argument",
			},
			{
				name:    "should reject --ask with --list-models",
				args:    []string{"--list-models", "--ask", "urgent=is this urgent"},
				wantErr: "onesie: --list-models asks no question. Drop --ask",
			},
			{
				name:    "should reject --pick with --list-models",
				args:    []string{"--list-models", "--pick", "a,b"},
				wantErr: "onesie: --list-models asks no question. Drop --pick",
			},
			{
				name:    "should reject --fallback with --list-models",
				args:    []string{"--list-models", "--fallback", "maybe"},
				wantErr: "onesie: --list-models asks no question. Drop --fallback",
			},
			{
				name:    "should reject -f with --list-models before it is read",
				args:    []string{"--list-models", "-f", "nowhere.yaml"},
				wantErr: "onesie: -f does not apply to --list-models, which asks no question",
			},
			{
				name:    "should reject --replace with --list-models",
				args:    []string{"--list-models", "--replace"},
				wantErr: "onesie: --replace applies to -f, which --list-models does not accept",
			},
			{
				name:    "should reject --state with --list-models",
				args:    []string{"--list-models", "--state", "x"},
				wantErr: "onesie: --state does not apply to --list-models, which reads no state",
			},
			{
				name:    "should reject --state-file with --list-models",
				args:    []string{"--list-models", "--state-file", "x"},
				wantErr: "onesie: --state-file does not apply to --list-models, which reads no state",
			},
			{
				// --input's default is text, so a rejection here can only come from Changed, not from
				// the value differing from the default.
				name:    "should reject --input given by its long name with --list-models",
				args:    []string{"--list-models", "--input", "text"},
				wantErr: "onesie: -i does not apply to --list-models, which reads no input",
			},
			{
				// --jobs's default is 1, for the same reason as the --input case above.
				name:    "should reject --jobs given by its long name with --list-models",
				args:    []string{"--list-models", "--jobs", "1"},
				wantErr: "onesie: -j does not apply to --list-models, which makes one request",
			},
			{
				name:    "should reject --model given by its long name with --list-models",
				args:    []string{"--list-models", "--model", "x"},
				wantErr: "onesie: -m names a model to ask, which --list-models does not do",
			},
			{
				name:    "should reject -i with --list-models",
				args:    []string{"--list-models", "-i", "jsonl"},
				wantErr: "onesie: -i does not apply to --list-models, which reads no input",
			},
			{
				name:    "should reject -i request with --list-models by name",
				args:    []string{"--list-models", "-i", "request"},
				wantErr: "onesie: -i does not apply to --list-models, which reads no input",
			},
			{
				name: "should blame --list-models rather than -i request for a shared offender",
				args: []string{
					"--list-models", "-i", "request", "--ask", "urgent=is this urgent",
				},
				wantErr: "onesie: --list-models asks no question. Drop --ask",
			},
			{
				name:    "should reject -o with --list-models",
				args:    []string{"--list-models", "-o", "json"},
				wantErr: "onesie: -o does not apply to --list-models, which writes a fixed listing",
			},
			{
				// Its own rule rather than the -o one, since run leaves Output empty for a bare -r.
				name:    "should reject -r with --list-models",
				args:    []string{"--list-models", "-r"},
				wantErr: "onesie: -r does not apply to --list-models, which writes a fixed listing",
			},
			{
				name:    "should reject -q with --list-models",
				args:    []string{"--list-models", "-q"},
				wantErr: "onesie: -q suppresses output, which leaves --list-models nothing to write",
			},
			{
				name:    "should reject --assert with --list-models",
				args:    []string{"--list-models", "--assert", "answer.value > 0.5"},
				wantErr: "onesie: --assert judges an answer, which --list-models does not produce",
			},
			{
				name:    "should reject an unparseable --assert with --list-models",
				args:    []string{"--list-models", "--assert", "nonsense syntax here !!"},
				wantErr: "onesie: --assert judges an answer, which --list-models does not produce",
			},
			{
				name: "should reject --usage with --list-models",
				args: []string{"--list-models", "--usage"},
				wantErr: "onesie: --usage reports the tokens a question cost, " +
					"which --list-models does not ask",
			},
			{
				name: "should reject --merge with --list-models",
				args: []string{"--list-models", "--merge"},
				wantErr: "onesie: --merge needs answers to fold in, " +
					"which --list-models does not produce",
			},
			{
				name: "should name --merge-key when that is the merge flag given",
				args: []string{"--list-models", "--merge-key", "out"},
				wantErr: "onesie: --merge-key needs answers to fold in, " +
					"which --list-models does not produce",
			},
			{
				name:    "should reject -j with --list-models",
				args:    []string{"--list-models", "-j", "4"},
				wantErr: "onesie: -j does not apply to --list-models, which makes one request",
			},
			{
				name:    "should reject -m with --list-models",
				args:    []string{"--list-models", "-m", "onesie-1.13.0"},
				wantErr: "onesie: -m names a model to ask, which --list-models does not do",
			},
			{
				name: "should reject --unordered with --list-models",
				args: []string{"--list-models", "--unordered"},
				wantErr: "onesie: --unordered applies to streaming input, " +
					"which --list-models does not read",
			},
			{
				name: "should reject --stop-on-error with --list-models",
				args: []string{"--list-models", "--stop-on-error"},
				wantErr: "onesie: --stop-on-error applies to streaming input, " +
					"which --list-models does not read",
			},
			{
				name: "should reject --stop-on-assert with --list-models",
				args: []string{"--list-models", "--stop-on-assert"},
				wantErr: "onesie: --stop-on-assert applies to streaming input, " +
					"which --list-models does not read",
			},
			{
				name: "should reject --skip-blank with --list-models",
				args: []string{"--list-models", "--skip-blank"},
				wantErr: "onesie: --skip-blank applies to streaming input, " +
					"which --list-models does not read",
			},
			{
				name: "should reject --print-request with --list-models",
				args: []string{"--list-models", "--print-request"},
				wantErr: "onesie: --print-request and --list-models each write a different thing " +
					"to stdout. Pass one",
			},
			{
				name: "should reject --print-questions with --list-models",
				args: []string{"--list-models", "--print-questions"},
				wantErr: "onesie: --print-questions and --list-models each write a different thing " +
					"to stdout. Pass one",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, code := runOfflineStdin(t, tc.args, `{"state":"x"}`+"\n")

				if code != ExitUsage {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitUsage, out, errOut)
				}

				if strings.TrimSpace(errOut) != tc.wantErr {
					t.Errorf("stderr = %q, want %q", strings.TrimSpace(errOut), tc.wantErr)
				}

				if out != "" {
					t.Errorf("stdout should be empty, got:\n%s", out)
				}
			})
		}
	})

	t.Run("should reject --stats with a print flag", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			args []string
			want string
		}{
			{
				name: "should reject stats with print-request",
				args: []string{"--ask", "urgent=is this urgent", "--print-request", "--stats"},
				want: "onesie: --stats has nothing to report with --print-request, " +
					"which makes no request",
			},
			{
				name: "should reject stats with print-questions",
				args: []string{"--ask", "urgent=is this urgent", "--print-questions", "--stats"},
				want: "onesie: --stats has nothing to report with --print-questions, " +
					"which makes no request",
			},
			{
				// The path that returns before a plan is built, so the rule has to sit above it.
				name: "should reject stats with print-request under a request mode stream",
				args: []string{"-i", "request", "--print-request", "--stats"},
				want: "onesie: --stats has nothing to report with --print-request, " +
					"which makes no request",
			},
			{
				name: "should reject both print flags at once",
				args: []string{
					"--ask", "urgent=is this urgent", "--print-request", "--print-questions",
				},
				want: "onesie: --print-request and --print-questions each write a different thing " +
					"to stdout. Pass one",
			},
			{
				name: "should reject an output mode with print-questions",
				args: []string{
					"--ask", "urgent=is this urgent", "--print-questions", "-o", "json",
				},
				want: "onesie: -o does not apply to --print-questions, which writes a question file",
			},
			{
				name: "should reject an output mode with print-request",
				args: []string{"--ask", "urgent=is this urgent", "--print-request", "-o", "json"},
				want: "onesie: -o does not apply to --print-request, which writes a request body",
			},
			{
				name: "should reject the raw shorthand with print-request",
				args: []string{"--ask", "urgent=is this urgent", "--print-request", "-r"},
				want: "onesie: -r does not apply to --print-request, which writes a request body",
			},
			{
				name: "should reject quiet with print-questions",
				args: []string{"--ask", "urgent=is this urgent", "--print-questions", "-q"},
				want: "onesie: -q suppresses output, which leaves --print-questions nothing to write",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, code := runOffline(t, tc.args)

				if code != ExitUsage {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitUsage, out, errOut)
				}

				// Rejected before anything reaches stdout, which is the reason the rule lives in
				// checkFlags rather than in the print path.
				if out != "" {
					t.Errorf("stdout should be empty, got:\n%s", out)
				}

				if !strings.Contains(errOut, tc.want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, tc.want)
				}
			})
		}
	})
}

func testConfig(flags runFlags, given []string, events []argv.Event, positional string) runConfig {
	mode, _ := input.ParseMode(flags.input)
	config := runConfig{runFlags: flags, inputMode: mode, events: events, positional: positional}

	for _, name := range given {
		switch name {
		case flagState:
			config.hasState = true
		case flagStateFile:
			config.hasStateFile = true
		case flagMap:
			config.hasMap = true
		case flagID:
			config.hasID = true
		case flagModel:
			config.hasModel = true
		case flagInput:
			config.hasInput = true
		case flagJobs:
			config.jobsSet = true
		case flagTimeout:
			config.timeoutSet = true
		case flagRetries:
			config.retriesSet = true
		case flagMaxRetryAfter:
			config.maxRetryAfterSet = true
		case flagDelimiter:
			config.hasDelimiter = true
			config.parsedDelimiter, _ = input.ParseDelimiter(flags.delimiter)
		}
	}

	return config
}

func assertWarning(t *testing.T, warnings []string, want string, failing bool) {
	t.Helper()

	if want == "" {
		// A passing case that warns is a rule firing where no rule was expected. A failing one is
		// allowed to carry whatever the checks reached before the fatal error.
		if !failing && len(warnings) != 0 {
			t.Errorf("unexpected warnings: %v", warnings)
		}

		return
	}

	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, want) {
		t.Errorf("warnings = %q\nwant one containing %q", joined, want)
	}
}

func filePick(options ...string) []plan.Question {
	named := make([]plan.Option, 0, len(options))
	for _, option := range options {
		named = append(named, plan.Option{Name: option})
	}

	return []plan.Question{{
		ID: "team", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginFile,
		Options: named,
	}}
}
