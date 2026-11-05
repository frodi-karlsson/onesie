package settings_test

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		events      []argv.Event
		file        []plan.Question
		positional  string
		set         values
		typed       []settings.Name
		wantErr     string
		wantExact   string
		wantWarning string
	}{
		{
			name:    "should name only --assert for -q on a box",
			events:  []argv.Event{{Name: "ask", Value: "mug=x"}, {Name: "box", Value: "true"}},
			set:     values{"quiet": true, "jobs": 1},
			wantErr: "-q on 'mug' needs --assert. A box has no policy that could fail it",
		},
		{
			name: "should refuse a question named like a box column under -o csv",
			events: []argv.Event{
				{Name: "ask", Value: "mug=x"}, {Name: "box", Value: "true"}, {Name: "ask", Value: "mug_left=y"},
			},
			set:     values{"output": "csv", "jobs": 1},
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
			set:       values{"output": "csv", "input": "text"},
			wantExact: "question 'a_fallback' has the name of the fallback column -o csv writes for 'a'",
		},
		{
			name: "should accept a question named like a fallback column outside csv and tsv",
			events: []argv.Event{
				{Name: "ask", Value: "a=q"},
				{Name: "fallback", Value: "no"},
				{Name: "ask", Value: "a_fallback=q2"},
			},
			set: values{"output": "json", "input": "text"},
		},
		{
			name: "should reject raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			set:     values{"raw": true},
			wantErr: "-r needs a single question. 'a', 'b' were asked",
		},
		{
			name: "should reject -o raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			set:     values{"output": "raw"},
			wantErr: "-o raw needs a single question. 'a', 'b' were asked",
		},
		{
			name:       "should accept -o raw with a single question",
			positional: "is this urgent",
			set:        values{"output": "raw"},
		},
		{
			name: "should reject quiet on a pick with no policy",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
			},
			set: values{"quiet": true},
			wantErr: "-q on 'team' needs --min-confidence and --fallback, or --assert. " +
				"Without one the exit code is always 0",
		},
		{
			name: "should accept quiet on a pick carrying only an assertion",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
			},
			set: values{"assert": []string{"answer.value > 0.5"}, "quiet": true},
		},
		{
			name:       "should reject raw combined with output",
			positional: "is this urgent",
			set:        values{"raw": true, "output": "json"},
			wantErr:    "-r and -o are mutually exclusive",
		},
		{
			name:       "should reject both state flags together",
			positional: "is this urgent",
			typed:      []settings.Name{"state", "state_file"},
			wantErr:    "--state and --state-file are mutually exclusive",
		},
		{
			name:       "should reject replace with no file given",
			positional: "is this urgent",
			set:        values{"replace": true},
			wantErr:    "--replace applies to -f, which was not given",
		},
		{
			name: "should accept replace alongside a file",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile},
			},
			set: values{"replace": true, "file": "triage.yaml"},
		},
		{
			name: "should name the file's keys in the quiet policy remedy",
			file: filePick("billing", "technical"),
			set:  values{"quiet": true},
			wantExact: "-q on 'team' needs 'min_confidence' and 'fallback', or --assert. " +
				"Without one the exit code is always 0",
		},
		{
			name:       "should reject unordered outside a stream",
			positional: "is this urgent",
			set:        values{"unordered": true, "input": "text"},
			wantErr:    "--unordered applies to streaming input. -i text reads one record",
		},
		{
			name:       "should reject -i beside --image with no state flag",
			positional: "is this urgent",
			set:        values{"image": []string{"photo=photo.png"}, "input": "json"},
			typed:      []settings.Name{"input"},
			wantErr:    "-i json holds one record, which beside --image reads stdin only through --state -. Pass --state - to read the state from stdin",
		},
		{
			name:       "should accept -i beside --image under --state",
			positional: "is this urgent",
			set:        values{"image": []string{"photo=photo.png"}, "input": "json"},
			typed:      []settings.Name{"input", "state"},
		},
		{
			name:       "should reject stop on error outside a stream",
			positional: "is this urgent",
			set:        values{"stop_on_error": true, "input": "json"},
			wantErr:    "--stop-on-error applies to streaming input. -i json reads one record",
		},
		{
			name:       "should reject stop on assert outside a stream",
			positional: "is this urgent",
			set:        values{"stop_on_assert": true, "input": "text"},
			wantErr:    "--stop-on-assert applies to streaming input. -i text reads one record",
		},
		{
			name:       "should accept stop on assert in a gated stream",
			positional: "is this urgent",
			set:        values{"assert": []string{"answer.value > 0.5"}, "stop_on_assert": true, "input": "jsonl"},
		},
		{
			name:       "should reject skip blank outside a stream",
			positional: "is this urgent",
			set:        values{"skip_blank": true, "input": "text"},
			wantErr:    "--skip-blank applies to streaming input",
		},
		{
			name:       "should reject no dedup outside a stream",
			positional: "is this urgent",
			set:        values{"no_dedup": true, "input": "text"},
			wantErr:    "--no-dedup applies to streaming input. -i text reads one record",
		},
		{
			name:       "should accept no dedup in a stream",
			positional: "is this urgent",
			set:        values{"no_dedup": true, "input": "lines"},
		},
		{
			name:       "should accept unordered in a stream",
			positional: "is this urgent",
			set:        values{"unordered": true, "input": "lines"},
		},
		{
			name:       "should reject state with a streaming mode",
			positional: "is this urgent",
			set:        values{"input": "jsonl"},
			typed:      []settings.Name{"state"},
			wantErr:    "--state cannot be combined with -i jsonl",
		},
		{
			name:       "should reject state-file with a streaming mode naming the flag used",
			positional: "is this urgent",
			set:        values{"input": "lines"},
			typed:      []settings.Name{"state_file"},
			wantErr:    "--state-file cannot be combined with -i lines",
		},
		{
			name:       "should reject merge with raw output",
			positional: "is this urgent",
			set:        values{"merge": true, "output": "raw", "input": "lines"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should reject merge with table output outside a stream too",
			positional: "is this urgent",
			set:        values{"merge": true, "output": "table", "input": "text"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should name --merge-key when that is the flag given",
			positional: "is this urgent",
			set:        values{"merge_key": "verdict", "output": "table", "input": "text"},
			wantErr:    "--merge-key needs -o json, values, csv or tsv",
		},
		{
			name:       "should reject merge with the -r spelling of raw",
			positional: "is this urgent",
			set:        values{"merge": true, "raw": true, "input": "text"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should accept merge with values output",
			positional: "is this urgent",
			set:        values{"merge": true, "output": "values", "input": "jsonl"},
		},
		{
			name:       "should accept merge with an explicit auto output",
			positional: "is this urgent",
			set:        values{"merge": true, "output": "auto", "input": "jsonl"},
		},
		{
			name:       "should accept merge with json output",
			positional: "is this urgent",
			set:        values{"merge": true, "output": "json", "input": "jsonl"},
		},
		{
			name:       "should accept merge with an absent output",
			positional: "is this urgent",
			set:        values{"merge": true, "input": "jsonl"},
		},
		{
			name:       "should reject quiet in a stream",
			positional: "is this urgent",
			set:        values{"quiet": true, "input": "jsonl"},
			wantErr:    "-q reads one record. Drop -i jsonl",
		},
		{
			name:       "should reject a table in a stream",
			positional: "is this urgent",
			set:        values{"output": "table", "input": "lines"},
			wantErr:    "-o table reads one record. Drop -i lines or use -o json",
		},
		{
			name:       "should accept a table outside a stream",
			positional: "is this urgent",
			set:        values{"output": "table", "input": "text"},
		},
		{
			name:        "should warn about jobs outside a stream",
			positional:  "is this urgent",
			set:         values{"jobs": 8, "input": "text"},
			typed:       []settings.Name{"jobs"},
			wantWarning: "-j 8 ignored. -i text reads one record",
		},
		{
			name:       "should report the warnings alongside a fatal error",
			positional: "which team",
			events: []argv.Event{
				{Name: "pick", Value: "billing"},
			},
			set:         values{"jobs": 8, "input": "text"},
			typed:       []settings.Name{"jobs"},
			wantErr:     "--pick needs at least two options, got 1: billing",
			wantWarning: "-j 8 ignored. -i text reads one record",
		},
		{
			name:       "should reject a zero job count",
			positional: "is this urgent",
			set:        values{"jobs": 0, "input": "lines"},
			typed:      []settings.Name{"jobs"},
			wantErr:    "-j takes a positive number",
		},
		{
			name:       "should reject a negative job count",
			positional: "is this urgent",
			set:        values{"jobs": -2, "input": "lines"},
			typed:      []settings.Name{"jobs"},
			wantErr:    "-j takes a positive number",
		},
		{
			name:       "should accept a config that never set jobs",
			positional: "is this urgent",
			set:        values{"input": "lines"},
		},
		{
			name:       "should reject a timeout of zero",
			positional: "is this urgent",
			set:        values{"timeout": 0, "input": "text"},
			typed:      []settings.Name{"timeout"},
			wantErr:    "--timeout takes a positive number of seconds, got 0",
		},
		{
			name:       "should reject a negative retry count",
			positional: "is this urgent",
			set:        values{"retries": -1, "input": "text"},
			typed:      []settings.Name{"retries"},
			wantErr:    "--retries takes a retry count of zero or more, got -1",
		},
		{
			name:       "should reject a retry count above the ceiling",
			positional: "is this urgent",
			set:        values{"retries": 100001, "input": "text"},
			typed:      []settings.Name{"retries"},
			wantErr:    "--retries takes at most 100, got 100001",
		},
		{
			name:       "should accept a retry count at the ceiling",
			positional: "is this urgent",
			set:        values{"retries": 100, "input": "text"},
			typed:      []settings.Name{"retries"},
		},
		{
			name:       "should reject a max retry after of zero",
			positional: "is this urgent",
			set:        values{"max_retry_after": 0, "input": "text"},
			typed:      []settings.Name{"max_retry_after"},
			wantErr:    "--max-retry-after must be a positive number of seconds, got 0",
		},
		{
			name:       "should reject a negative max retry after",
			positional: "is this urgent",
			set:        values{"max_retry_after": -5, "input": "text"},
			typed:      []settings.Name{"max_retry_after"},
			wantErr:    "--max-retry-after must be a positive number of seconds, got -5",
		},
		{
			name:       "should reject a timeout above the ceiling",
			positional: "is this urgent",
			set:        values{"timeout": 86401, "input": "text"},
			typed:      []settings.Name{"timeout"},
			wantErr:    "--timeout takes at most 86400 seconds, got 86401",
		},
		{
			name:       "should reject a timeout that would wrap a duration to a fraction",
			positional: "is this urgent",
			set:        values{"timeout": 18446744074, "input": "text"},
			typed:      []settings.Name{"timeout"},
			wantErr:    "--timeout takes at most 86400 seconds, got 18446744074",
		},
		{
			name:       "should reject a max retry after above the ceiling",
			positional: "is this urgent",
			set:        values{"max_retry_after": 86401, "input": "text"},
			typed:      []settings.Name{"max_retry_after"},
			wantErr:    "--max-retry-after takes at most 86400 seconds, got 86401",
		},
		{
			name:       "should reject a max retry after that would wrap a duration to a fraction",
			positional: "is this urgent",
			set:        values{"max_retry_after": 18446744074, "input": "text"},
			typed:      []settings.Name{"max_retry_after"},
			wantErr:    "--max-retry-after takes at most 86400 seconds, got 18446744074",
		},
		{
			name:       "should accept a timeout at the ceiling",
			positional: "is this urgent",
			set:        values{"timeout": 86400, "input": "text"},
			typed:      []settings.Name{"timeout"},
		},
		{
			name:       "should accept a retry count of zero",
			positional: "is this urgent",
			set:        values{"retries": 0, "input": "text"},
			typed:      []settings.Name{"retries"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			given := run{
				set: tc.set, typed: tc.typed, events: tc.events, positional: tc.positional, file: tc.file, planned: true,
			}

			warnings, _, err := settings.Validate(given.view(t), settings.Rules())
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

	t.Run("should check the settings alone when no plan is built", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			set        values
			typed      []settings.Name
			events     []argv.Event
			positional string
			facts      settings.Facts
			wantErr    string
		}{
			{
				name:    "should refuse --image with -i request",
				set:     values{"image": []string{"photo=photo.png"}, "input": "request", "jobs": 1},
				wantErr: "--image does not apply to -i request, whose bodies carry their own state",
			},
			{
				name:    "should refuse --image-field with -i request",
				set:     values{"input": "request", "jobs": 1, "image_field": []string{"photo"}},
				wantErr: "--image-field does not apply to -i request, whose bodies carry their own state",
			},
			{
				name:    "should refuse --box with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "box", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --box",
			},
			{
				name:       "should reject a positional question with -i request",
				set:        values{"input": "request", "jobs": 1},
				positional: "is it urgent",
				wantErr: "-i request carries its own questions. " +
					"Drop the question argument",
			},
			{
				name:    "should reject --ask with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "ask", Value: "a=x"}},
				wantErr: "-i request carries its own questions. Drop --ask",
			},
			{
				name: "should reject -f with -i request",
				set:  values{"input": "request", "jobs": 1, "file": "q.yaml"},
				wantErr: "-f does not apply to -i request, " +
					"which carries its own questions",
			},
			{
				name:    "should reject --replace with -i request",
				set:     values{"input": "request", "jobs": 1, "replace": true},
				wantErr: "--replace applies to -f, which -i request does not accept",
			},
			{
				name:    "should reject --pick with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "pick", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --pick",
			},
			{
				name:    "should reject --rate with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "rate", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --rate",
			},
			{
				name:    "should reject --desc with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "desc", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --desc",
			},
			{
				name:    "should reject --sep with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "sep", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --sep",
			},
			{
				name:   "should reject --threshold with -i request",
				set:    values{"input": "request", "jobs": 1},
				events: []argv.Event{{Name: "threshold", Value: "x"}},
				wantErr: "--threshold does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name:   "should reject --min-confidence with -i request",
				set:    values{"input": "request", "jobs": 1},
				events: []argv.Event{{Name: "min-confidence", Value: "x"}},
				wantErr: "--min-confidence does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name:   "should reject --fallback with -i request",
				set:    values{"input": "request", "jobs": 1},
				events: []argv.Event{{Name: "fallback", Value: "x"}},
				wantErr: "--fallback does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name:  "should reject --state with -i request",
				set:   values{"input": "request", "jobs": 1},
				typed: []settings.Name{"state"},
				wantErr: "--state does not apply to -i request, " +
					"whose bodies carry their own state",
			},
			{
				name:  "should reject --state-file with -i request",
				set:   values{"input": "request", "jobs": 1},
				typed: []settings.Name{"state_file"},
				wantErr: "--state-file does not apply to -i request, " +
					"whose bodies carry their own state",
			},
			{
				name:    "should reject --map with -i request",
				set:     values{"input": "request", "jobs": 1},
				typed:   []settings.Name{"map"},
				wantErr: "--map does not apply to -i request, whose bodies carry their own state",
			},
			{
				name:    "should reject --map with --list-models",
				set:     values{"list_models": true, "input": "text"},
				typed:   []settings.Name{"map"},
				wantErr: "--map does not apply to --list-models, which reads no state",
			},
			{
				name:    "should reject --map with --print-questions",
				set:     values{"print_questions": true, "input": "text"},
				typed:   []settings.Name{"map"},
				wantErr: "--map does not apply to --print-questions, which reads no state",
			},
			{
				name:  "should accept --map with --print-request",
				set:   values{"print_request": true, "input": "jsonl", "jobs": 1},
				typed: []settings.Name{"map"},
			},
			{
				name:    "should reject --id with -i request",
				set:     values{"input": "request", "jobs": 1},
				typed:   []settings.Name{"id"},
				wantErr: "--id does not apply to -i request, which forwards raw responses",
			},
			{
				name:    "should reject --id with --list-models",
				set:     values{"list_models": true, "input": "text"},
				typed:   []settings.Name{"id"},
				wantErr: "--id does not apply to --list-models, which reads no input",
			},
			{
				name:    "should reject --id with --print-questions",
				set:     values{"print_questions": true, "input": "text"},
				typed:   []settings.Name{"id"},
				wantErr: "--id does not apply to --print-questions, which reads no input",
			},
			{
				name:  "should accept --id with --print-request",
				set:   values{"print_request": true, "input": "csv", "jobs": 1},
				typed: []settings.Name{"id"},
			},
			{
				name:    "should reject -o with -i request",
				set:     values{"input": "request", "jobs": 1, "output": "json"},
				wantErr: "-o does not apply to -i request, which forwards raw responses",
			},
			{
				name:    "should reject -r with -i request",
				set:     values{"input": "request", "jobs": 1, "raw": true},
				wantErr: "-r does not apply to -i request, which forwards raw responses",
			},
			{
				name: "should reject --usage with -i request",
				set:  values{"input": "request", "jobs": 1, "usage": true},
				wantErr: "--usage does not apply to -i request, " +
					"whose response bodies already carry usage",
			},
			{
				name:    "should reject --merge with -i request",
				set:     values{"merge": true, "input": "request", "jobs": 1},
				wantErr: "--merge does not apply to -i request, which forwards raw responses",
			},
			{
				name: "should name --merge-key when that is the flag given",
				set:  values{"merge_key": "verdict", "input": "request", "jobs": 1},
				wantErr: "--merge-key does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name:  "should reject -m with -i request",
				set:   values{"input": "request", "jobs": 1},
				typed: []settings.Name{"model"},
				wantErr: "-m does not apply to -i request, " +
					"whose bodies carry their own model",
			},
			{
				name: "should reject --print-questions with -i request",
				set:  values{"input": "request", "jobs": 1, "print_questions": true},
				wantErr: "--print-questions needs questions of its own, " +
					"which -i request does not build",
			},
			{
				name:  "should accept the streaming flags with -i request",
				set:   values{"input": "request", "jobs": 4, "unordered": true, "stop_on_error": true, "skip_blank": true},
				typed: []settings.Name{"jobs"},
			},
			{
				name: "should reject both print flags at once",
				set:  values{"print_request": true, "print_questions": true, "input": "text"},
				wantErr: "--print-request and --print-questions each write a different " +
					"thing to stdout. Pass one",
			},
			{
				name:    "should reject -o with --print-questions",
				set:     values{"print_questions": true, "output": "json", "input": "text"},
				wantErr: "-o does not apply to --print-questions, which writes a question file",
			},
			{
				name:    "should reject -r with --print-request",
				set:     values{"print_request": true, "raw": true, "input": "text"},
				wantErr: "-r does not apply to --print-request, which writes a request body",
			},
			{
				name: "should name --merge-key when that is the spelling given",
				set:  values{"merge_key": "verdict", "print_request": true, "input": "text"},
				wantErr: "--merge-key needs answers to fold in, " +
					"which --print-request does not produce",
			},
			{
				name:    "should reject -q with --print-request",
				set:     values{"print_request": true, "quiet": true, "input": "text"},
				wantErr: "-q suppresses output, which leaves --print-request nothing to write",
			},
			{
				name: "should reject --stats with --print-questions",
				set:  values{"print_questions": true, "stats": true, "input": "text"},
				wantErr: "--stats has nothing to report with --print-questions, " +
					"which makes no request",
			},
			{
				name: "should reject --stats with --print-request under -i request",
				set:  values{"input": "request", "jobs": 1, "print_request": true, "stats": true},
				wantErr: "--stats has nothing to report with --print-request, " +
					"which makes no request",
			},
			{
				name: "should accept --stats on its own under -i request",
				set:  values{"input": "request", "jobs": 1, "stats": true},
			},
			{
				name:    "should blame the dry run rather than the mode for -o under -i request",
				set:     values{"input": "request", "jobs": 1, "print_request": true, "output": "json"},
				wantErr: "-o does not apply to --print-request, which writes a request body",
			},
			{
				name: "should blame the dry run rather than the mode for --usage under -i request",
				set:  values{"input": "request", "jobs": 1, "print_request": true, "usage": true},
				wantErr: "--usage reports the tokens a question cost, " +
					"which --print-request does not ask",
			},
			{
				name:    "should keep the request mode reason for a flag the dry run has no rule for",
				set:     values{"input": "request", "jobs": 1, "print_request": true},
				events:  []argv.Event{{Name: "ask", Value: "a=x"}},
				wantErr: "-i request carries its own questions. Drop --ask",
			},
			{
				name: "should reject --assert with -i request",
				set:  values{"assert": []string{"answer.value > 0.5"}, "input": "request", "jobs": 1},
				wantErr: "--assert does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name:    "should reject --assert with --list-models",
				set:     values{"assert": []string{"answer.value > 0.5"}, "list_models": true, "input": "text"},
				wantErr: "--assert judges an answer, which --list-models does not produce",
			},
			{
				name:    "should reject --assert with --print-request",
				set:     values{"assert": []string{"answer.value > 0.5"}, "print_request": true, "input": "text"},
				wantErr: "--assert judges an answer, which --print-request does not produce",
			},
			{
				name:  "should ignore a file's assertion with --print-request",
				set:   values{"print_request": true, "file": "q.yaml", "input": "text"},
				facts: settings.Facts{FileAssert: "answer.value > 0.5"},
			},
			{
				name:  "should ignore a file's two gates with --print-request",
				set:   values{"print_request": true, "file": "q.yaml", "input": "text"},
				facts: settings.Facts{FileAssert: "answer.value > 0.5", FileAbstainIf: "answer.value > 0.4"},
			},
			{
				name:    "should reject --assert typed beside a file's assertion with --print-request",
				set:     values{"assert": []string{"answer.value > 0.5"}, "print_request": true, "file": "q.yaml", "input": "text"},
				wantErr: "--assert judges an answer, which --print-request does not produce",
			},
			{
				name:    "should reject --abstain-if typed beside a file's gates with --print-request",
				set:     values{"abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "file": "q.yaml", "input": "text"},
				facts:   settings.Facts{FileAssert: "answer.value > 0.5"},
				wantErr: "--abstain-if judges an answer, which --print-request does not produce",
			},
			{
				name: "should accept --assert with --print-questions",
				set:  values{"assert": []string{"answer.value > 0.5"}, "print_questions": true, "input": "text"},
			},
			{
				name: "should reject --stop-on-assert with --print-request",
				set:  values{"print_request": true, "stop_on_assert": true, "input": "lines"},
				wantErr: "--stop-on-assert ends a stream on a false assertion, " +
					"which --print-request does not produce",
			},
			{
				name:    "should reject --abstain-if with --list-models",
				set:     values{"abstain_if": []string{"answer.value > 0.4"}, "list_models": true, "input": "text"},
				wantErr: "--abstain-if judges an answer, which --list-models does not produce",
			},
			{
				name: "should reject --abstain-if with -i request",
				set:  values{"abstain_if": []string{"answer.value > 0.4"}, "input": "request", "jobs": 1},
				wantErr: "--abstain-if does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name:    "should name --assert first when --print-request is given both gates",
				set:     values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "input": "text"},
				wantErr: "--assert judges an answer, which --print-request does not produce",
			},
			{
				name:    "should reject --abstain-if with --print-request",
				set:     values{"abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "input": "text"},
				wantErr: "--abstain-if judges an answer, which --print-request does not produce",
			},
			{
				name: "should accept --abstain-if with --print-questions",
				set:  values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}, "print_questions": true, "input": "text"},
			},
			{
				name: "should accept --stop-on-error with --print-request",
				set:  values{"print_request": true, "stop_on_error": true, "input": "lines"},
			},
			{
				name:    "should refuse --image-field on one record",
				set:     values{"input": "text", "image_field": []string{"photo"}, "jobs": 1},
				wantErr: "--image-field applies to streaming input. -i text reads one record",
			},
			{
				name:    "should refuse a second --image-field under -i lines",
				set:     values{"input": "lines", "image_field": []string{"a", "b"}, "jobs": 1},
				wantErr: "-i lines takes one --image-field, since a line holds one path",
			},
			{
				name:    "should reject -o csv with --merge over jsonl",
				set:     values{"merge": true, "input": "jsonl", "output": "csv", "jobs": 1},
				wantErr: "-o csv with --merge needs -i csv or -i tsv, since other input has no fixed columns",
			},
			{
				name: "should accept -o tsv with --merge over csv",
				set:  values{"merge": true, "input": "csv", "output": "tsv", "jobs": 1},
			},
			{
				name:    "should reject --merge-key with -o csv",
				set:     values{"merge_key": "verdict", "input": "csv", "output": "csv", "jobs": 1},
				wantErr: "--merge-key does not apply to -o csv, which puts the answers in columns",
			},
			{
				name:    "should reject --merge-key with --merge and -o csv, which --merge would otherwise hide",
				set:     values{"merge": true, "merge_key": "verdict", "input": "csv", "output": "csv", "jobs": 1},
				wantErr: "--merge-key does not apply to -o csv, which puts the answers in columns",
			},
			{
				name:    "should reject --merge-key with --merge and -o tsv",
				set:     values{"merge": true, "merge_key": "verdict", "input": "tsv", "output": "tsv", "jobs": 1},
				wantErr: "--merge-key does not apply to -o tsv, which puts the answers in columns",
			},
			{
				name:    "should refuse --merge-key state under -i text",
				set:     values{"merge_key": "state", "input": "text", "jobs": 1},
				wantErr: "--merge-key state does not apply to -i text, which wraps every record under a state key",
			},
			{
				name:    "should refuse --merge-key state under -i lines",
				set:     values{"merge_key": "state", "input": "lines", "jobs": 1},
				wantErr: "--merge-key state does not apply to -i lines, which wraps every record under a state key",
			},
			{
				name: "should accept --merge-key state under -i jsonl",
				set:  values{"merge_key": "state", "input": "jsonl", "jobs": 1},
			},
			{
				name:    "should reject --usage with -q",
				set:     values{"input": "text", "quiet": true, "usage": true, "jobs": 1},
				wantErr: "--usage does not apply to -q, which suppresses output",
			},
			{
				name:    "should reject --usage with -o tsv",
				set:     values{"input": "csv", "output": "tsv", "usage": true, "jobs": 1},
				wantErr: "--usage does not apply to -o tsv, which has no column for it",
			},
			{
				name:    "should reject --usage with -o values",
				set:     values{"input": "text", "output": "values", "usage": true, "jobs": 1},
				wantErr: "--usage does not apply to -o values, which writes only the answers. Use -o json",
			},
			{
				name:    "should reject --usage with -o raw",
				set:     values{"input": "jsonl", "output": "raw", "usage": true, "jobs": 1},
				wantErr: "--usage does not apply to -o raw, which writes only the answers. Use -o json",
			},
			{
				name:    "should reject --usage with -r",
				set:     values{"input": "text", "raw": true, "usage": true, "jobs": 1},
				wantErr: "--usage does not apply to -r, which writes only the answers. Use -o json",
			},
			{
				name: "should accept --usage with -o json",
				set:  values{"input": "text", "output": "json", "usage": true, "jobs": 1},
			},
			{
				name:    "should reject -j above the cap",
				set:     values{"input": "jsonl", "jobs": 2000000},
				typed:   []settings.Name{"jobs"},
				wantErr: "-j takes at most 256 records in flight, got 2000000",
			},
			{
				name:    "should reject --resume without --out",
				set:     values{"input": "jsonl", "resume": true},
				wantErr: "--resume needs --out, the file it picks up from",
			},
			{
				name:    "should reject --resume on a single record",
				set:     values{"input": "text", "resume": true, "out": "answers.txt"},
				wantErr: "--resume applies to streaming input. -i text reads one record",
			},
			{
				name:    "should reject --merge with -o markdown",
				set:     values{"merge": true, "input": "jsonl", "output": "markdown", "jobs": 1},
				wantErr: "--merge needs -o json, values, csv or tsv",
			},
			{
				name:    "should reject --merge-key with -o md",
				set:     values{"merge_key": "verdict", "input": "jsonl", "output": "md", "jobs": 1},
				wantErr: "--merge-key needs -o json, values, csv or tsv",
			},
			{
				name:    "should reject -r with -o markdown",
				set:     values{"input": "text", "raw": true, "output": "markdown", "jobs": 1},
				wantErr: "-r and -o are mutually exclusive",
			},
			{
				name:    "should reject -q with -o markdown on one record",
				set:     values{"input": "text", "output": "markdown", "quiet": true, "jobs": 1},
				wantErr: "-q suppresses output, which leaves -o markdown nothing to write",
			},
			{
				name:    "should reject -q with -o md on a stream, naming the spelling given",
				set:     values{"input": "jsonl", "output": "md", "quiet": true, "jobs": 1},
				wantErr: "-q suppresses output, which leaves -o md nothing to write",
			},
			{
				name: "should reject --resume with -o markdown",
				set:  values{"input": "jsonl", "output": "markdown", "resume": true, "out": "a.md", "jobs": 1},
				wantErr: "--resume reads each record's outcome back from --out, " +
					"which a markdown table does not keep. Use -o json, values, csv or tsv",
			},
			{
				name: "should accept --out without --resume under -o markdown",
				set:  values{"input": "jsonl", "output": "markdown", "out": "a.md", "jobs": 1},
			},
			{
				name: "should accept --usage with -o markdown",
				set:  values{"input": "text", "output": "markdown", "usage": true, "jobs": 1},
			},
			{
				name:    "should reject --resume with --unordered",
				set:     values{"input": "jsonl", "resume": true, "out": "a.jsonl", "unordered": true},
				wantErr: "--resume relies on input order, which --unordered gives up. Pass --id to resume by id",
			},
			{
				name:  "should accept --resume with --unordered when --id names the records",
				set:   values{"input": "jsonl", "resume": true, "out": "a.jsonl", "unordered": true, "jobs": 1},
				typed: []settings.Name{"id"},
			},
			{
				name:    "should reject --resume with --id and -r, which writes no id",
				set:     values{"input": "jsonl", "resume": true, "out": "a.txt", "raw": true},
				typed:   []settings.Name{"id"},
				wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
			},
			{
				name:    "should reject --resume with --id and -o raw, which writes no id",
				set:     values{"input": "jsonl", "resume": true, "out": "a.txt", "output": "raw"},
				typed:   []settings.Name{"id"},
				wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
			},
			{
				name:    "should reject a resume by position with -r and --assert, since a raw line keeps no outcome",
				set:     values{"assert": []string{"answer.value > 0.5"}, "input": "jsonl", "resume": true, "out": "a.txt", "raw": true},
				wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
			},
			{
				name:    "should reject a resume by position with -o raw and a file's assert",
				set:     values{"input": "jsonl", "resume": true, "out": "a.txt", "output": "raw"},
				facts:   settings.Facts{FileAssert: "answer.value > 0.5"},
				wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
			},
			{
				name:    "should reject a resume by id with -o raw and --assert",
				set:     values{"assert": []string{"answer.value > 0.5"}, "input": "jsonl", "resume": true, "out": "a.txt", "output": "raw"},
				typed:   []settings.Name{"id"},
				wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
			},
			{
				name:    "should reject a resume by position with -o raw and no gate, since a raw line keeps no failure",
				set:     values{"input": "jsonl", "resume": true, "out": "a.txt", "output": "raw", "jobs": 1},
				wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
			},
			{
				name:    "should reject a resume by position with -r and no gate",
				set:     values{"input": "jsonl", "resume": true, "out": "a.txt", "raw": true, "jobs": 1},
				wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
			},
			{
				name: "should accept a tsv resume by position under --stop-on-error, since the file is read at runtime",
				set:  values{"input": "jsonl", "resume": true, "out": "a.tsv", "output": "tsv", "stop_on_error": true, "jobs": 1},
			},
			{
				name:  "should accept a csv resume by id under --stop-on-error",
				set:   values{"input": "jsonl", "resume": true, "out": "a.csv", "output": "csv", "stop_on_error": true, "jobs": 1},
				typed: []settings.Name{"id"},
			},
			{
				name:    "should reject --prune without --resume",
				set:     values{"input": "jsonl", "out": "a.jsonl", "prune": true, "jobs": 1},
				typed:   []settings.Name{"id"},
				wantErr: "--prune applies to --resume, which was not given",
			},
			{
				name:    "should reject --prune without --id",
				set:     values{"input": "jsonl", "resume": true, "out": "a.jsonl", "prune": true, "jobs": 1},
				wantErr: "--prune drops the answered ids the input no longer has, so it needs --id",
			},
			{
				name:  "should accept --prune with --resume and --id",
				set:   values{"input": "jsonl", "resume": true, "out": "a.jsonl", "prune": true, "jobs": 1},
				typed: []settings.Name{"id"},
			},
			{
				name:    "should reject --resume with --list-models",
				set:     values{"list_models": true, "input": "text", "resume": true, "out": "m.txt"},
				wantErr: "--resume applies to streaming input, which --list-models does not read",
			},
			{
				name:    "should reject --resume with --print-questions",
				set:     values{"print_questions": true, "input": "text", "resume": true, "out": "q.yaml"},
				wantErr: "--resume applies to streaming input, which --print-questions does not read",
			},
			{
				name:    "should reject --id with -i text",
				set:     values{"input": "text"},
				typed:   []settings.Name{"id"},
				wantErr: "--id applies to streaming input. -i text reads one record",
			},
			{
				name:    "should reject --id with -i json",
				set:     values{"input": "json"},
				typed:   []settings.Name{"id"},
				wantErr: "--id applies to streaming input. -i json reads one record",
			},
			{
				name:  "should accept --id with streaming input",
				set:   values{"input": "jsonl", "jobs": 1},
				typed: []settings.Name{"id"},
			},
			{
				name:    "should reject -q with -i request",
				set:     values{"input": "request", "jobs": 1, "quiet": true},
				wantErr: "-q needs a policy to report, which -i request has none of",
			},
			{
				name:    "should still reject -j 0 with -i request",
				set:     values{"input": "request", "jobs": 0},
				typed:   []settings.Name{"jobs"},
				wantErr: "-j takes a positive number of records in flight, got 0",
			},
			{
				name:    "should reject -r with -o outside request mode",
				set:     values{"raw": true, "output": "json", "input": "text"},
				wantErr: "-r and -o are mutually exclusive",
			},
			{
				name:    "should reject --abstain-if without an assertion",
				set:     values{"abstain_if": []string{"answer.value > 0.4"}, "input": "text"},
				wantErr: "--abstain-if needs --assert, since without one every record is a yes",
			},
			{
				name:    "should name the file's key when only the file carried the abstain expression",
				set:     values{"file": "q.yaml", "input": "text"},
				facts:   settings.Facts{FileAbstainIf: "answer.value > 0.4"},
				wantErr: "'abstain_if' needs --assert, since without one every record is a yes",
			},
			{
				name:  "should accept --abstain-if when the only assertion comes from the file",
				set:   values{"abstain_if": []string{"answer.value > 0.4"}, "file": "q.yaml", "input": "text"},
				facts: settings.Facts{FileAssert: "answer.value > 0.5"},
			},
			{
				name:    "should reject --stop-on-assert without an assertion",
				set:     values{"stop_on_assert": true, "input": "jsonl"},
				wantErr: "--stop-on-assert needs --assert, since without one no assertion is false",
			},
			{
				name:  "should accept --stop-on-assert when the only assertion comes from the file",
				set:   values{"stop_on_assert": true, "file": "q.yaml", "input": "jsonl"},
				facts: settings.Facts{FileAssert: "answer.value > 0.5"},
			},
			{
				name:    "should reject --resume with --print-request",
				set:     values{"input": "jsonl", "resume": true, "out": "a.jsonl", "print_request": true, "jobs": 1},
				typed:   []settings.Name{"id"},
				wantErr: "--print-request writes request bodies, not answers, so --resume has nothing to pick up. Drop --resume",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				given := run{set: tc.set, typed: tc.typed, events: tc.events, positional: tc.positional, facts: tc.facts}

				warnings, _, err := settings.Validate(given.view(t), settings.Rules())

				if tc.wantErr == "" {
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}

					if len(warnings) != 0 {
						t.Errorf("warnings = %q, want none", warnings)
					}

					return
				}

				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}
			})
		}
	})

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

				given := run{
					set:   values{"input": tc.input, "output": tc.output, "delimiter": cmp.Or(tc.delimiter, ";")},
					typed: []settings.Name{"input", "delimiter"},
				}

				_, _, err := settings.Validate(given.view(t), settings.Rules())
				if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr) {
					t.Errorf("Validate error = %v, want %q", err, tc.wantErr)
				}
			})
		}
	})

	t.Run("should refuse and pass under each rule", func(t *testing.T) {
		t.Parallel()

		for _, tc := range ruleCases() {
			t.Run("should refuse under "+tc.rule, func(t *testing.T) {
				t.Parallel()

				_, _, err := settings.Validate(tc.refused.view(t), []settings.Rule{ruleByID(t, tc.rule)})
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}
			})

			t.Run("should pass under "+tc.rule, func(t *testing.T) {
				t.Parallel()

				if _, _, err := settings.Validate(tc.passed.view(t), []settings.Rule{ruleByID(t, tc.rule)}); err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			})
		}
	})

	t.Run("should cover every rule in Rules", func(t *testing.T) {
		t.Parallel()

		var covered []string
		for _, tc := range ruleCases() {
			covered = append(covered, tc.rule)
		}

		for _, rule := range settings.Rules() {
			if !slices.Contains(covered, rule.ID) {
				t.Errorf("rule %s has no refusing and passing case", rule.ID)
			}
		}
	})
}

func TestRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, rules []settings.Rule)
	}{
		{
			name: "should name real rows in every rule's Reads",
			check: func(t *testing.T, rules []settings.Rule) {
				t.Helper()

				for _, rule := range rules {
					for _, name := range rule.Reads {
						if _, found := settings.Lookup(name); !found {
							t.Errorf("rule %s reads %s, which no row is", rule.ID, name)
						}
					}
				}
			},
		},
		{
			name: "should give every rule its own ID",
			check: func(t *testing.T, rules []settings.Rule) {
				t.Helper()

				seen := map[string]bool{}
				for _, rule := range rules {
					if seen[rule.ID] {
						t.Errorf("rule %s is listed twice", rule.ID)
					}

					seen[rule.ID] = true
				}
			},
		},
		{
			name: "should give every rule something to do",
			check: func(t *testing.T, rules []settings.Rule) {
				t.Helper()

				for _, rule := range rules {
					if rule.Check == nil && rule.Warn == nil && rule.Notice == nil {
						t.Errorf("rule %s has no Check, Warn or Notice", rule.ID)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, settings.Rules())
		})
	}
}

func ruleCases() []ruleCase {
	two := []argv.Event{{Name: "ask", Value: "a=one"}, {Name: "ask", Value: "b=two"}}

	return []ruleCase{
		{
			rule:    "needs-a-question",
			refused: run{planned: true},
			passed:  run{planned: true, positional: "is this urgent"},
			wantErr: "no question given. Pass a question, --ask, or -f",
		},
		{
			rule:    "list-models-alone",
			refused: run{set: values{"list_models": true}, typed: []settings.Name{"model"}},
			passed:  run{set: values{"list_models": true, "stats": true}},
			wantErr: "-m names a model to ask, which --list-models does not do",
		},
		{
			rule:    "resume-needs-a-stream",
			refused: run{set: values{"resume": true}},
			passed:  run{set: values{"resume": true, "out": "answers.jsonl", "input": "jsonl"}},
			wantErr: "--resume needs --out, the file it picks up from",
		},
		{
			rule:    "prune-needs-resume-and-id",
			refused: run{set: values{"prune": true}},
			passed:  run{set: values{"prune": true, "resume": true}, typed: []settings.Name{"id"}},
			wantErr: "--prune applies to --resume, which was not given",
		},
		{
			rule:    "dry-run-and-request",
			refused: run{set: values{"print_request": true, "mock": "answers.jsonl"}, fromEnv: []settings.Name{"mock"}},
			passed:  run{set: values{"print_request": true, "input": "request", "stop_on_error": true}},
			wantErr: "ONESIE_MOCK answers requests, and --print-request sends none. Unset it or drop --print-request",
		},
		{
			rule:    "delimiter-needs-csv",
			refused: run{set: values{"delimiter": ";", "input": "jsonl"}, typed: []settings.Name{"delimiter"}},
			passed:  run{set: values{"delimiter": ";", "input": "csv"}, typed: []settings.Name{"delimiter"}},
			wantErr: "--delimiter applies to -i csv and -o csv. -i jsonl has no delimiter",
		},
		{
			rule:    "abstain-needs-assert",
			refused: run{set: values{"abstain_if": []string{"answer.value > 0.4"}}},
			passed: run{
				set:   values{"abstain_if": []string{"answer.value > 0.4"}},
				facts: settings.Facts{FileAssert: "answer.value > 0.5"},
			},
			wantErr: "--abstain-if needs --assert, since without one every record is a yes",
		},
		{
			rule:    "raw-and-output",
			refused: run{set: values{"raw": true, "output": "json"}},
			passed:  run{set: values{"raw": true}},
			wantErr: "-r and -o are mutually exclusive",
		},
		{
			rule:    "quiet-without-markdown",
			refused: run{set: values{"quiet": true, "output": "md"}},
			passed:  run{set: values{"quiet": true, "output": "json"}},
			wantErr: "-q suppresses output, which leaves -o md nothing to write",
		},
		{
			rule:    "state-and-state-file",
			refused: run{typed: []settings.Name{"state", "state_file"}},
			passed:  run{typed: []settings.Name{"state"}},
			wantErr: "--state and --state-file are mutually exclusive",
		},
		{
			rule:    "replace-needs-file",
			refused: run{set: values{"replace": true}},
			passed:  run{set: values{"replace": true, "file": "triage.yaml"}},
			wantErr: "--replace applies to -f, which was not given",
		},
		{
			rule:    "merge-needs-mergeable-output",
			refused: run{set: values{"merge": true, "output": "markdown"}},
			passed:  run{set: values{"merge": true, "output": "auto"}},
			wantErr: "--merge needs -o json, values, csv or tsv",
		},
		{
			rule:    "merge-key-not-wrapped",
			refused: run{set: values{"merge_key": "state", "input": "lines"}},
			passed:  run{set: values{"merge_key": "state", "input": "jsonl"}},
			wantErr: "--merge-key state does not apply to -i lines, which wraps every record under a state key",
		},
		{
			rule:    "delimited-output",
			refused: run{set: values{"output": "csv", "usage": true}},
			passed:  run{set: values{"output": "csv"}},
			wantErr: "--usage does not apply to -o csv, which has no column for it",
		},
		{
			rule:    "quiet-without-usage",
			refused: run{set: values{"quiet": true, "usage": true}},
			passed:  run{set: values{"usage": true}},
			wantErr: "--usage does not apply to -q, which suppresses output",
		},
		{
			rule:    "usage-needs-a-usage-column",
			refused: run{set: values{"usage": true, "output": "values"}},
			passed:  run{set: values{"usage": true, "output": "json"}},
			wantErr: "--usage does not apply to -o values, which writes only the answers. Use -o json",
		},
		{
			rule:    "numbers-in-range",
			refused: run{set: values{"timeout": 0}, typed: []settings.Name{"timeout"}},
			passed:  run{set: values{"timeout": 30}, typed: []settings.Name{"timeout"}},
			wantErr: "--timeout takes a positive number of seconds, got 0",
		},
		{
			rule:    "streaming-flags-need-a-stream",
			refused: run{set: values{"unordered": true}},
			passed:  run{set: values{"unordered": true, "input": "jsonl"}},
			wantErr: "--unordered applies to streaming input. -i text reads one record",
		},
		{
			rule:    "stream-reads-its-own-state",
			refused: run{set: values{"input": "jsonl"}, typed: []settings.Name{"state"}},
			passed:  run{set: values{"input": "jsonl"}},
			wantErr: "--state cannot be combined with -i jsonl",
		},
		{
			rule:    "quiet-reads-one-record",
			refused: run{set: values{"quiet": true, "input": "jsonl"}},
			passed:  run{set: values{"quiet": true}},
			wantErr: "-q reads one record. Drop -i jsonl or use -o values and filter the stream",
		},
		{
			rule:    "table-reads-one-record",
			refused: run{set: values{"output": "table", "input": "lines"}},
			passed:  run{set: values{"output": "table"}},
			wantErr: "-o table reads one record. Drop -i lines or use -o json",
		},
		{
			rule:    "stop-on-assert-needs-assert",
			refused: run{set: values{"stop_on_assert": true}},
			passed:  run{set: values{"stop_on_assert": true, "assert": []string{"answer.value > 0.5"}}},
			wantErr: "--stop-on-assert needs --assert, since without one no assertion is false",
		},
		{
			rule: "columns-unique",
			refused: run{planned: true, set: values{"output": "csv"}, events: []argv.Event{
				{Name: "ask", Value: "a=q"}, {Name: "fallback", Value: "no"}, {Name: "ask", Value: "a_fallback=q2"},
			}},
			passed: run{planned: true, set: values{"output": "json"}, events: []argv.Event{
				{Name: "ask", Value: "a=q"}, {Name: "fallback", Value: "no"}, {Name: "ask", Value: "a_fallback=q2"},
			}},
			wantErr: "question 'a_fallback' has the name of the fallback column -o csv writes for 'a'",
		},
		{
			rule:    "line-field-reserved",
			refused: run{planned: true, set: values{"input": "lines"}, events: []argv.Event{{Name: "ask", Value: "line=q"}}},
			passed:  run{planned: true, set: values{"input": "jsonl"}, events: []argv.Event{{Name: "ask", Value: "line=q"}}},
			wantErr: "question id 'line' is reserved under -i lines, which writes the input line number as line",
		},
		{
			rule: "questions-valid",
			refused: run{planned: true, events: []argv.Event{
				{Name: "ask", Value: "a=one"}, {Name: "ask", Value: "a=two"},
			}},
			passed:  run{planned: true, events: two},
			wantErr: "question id 'a' is given twice",
		},
		{
			rule:    "quiet-needs-one-question",
			refused: run{planned: true, set: values{"quiet": true}, events: two},
			passed:  run{planned: true, set: values{"quiet": true, "assert": []string{"answer.value > 0.5"}}, events: two},
			wantErr: "-q needs a single question, or --assert to gate several. 'a', 'b' were asked",
		},
		{
			rule:    "raw-needs-one-question",
			refused: run{planned: true, set: values{"raw": true}, events: two},
			passed:  run{planned: true, set: values{"raw": true}, positional: "is this urgent"},
			wantErr: "-r needs a single question. 'a', 'b' were asked",
		},
	}
}

type ruleCase struct {
	rule    string
	refused run
	passed  run
	wantErr string
}

func ruleByID(t *testing.T, id string) settings.Rule {
	t.Helper()

	for _, rule := range settings.Rules() {
		if rule.ID == id {
			return rule
		}
	}

	t.Fatalf("no rule %s", id)

	return settings.Rule{}
}

type run struct {
	set        values
	typed      []settings.Name
	fromEnv    []settings.Name
	events     []argv.Event
	positional string
	file       []plan.Question
	facts      settings.Facts
	planned    bool
}

func (r run) view(t *testing.T) settings.View {
	t.Helper()

	merged := &settings.Merged{}

	for name, value := range r.set {
		merged.Set(name, value, r.sourceOf(name))
	}

	for _, name := range slices.Concat(r.typed, r.fromEnv) {
		if _, set := r.set[name]; !set {
			merged.Set(name, nil, r.sourceOf(name))
		}
	}

	facts := r.facts
	facts.Events, facts.Positional = r.events, r.positional

	if !r.planned {
		return settings.NewView(merged, nil, facts)
	}

	built, err := plan.Assemble(plan.Source{
		Events: r.events, File: r.file, Positional: r.positional,
		ReadFile: func(string) ([]byte, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("assembling: %v", err)
	}

	return settings.NewView(merged, built, facts)
}

func (r run) sourceOf(name settings.Name) settings.Source {
	switch {
	case slices.Contains(r.typed, name):
		return settings.SourceFlag
	case slices.Contains(r.fromEnv, name):
		return settings.SourceEnv
	default:
		return settings.SourceDefault
	}
}

type values = map[settings.Name]any

func assertWarning(t *testing.T, warnings []string, want string, failing bool) {
	t.Helper()

	if want == "" {
		// A passing case that warns is a rule firing where no rule was expected. A failing one is
		// allowed to carry whatever the rules reached before the refusal.
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
