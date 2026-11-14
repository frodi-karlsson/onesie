package settings_test

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

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
		typed       []settingrow.Name
		fromFile    []settingrow.Name
		facts       settings.Facts
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
			name:       "should mention a positional box under -q without the id the user never typed",
			positional: "where is the mug",
			events:     []argv.Event{{Name: "box", Value: "true"}},
			set:        values{"quiet": true, "jobs": 1},
			wantExact:  "-q on the question needs --assert. A box has no policy that could fail it",
		},
		{
			name:       "should mention a positional pick under -q without the id the user never typed",
			positional: "which team",
			events:     []argv.Event{{Name: "pick", Value: "x,y"}},
			set:        values{"quiet": true},
			wantExact:  "-q on the question needs --min-confidence and --fallback, or --assert. Without one the exit code is always 0",
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
			name: "should accept a question named like a fallback column outside csv and tsv",
			events: []argv.Event{
				{Name: "ask", Value: "a=q"},
				{Name: "fallback", Value: "no"},
				{Name: "ask", Value: "a_fallback=q2"},
			},
			set: values{"output": "json", "input": "text"},
		},
		{
			name: "should refuse quiet on a pick with no policy",
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
			name: "should refuse --for without -f before checking the values after it",
			events: []argv.Event{
				{Name: "ask", Value: "a=q"}, {Name: "for", Value: "a"}, {Name: "threshold", Value: "high"},
			},
			wantExact: "--for selects a question from -f, which was not given",
		},
		{
			name:   "should name a built in set the way the file is named elsewhere",
			file:   fileNouls("hostile", "spam"),
			set:    values{"file": "moderation"},
			facts:  settings.Facts{FileName: "built in moderation"},
			events: []argv.Event{{Name: "for", Value: "nope"}},
			wantExact: "--for nope names no question in built in moderation, which has: 'hostile', 'spam'. " +
				"Define a new question with --ask nope=QUESTION",
		},
		{
			name:   "should accept --for naming a question of the file",
			file:   fileNouls("urgent", "spam"),
			set:    values{"file": "two.yaml"},
			events: []argv.Event{{Name: "for", Value: "spam"}, {Name: "threshold", Value: "0.9"}},
		},
		{
			name: "should name the file's keys in the quiet policy remedy",
			file: filePick("billing", "technical"),
			set:  values{"quiet": true},
			wantExact: "-q on 'team' needs 'min_confidence' and 'fallback', or --assert. " +
				"Without one the exit code is always 0",
		},
		{
			name:       "should refuse -i beside --image with no state flag",
			positional: "is this urgent",
			set:        values{"image": []string{"photo=photo.png"}, "input": "json"},
			typed:      []settingrow.Name{"input"},
			wantErr:    "-i json holds one record, which beside --image reads stdin only through --state -. Pass --state - to read the state from stdin",
		},
		{
			name:       "should accept -i beside --image under --state",
			positional: "is this urgent",
			set:        values{"image": []string{"photo=photo.png"}, "input": "json"},
			typed:      []settingrow.Name{"input", "state"},
		},
		{
			name:       "should refuse --stop-on=error outside a stream",
			positional: "is this urgent",
			set:        values{"stop_on": "error", "input": "json"},
			wantErr:    "--stop-on applies to streaming input. -i json reads one record",
		},
		{
			name:       "should accept --stop-on=fail in a gated stream",
			positional: "is this urgent",
			set:        values{"assert": []string{"answer.value > 0.5"}, "stop_on": "fail", "input": "jsonl"},
		},
		{
			name:       "should refuse skip blank outside a stream",
			positional: "is this urgent",
			set:        values{"skip_blank": true, "input": "text"},
			wantErr:    "--skip-blank applies to streaming input",
		},
		{
			name:       "should refuse no dedup outside a stream",
			positional: "is this urgent",
			set:        values{"no_dedup": true, "input": "text"},
			wantErr:    "--no-dedup applies to streaming input. -i text reads one record",
		},
		{
			name:       "should refuse state with a streaming mode",
			positional: "is this urgent",
			set:        values{"input": "jsonl"},
			typed:      []settingrow.Name{"state"},
			wantErr:    "--state cannot be combined with -i jsonl",
		},
		{
			name:       "should name --merge-key when that is the flag given",
			positional: "is this urgent",
			set:        values{"merge_key": "verdict", "output": "table", "input": "text"},
			wantErr:    "--merge-key needs -o json, values, csv or tsv",
		},
		{
			name:       "should refuse merge with -o raw",
			positional: "is this urgent",
			set:        values{"merge": true, "output": "raw", "input": "text"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should accept merge with an explicit auto output",
			positional: "is this urgent",
			set:        values{"merge": true, "output": "auto", "input": "jsonl"},
		},
		{
			name:       "should accept merge with an absent output",
			positional: "is this urgent",
			set:        values{"merge": true, "input": "jsonl"},
		},
		{
			name:       "should refuse quiet in a stream",
			positional: "is this urgent",
			set:        values{"quiet": true, "input": "jsonl"},
			wantErr:    "-q reads one record. Drop -i jsonl",
		},
		{
			name:       "should refuse a table in a stream",
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
			name:       "should refuse -j 4 on one record",
			positional: "is this urgent",
			set:        values{"jobs": 4, "input": "text"},
			typed:      []settingrow.Name{"jobs"},
			wantExact:  "-j applies to streaming input. -i text reads one record. Pass -i jsonl, -i lines, -i csv or -i tsv, or drop -j",
		},
		{
			name:       "should accept -j 1 on one record",
			positional: "is this urgent",
			set:        values{"jobs": 1, "input": "text"},
			typed:      []settingrow.Name{"jobs"},
		},
		{
			name:       "should accept -j 4 on a stream",
			positional: "is this urgent",
			set:        values{"jobs": 4, "input": "jsonl"},
			typed:      []settingrow.Name{"jobs"},
		},
		{
			name: "should report the warnings alongside a fatal error",
			events: []argv.Event{
				{Name: "ask", Value: "team=which team"},
				{Name: "pick", Value: "billing,support"},
				{Name: "desc", Value: "billing=money"},
				{Name: "ask", Value: "tier=which tier"},
				{Name: "pick", Value: "gold"},
			},
			set:         values{"input": "text"},
			wantErr:     "--pick needs at least two options in question 'tier', got 1: gold",
			wantWarning: "'team' describes billing but not support",
		},
		{
			name:       "should accept a config that never set jobs",
			positional: "is this urgent",
			set:        values{"input": "lines"},
		},
		{
			name: "should refuse a shape flag with no ask to bind to",
			events: []argv.Event{
				{Name: "pick", Value: "x,y"},
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
				{Name: "ask", Value: "c=three"},
			},
			wantErr: "--pick given with no --ask to bind to and 3 questions asked",
		},
		{
			name: "should accept policy on an ask added beside a one question body",
			file: bodyQuestions(1),
			events: []argv.Event{
				{Name: "ask", Value: "extra=second"},
				{Name: "threshold", Value: "0.8"},
			},
		},
		{
			name: "should accept a one question body with no policy at all",
			file: bodyQuestions(1),
		},
		{
			name:   "should refuse a top level --threshold beside two questions, naming --for",
			file:   fileNouls("urgent", "spam"),
			set:    values{"file": "two.yaml"},
			events: []argv.Event{{Name: "threshold", Value: "0.9"}},
			wantExact: "--threshold applies to one question, and 2 were asked: 'urgent', 'spam'. " +
				"Put it after --for ID to set one, as in --for urgent --threshold 0.9",
		},
		{
			name:   "should suggest the first file question that takes the flag",
			file:   append(filePick("billing", "technical"), fileNouls("urgent")...),
			events: []argv.Event{{Name: "threshold", Value: "0.9"}},
			wantExact: "--threshold applies to one question, and 2 were asked: 'team', 'urgent'. " +
				"Put it after --for ID to set one, as in --for urgent --threshold 0.9",
		},
		{
			name:   "should suggest a pick question from the file for a top level --pick",
			file:   append(fileNouls("urgent"), filePick("billing", "technical")...),
			events: []argv.Event{{Name: "pick", Value: "x,y"}},
			wantExact: "--pick applies to one question, and 2 were asked: 'urgent', 'team'. " +
				"Put it after --for ID to set one, as in --for team --pick x,y",
		},
		{
			name:   "should suggest --ask when no file question takes the flag",
			file:   fileNouls("urgent", "spam"),
			events: []argv.Event{{Name: "box", Value: "true"}},
			wantExact: "--box applies to one question, and 2 were asked: 'urgent', 'spam'. " +
				"No question from the file takes it, so put it after the --ask it belongs to, as in --ask ID=QUESTION --box",
		},
		{
			name:   "should refuse it beside a body of two questions",
			file:   bodyQuestions(2),
			events: []argv.Event{{Name: "threshold", Value: "0.8"}},
			wantExact: "--threshold applies to one question, and 2 were asked: 'b0', 'b1'. " +
				"Put it after --for ID to set one, as in --for b0 --threshold 0.8",
		},
		{
			name: "should list every question asked beside a top level flag",
			file: bodyQuestions(2),
			events: []argv.Event{
				{Name: "threshold", Value: "0.8"},
				{Name: "ask", Value: "extra=second"},
			},
			wantErr: "and 3 were asked: 'b0', 'b1', 'extra'. Put it after --for ID to set one, as in --for b0",
		},
		{
			name: "should keep the old wording for a top level flag beside several --ask with no file",
			events: []argv.Event{
				{Name: "fallback", Value: "no"}, {Name: "ask", Value: "a=one"}, {Name: "ask", Value: "b=two"},
			},
			wantExact: "--fallback given with no --ask to bind to and 2 questions asked",
		},
		{
			name:   "should accept a policy --for gives a body question",
			file:   bodyQuestions(2),
			set:    values{"file": "body.json"},
			events: []argv.Event{{Name: "for", Value: "b1"}, {Name: "threshold", Value: "0.8"}},
		},
		{
			name:    "should refuse --pick after --for on a body question",
			file:    bodyQuestions(2),
			set:     values{"file": "body.json"},
			events:  []argv.Event{{Name: "for", Value: "b1"}, {Name: "pick", Value: "x,y"}},
			wantErr: "--pick cannot reshape a request body's question. A body carries its own type and criteria",
		},
		{
			name:   "should refuse --rate on a file pick question",
			file:   filePick("billing", "technical"),
			set:    values{"file": "team.yaml"},
			events: []argv.Event{{Name: "for", Value: "team"}, {Name: "rate", Value: "low,high"}},
			wantExact: "--rate cannot make 'team' a score question, since a flag never changes the shape of a " +
				"question from team.yaml. Redefine it whole with --ask team=QUESTION --replace",
		},
		{
			name:   "should refuse --box on a file question",
			file:   filePick("billing", "technical"),
			events: []argv.Event{{Name: "box", Value: "true"}},
			wantExact: "--box cannot make 'team' a box question, since a flag never changes the shape of a " +
				"question from the question file. Redefine it whole with --ask team=QUESTION --replace",
		},
		{
			name: "should refuse --box on a file box question",
			file: []plan.Question{{ID: "mug", Shape: plan.Box, Boxed: true, Instructions: "q", Origin: plan.OriginFile}},
			set:  values{"file": "mug.yaml"},
			events: []argv.Event{
				{Name: "for", Value: "mug"}, {Name: "box", Value: "true"},
			},
			wantExact: "--box does not apply to 'mug', a box question from mug.yaml already. Drop --box",
		},
		{
			name:    "should refuse --pick on a yes or no file question",
			file:    fileNouls("urgent"),
			events:  []argv.Event{{Name: "pick", Value: "a,b"}},
			wantErr: "--pick cannot make 'urgent' a choice question",
		},
		{
			name:      "should spell a --threshold given after --for by its flag in its range error",
			file:      fileNouls("urgent", "spam"),
			set:       values{"file": "two.yaml"},
			events:    []argv.Event{{Name: "for", Value: "spam"}, {Name: "threshold", Value: "2"}},
			wantExact: "--threshold must be between 0 and 1, got 2",
		},
		{
			name:   "should suggest no shape for --min-confidence on a yes or no file question",
			file:   fileNouls("urgent"),
			events: []argv.Event{{Name: "min-confidence", Value: "0.5"}},
			wantExact: "--min-confidence needs a confidence value, and 'urgent' is a yes or no question. " +
				"Use --threshold",
		},
		{
			name:   "should accept --pick on a file pick question",
			file:   filePick("billing", "technical"),
			events: []argv.Event{{Name: "pick", Value: "sales,support"}},
		},
		{
			name:    "should refuse --pick that leaves a file pick question one option",
			file:    filePick("billing", "technical"),
			events:  []argv.Event{{Name: "pick", Value: "sales"}},
			wantErr: "needs at least two options in question 'team', got 1: sales",
		},
		{
			name:    "should refuse a top level --desc on a lone body question",
			file:    bodyQuestions(1),
			events:  []argv.Event{{Name: "desc", Value: "yes=nope"}},
			wantErr: "--desc cannot reshape a request body's question",
		},
		{
			name:       "should report a streaming -q before -o table",
			positional: "is this urgent",
			set:        values{"quiet": true, "output": "table", "input": "jsonl"},
			wantExact:  "-q reads one record. Drop -i jsonl or use -o values and filter the stream",
		},
		{
			name:       "should report --resume without --out before --prune",
			positional: "is this urgent",
			set:        values{"prune": true, "resume": true},
			wantExact:  "--resume needs --out, the file it picks up from",
		},
		{
			name:       "should report the dry run before --delimiter",
			positional: "is this urgent",
			set:        values{"print_request": true, "input": "jsonl", "delimiter": "x", "output": "raw"},
			typed:      []settingrow.Name{"input", "delimiter"},
			wantExact:  "-o does not apply to --print-request, which writes the request bodies and sends none",
		},
		{
			name:       "should report --delimiter before --abstain-if without an assertion",
			positional: "is this urgent",
			set:        values{"abstain_if": []string{"answer.value > 0.4"}, "input": "jsonl", "delimiter": ";"},
			typed:      []settingrow.Name{"input", "delimiter"},
			wantExact:  "--delimiter applies to -i csv and -o csv. -i jsonl has no delimiter",
		},
		{
			name:      "should report a streaming -q before the plan rules",
			events:    []argv.Event{{Name: "ask", Value: "a=one"}, {Name: "ask", Value: "b=two"}},
			set:       values{"quiet": true, "input": "jsonl"},
			wantExact: "-q reads one record. Drop -i jsonl or use -o values and filter the stream",
		},
		{
			name:       "should refuse -q with --merge",
			positional: "is this urgent",
			set:        values{"quiet": true, "merge": true},
			wantExact:  "-q suppresses output, which leaves --merge nothing to write",
		},
		{
			name:       "should refuse -q with --out and leave no file",
			positional: "is this urgent",
			set:        values{"quiet": true, "out": "answers.jsonl"},
			typed:      []settingrow.Name{"out"},
			wantExact:  "-q suppresses output, which leaves --out nothing to write",
		},
		{
			name:      "should keep the gate message for -q with -o raw on two questions",
			events:    []argv.Event{{Name: "ask", Value: "a=one"}, {Name: "ask", Value: "b=two"}},
			set:       values{"quiet": true, "output": "raw"},
			wantExact: "-q needs a single question, or --assert to gate several. 'a', 'b' were asked",
		},
		{
			name:       "should accept -q alone",
			positional: "is this urgent",
			set:        values{"quiet": true},
		},
		{
			name:       "should report --stop-on=fail on one record before its missing assertion",
			positional: "is this urgent",
			set:        values{"stop_on": "fail"},
			wantExact:  "--stop-on applies to streaming input. -i text reads one record",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			given := run{
				set: tc.set, typed: tc.typed, fromFile: tc.fromFile, events: tc.events, positional: tc.positional,
				file: tc.file, planned: true, facts: tc.facts,
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

	t.Run("should note a run setting typed twice under rule setting-given-twice", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			command settingrow.Command
			args    []string
			unread  bool
			want    []string
		}{
			{
				name: "should spell the flag as it was last typed",
				args: []string{"q", "--model", "a", "-m", "b"}, want: []string{"-m is given twice, 'b' overrides 'a'"},
			},
			{
				name: "should name every earlier value of a setting typed three times",
				args: []string{"q", "-m", "a", "-m", "b", "-m", "c"},
				want: []string{"-m is given 3 times, 'c' overrides 'a' and 'b'"},
			},
			{
				name: "should note a switch turned off after it was turned on",
				args: []string{"q", "--cache", "--cache=false"}, want: []string{"--cache is given twice, 'false' overrides 'true'"},
			},
			{
				name: "should spell the flag by its long name when the command line cannot be read",
				args: []string{"q", "-m", "a", "-m", "b"}, unread: true, want: []string{"--model is given twice, 'b' overrides 'a'"},
			},
			{
				name:    "should note a shared root flag typed twice on calibrate",
				command: settingrow.CommandCalibrate, args: []string{"--profile", "a", "--profile", "b"},
				want: []string{"--profile is given twice, 'b' overrides 'a'"},
			},
			{name: "should accept --desc for different keys", args: []string{"--ask", "a=q", "--desc", "k=x", "--desc", "j=y"}},
			{
				name: "should accept repeated --assert, --pick and --ask",
				args: []string{
					"--ask", "a=q", "--pick", "x", "--pick", "y", "--ask", "b=r",
					"--assert", "a.value > 0.5", "--assert", "b.value > 0.5",
				},
			},
			{name: "should print nothing for the same value twice", args: []string{"q", "-m", "a", "-m", "a"}},
			{name: "should print nothing for one switch value spelled two ways", args: []string{"q", "--cache=1", "--cache=true"}},
			{name: "should print nothing for one number spelled two ways", args: []string{"q", "-i", "jsonl", "-j", "2", "-j", "02"}},
			{
				name: "should cut a long value to 40 characters",
				args: []string{"q", "--state", strings.Repeat("a", 50), "--state", "it's short"},
				want: []string{`--state is given twice, 'it\'s short' overrides '` + strings.Repeat("a", 40) + `…'`},
			},
			{
				name: "should leave out an earlier value the last one repeats",
				args: []string{"q", "-m", "a", "-m", "b", "-m", "a"}, want: []string{"-m is given 3 times, 'a' overrides 'b'"},
			},
		}

		rule := ruleByID(t, "setting-given-twice")

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				root := register(settingrow.CommandRoot)
				parsed, facts := root, settings.Facts{Command: settingrow.CommandRoot, Given: root.given, Args: tc.args}

				if tc.command == settingrow.CommandCalibrate {
					parsed = register(settingrow.CommandCalibrate)
					parsed.share(root, "profile", "provider")
					parsed.given.Within(root.given)
					facts.Command, facts.Given = settingrow.CommandCalibrate, parsed.given
				}

				parsed.parse(t, tc.args...)

				if tc.unread {
					facts.Args = nil
				}

				_, notices, err := settings.Validate(settings.NewView(&settings.Merged{}, nil, facts), []settings.Rule{rule})
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}

				var got []string
				for _, notice := range notices {
					got = append(got, notice.String())
				}

				if !slices.Equal(got, tc.want) {
					t.Errorf("notices = %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("should note a question setting typed twice under rule setting-given-twice", func(t *testing.T) {
		t.Parallel()

		ask := func(events ...argv.Event) []argv.Event {
			return append([]argv.Event{{Name: "ask", Value: "a=q"}}, events...)
		}

		tests := []struct {
			name       string
			events     []argv.Event
			positional string
			file       []plan.Question
			want       []string
		}{
			{
				name:   "should keep the last value of a question setting typed twice for one --ask",
				events: ask(argv.Event{Name: "threshold", Value: "0.5"}, argv.Event{Name: "threshold", Value: "0.6"}),
				want:   []string{"--threshold is given twice for 'a', '0.6' overrides '0.5'"},
			},
			{
				name:       "should keep the last value of a question setting typed twice for the bare question",
				positional: "is this urgent",
				events:     []argv.Event{{Name: "threshold", Value: "0.5"}, {Name: "threshold", Value: "0.6"}},
				want:       []string{"--threshold is given twice, '0.6' overrides '0.5'"},
			},
			{
				name: "should keep the last value of a question setting typed twice for one --for, with a notice",
				file: fileNouls("tone", "spam"),
				events: []argv.Event{
					{Name: "for", Value: "tone"},
					{Name: "fallback", Value: "human"},
					{Name: "for", Value: "spam"},
					{Name: "threshold", Value: "0.6"},
					{Name: "for", Value: "tone"},
					{Name: "fallback", Value: "x"},
				},
				want: []string{"--fallback is given twice for 'tone', 'x' overrides 'human'"},
			},
			{
				name: "should count a top level flag and a --for group on the lone file question as one",
				file: fileNouls("urgent"),
				events: []argv.Event{
					{Name: "threshold", Value: "0.5"}, {Name: "for", Value: "urgent"}, {Name: "threshold", Value: "0.6"},
				},
				want: []string{"--threshold is given twice for 'urgent', '0.6' overrides '0.5'"},
			},
			{
				name: "should count a top level flag a lone --ask takes with the group's own",
				events: []argv.Event{
					{Name: "min-confidence", Value: "0.5"}, {Name: "ask", Value: "a=q"}, {Name: "min-confidence", Value: "0.7"},
				},
				want: []string{"--min-confidence is given twice for 'a', '0.7' overrides '0.5'"},
			},
			{
				name:   "should note --desc given twice for the same key",
				events: ask(argv.Event{Name: "desc", Value: "k=x"}, argv.Event{Name: "desc", Value: "k=y"}),
				want:   []string{"--desc is given twice for 'a', 'k=y' overrides 'k=x'"},
			},
			{
				name:   "should accept --desc for different keys",
				events: ask(argv.Event{Name: "desc", Value: "k=x"}, argv.Event{Name: "desc", Value: "j=y"}),
			},
			{
				name: "should accept repeated --pick and --rate",
				events: ask(
					argv.Event{Name: "pick", Value: "x"}, argv.Event{Name: "pick", Value: "y"},
					argv.Event{Name: "ask", Value: "b=r"}, argv.Event{Name: "rate", Value: "1"},
					argv.Event{Name: "rate", Value: "2"},
				),
			},
			{
				name: "should accept a repeated --for with different settings",
				file: fileNouls("tone"),
				events: []argv.Event{
					{Name: "for", Value: "tone"},
					{Name: "fallback", Value: "human"},
					{Name: "for", Value: "tone"},
					{Name: "threshold", Value: "0.6"},
				},
			},
			{
				name:   "should print nothing for the same question setting typed twice with one value",
				events: ask(argv.Event{Name: "threshold", Value: "0.5"}, argv.Event{Name: "threshold", Value: ".50"}),
			},
			{
				name: "should keep apart the same setting typed for two questions",
				events: ask(
					argv.Event{Name: "threshold", Value: "0.5"}, argv.Event{Name: "ask", Value: "b=r"},
					argv.Event{Name: "threshold", Value: "0.6"},
				),
			},
		}

		rule := ruleByID(t, "setting-given-twice")

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				given := run{
					planned: true, events: tc.events, positional: tc.positional, file: tc.file,
					set: values{"file": "q.yaml"}, facts: settings.Facts{Command: settingrow.CommandRoot},
				}

				_, notices, err := settings.Validate(given.view(t), []settings.Rule{rule})
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}

				var got []string
				for _, notice := range notices {
					got = append(got, notice.String())
				}

				if !slices.Equal(got, tc.want) {
					t.Errorf("notices = %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("should check the settings alone when no plan is built", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			set        values
			typed      []settingrow.Name
			fromEnv    []settingrow.Name
			fromFile   []settingrow.Name
			events     []argv.Event
			positional string
			facts      settings.Facts
			wantErr    string
		}{
			{
				name:     "should ignore an assertion the merge took from the file with --print-request",
				set:      values{"assert": []string{"answer.value > 0.5"}, "print_request": true, "file": "q.yaml", "input": "text"},
				fromFile: []settingrow.Name{"assert"},
			},
			{
				name:     "should name the file's key when the merge took the abstain expression from the file",
				set:      values{"abstain_if": []string{"answer.value > 0.4"}, "file": "q.yaml", "input": "text"},
				fromFile: []settingrow.Name{"abstain_if"},
				wantErr:  "'abstain_if' needs --assert, since without one every record ends answered",
			},
			{
				name:    "should name --merge-key when that is the flag given",
				set:     values{"merge_key": "verdict", "input": "request", "jobs": 1},
				wantErr: "--merge-key does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should blame the dry run rather than the mode for -o under -i request",
				set:     values{"input": "request", "jobs": 1, "print_request": true, "output": "json"},
				wantErr: "-o does not apply to --print-request, which writes the request bodies and sends none",
			},
			{
				name:    "should blame the dry run rather than the mode for --usage under -i request",
				set:     values{"input": "request", "jobs": 1, "print_request": true, "usage": true},
				wantErr: "--usage does not apply to --print-request, which writes the request bodies and sends none",
			},
			{
				name:    "should keep the request mode reason for a flag the dry run has no rule for",
				set:     values{"input": "request", "jobs": 1, "print_request": true},
				events:  []argv.Event{{Name: "ask", Value: "a=x"}},
				wantErr: "--ask does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:     "should ignore a file's assertion with --print-request",
				set:      values{"assert": []string{"answer.value > 0.5"}, "print_request": true, "file": "q.yaml", "input": "text"},
				fromFile: []settingrow.Name{"assert"},
			},
			{
				name:     "should ignore a file's two gates with --print-request",
				set:      values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "file": "q.yaml", "input": "text"},
				fromFile: []settingrow.Name{"assert", "abstain_if"},
			},
			{
				name:    "should refuse --assert typed beside a file's assertion with --print-request",
				set:     values{"assert": []string{"answer.value > 0.5"}, "print_request": true, "file": "q.yaml", "input": "text"},
				wantErr: "--assert does not apply to --print-request, which writes the request bodies and sends none. Rehearse the whole command with --mock, or see the questions with --print-questions",
			},
			{
				name:     "should refuse --abstain-if typed beside a file's gates with --print-request",
				set:      values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "file": "q.yaml", "input": "text"},
				fromFile: []settingrow.Name{"assert"},
				wantErr:  "--abstain-if does not apply to --print-request, which writes the request bodies and sends none. Rehearse the whole command with --mock, or see the questions with --print-questions",
			},
			{
				name:    "should refuse --stop-on=fail with --print-request",
				set:     values{"print_request": true, "stop_on": "fail", "input": "lines"},
				wantErr: "--stop-on=fail does not apply to --print-request, which writes the request bodies and sends none",
			},
			{
				name:    "should name --assert first when --print-request is given both gates",
				set:     values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "input": "text"},
				wantErr: "--assert does not apply to --print-request, which writes the request bodies and sends none. Rehearse the whole command with --mock, or see the questions with --print-questions",
			},
			{
				name: "should accept --stop-on=error with --print-request",
				set:  values{"print_request": true, "stop_on": "error", "input": "lines"},
			},
			{
				name:    "should refuse --threshold beside --print-request",
				set:     values{"print_request": true, "input": "text"},
				events:  []argv.Event{{Name: "threshold", Value: "0.9"}},
				wantErr: "--threshold does not apply to --print-request, which writes the request bodies and sends none. Rehearse the whole command with --mock, or see the questions with --print-questions",
			},
			{
				name:    "should refuse --fallback typed in a --for group beside --print-request",
				set:     values{"print_request": true, "input": "text", "file": "q.yaml"},
				events:  []argv.Event{{Name: "for", Value: "tone"}, {Name: "fallback", Value: "human"}},
				wantErr: "--fallback does not apply to --print-request, which writes the request bodies and sends none. Rehearse the whole command with --mock, or see the questions with --print-questions",
			},
			{
				name: "should accept a file's policy with --print-request",
				set:  values{"print_request": true, "input": "text", "file": "q.yaml", "threshold": 0.9},
			},
			{
				name:    "should skip ONESIE_CACHE beside --print-questions",
				set:     values{"print_questions": true, "input": "text", "cache": true},
				fromEnv: []settingrow.Name{"cache"},
			},
			{
				name:    "should skip ONESIE_CACHE beside --print-request",
				set:     values{"print_request": true, "input": "text", "cache": true},
				fromEnv: []settingrow.Name{"cache"},
			},
			{
				name:    "should skip a typed --cache beside an ambient ONESIE_MOCK",
				set:     values{"input": "text", "cache": true, "mock": "m.json"},
				typed:   []settingrow.Name{"cache"},
				fromEnv: []settingrow.Name{"mock"},
			},
			{
				name:    "should skip ONESIE_CACHE beside --mock",
				set:     values{"input": "text", "cache": true, "mock": "m.json"},
				typed:   []settingrow.Name{"mock"},
				fromEnv: []settingrow.Name{"cache"},
			},
			{
				name:  "should accept --cache=false beside --mock",
				set:   values{"input": "text", "cache": false, "mock": "m.json"},
				typed: []settingrow.Name{"cache", "mock"},
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
				name:    "should refuse -o csv with --merge over jsonl",
				set:     values{"merge": true, "input": "jsonl", "output": "csv", "jobs": 1},
				wantErr: "-o csv with --merge needs -i csv or -i tsv, since other input has no fixed columns",
			},
			{
				name: "should accept -o tsv with --merge over csv",
				set:  values{"merge": true, "input": "csv", "output": "tsv", "jobs": 1},
			},
			{
				name:    "should refuse --merge-key with -o csv",
				set:     values{"merge_key": "verdict", "input": "csv", "output": "csv", "jobs": 1},
				wantErr: "--merge-key does not apply to -o csv, which puts the answers in columns",
			},
			{
				name:    "should refuse --merge-key with --merge and -o csv, which --merge would otherwise hide",
				set:     values{"merge": true, "merge_key": "verdict", "input": "csv", "output": "csv", "jobs": 1},
				wantErr: "--merge-key does not apply to -o csv, which puts the answers in columns",
			},
			{
				name: "should accept --merge-key state under -i jsonl",
				set:  values{"merge_key": "state", "input": "jsonl", "jobs": 1},
			},
			{
				name:    "should refuse --resume on a single record",
				set:     values{"input": "text", "resume": true, "out": "answers.txt"},
				wantErr: "--resume applies to streaming input. -i text reads one record",
			},
			{
				name:    "should refuse -q with -o md on a stream, naming the spelling given",
				set:     values{"input": "jsonl", "output": "markdown", "quiet": true, "jobs": 1},
				facts:   settings.Facts{Given: typedGiven(t, "-o", "md")},
				wantErr: "-q suppresses output, which leaves -o md nothing to write",
			},
			{
				name: "should refuse --resume with -o markdown",
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
				name:    "should refuse --resume with --unordered",
				set:     values{"input": "jsonl", "resume": true, "out": "a.jsonl", "unordered": true},
				wantErr: "--resume relies on input order, which --unordered gives up. Pass --id to resume by id",
			},
			{
				name:  "should accept --resume with --unordered when --id names the records",
				set:   values{"input": "jsonl", "resume": true, "out": "a.jsonl", "unordered": true, "jobs": 1},
				typed: []settingrow.Name{"id"},
			},
			{
				name:    "should refuse --resume with --id and -o raw, which writes no id",
				set:     values{"input": "jsonl", "resume": true, "out": "a.txt", "output": "raw"},
				typed:   []settingrow.Name{"id"},
				wantErr: "--resume needs output that keeps each record's outcome, which raw lines do not. Use -o values or -o json",
			},
			{
				name: "should accept a tsv resume by position under --stop-on=error, since the file is read at runtime",
				set:  values{"input": "jsonl", "resume": true, "out": "a.tsv", "output": "tsv", "stop_on": "error", "jobs": 1},
			},
			{
				name:    "should refuse --prune without --resume",
				set:     values{"input": "jsonl", "out": "a.jsonl", "prune": true, "jobs": 1},
				typed:   []settingrow.Name{"id"},
				wantErr: "--prune applies to --resume, which was not given",
			},
			{
				name:    "should refuse --prune without --id",
				set:     values{"input": "jsonl", "resume": true, "out": "a.jsonl", "prune": true, "jobs": 1},
				wantErr: "--prune drops the answered ids the input no longer has, so it needs --id",
			},
			{
				name:  "should accept --prune with --resume and --id",
				set:   values{"input": "jsonl", "resume": true, "out": "a.jsonl", "prune": true, "jobs": 1},
				typed: []settingrow.Name{"id"},
			},
			{
				name:    "should refuse --resume with --list-models",
				set:     values{"list_models": true, "input": "text", "resume": true, "out": "m.txt"},
				wantErr: "--resume applies to streaming input, which --list-models does not read",
			},
			{
				name:    "should refuse --resume with --print-questions",
				set:     values{"print_questions": true, "input": "text", "resume": true, "out": "q.yaml"},
				wantErr: "--resume applies to streaming input, which --print-questions does not read",
			},
			{
				name:    "should refuse --id with -i text",
				set:     values{"input": "text"},
				typed:   []settingrow.Name{"id"},
				wantErr: "--id applies to streaming input. -i text reads one record",
			},
			{
				name:  "should accept --id with streaming input",
				set:   values{"input": "jsonl", "jobs": 1},
				typed: []settingrow.Name{"id"},
			},
			{
				name:     "should accept --abstain-if when the only assertion comes from the file",
				set:      values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}, "file": "q.yaml", "input": "text"},
				fromFile: []settingrow.Name{"assert"},
			},
			{
				name:     "should accept --stop-on=fail when the only assertion comes from the file",
				set:      values{"assert": []string{"answer.value > 0.5"}, "stop_on": "fail", "file": "q.yaml", "input": "jsonl"},
				fromFile: []settingrow.Name{"assert"},
			},
			{
				name:    "should refuse --resume with --print-request",
				set:     values{"input": "jsonl", "resume": true, "out": "a.jsonl", "print_request": true, "jobs": 1},
				typed:   []settingrow.Name{"id"},
				wantErr: "--print-request writes request bodies, not answers, so --resume has nothing to pick up. Drop --resume",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				given := run{
					set: tc.set, typed: tc.typed, fromEnv: tc.fromEnv, fromFile: tc.fromFile, events: tc.events,
					positional: tc.positional, facts: tc.facts,
				}

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
				wantErr: "--delimiter does not apply to -i request, which sends each body as written and prints each raw response",
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
					typed: []settingrow.Name{"input", "delimiter"},
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

	t.Run("should refuse a typed calibrate -o beside --print-request whatever its value", func(t *testing.T) {
		t.Parallel()

		given := run{
			facts: settings.Facts{Command: settingrow.CommandCalibrate},
			set:   values{"print_request": true},
			typed: []settingrow.Name{"output"},
		}

		_, _, err := settings.Validate(given.view(t), []settings.Rule{ruleByID(t, "calibrate-report")})
		if want := "-o does not apply to --print-request, which writes the request bodies and sends none"; err == nil || err.Error() != want {
			t.Errorf("error = %v, want %q", err, want)
		}
	})

	t.Run("should accept calibrate's -o table, which is its report, on records", func(t *testing.T) {
		t.Parallel()

		given := run{facts: settings.Facts{Command: settingrow.CommandCalibrate}, set: values{"input": "jsonl", "output": "table"}}

		if _, _, err := settings.Validate(given.view(t), []settings.Rule{ruleByID(t, "table-reads-one-record")}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("should leave calibrate's rules to calibrate", func(t *testing.T) {
		t.Parallel()

		given := run{facts: settings.Facts{Command: settingrow.CommandRoot}, positional: "is this urgent", planned: true}

		if _, _, err := settings.Validate(given.view(t), settings.Rules()); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("should leave a body's state to the run, which calibrate only warns about", func(t *testing.T) {
		t.Parallel()

		given := run{
			facts: settings.Facts{Command: settingrow.CommandCalibrate, BodyState: true},
			set:   values{"file": "body.json"}, typed: []settingrow.Name{"state"},
		}

		if _, _, err := settings.Validate(given.view(t), []settings.Rule{ruleByID(t, "one-state")}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("should refuse each setting --compare cannot run with", func(t *testing.T) {
		t.Parallel()

		calibrating := settings.Facts{Command: settingrow.CommandCalibrate}
		files := []string{"march.jsonl", "november.jsonl"}
		compared := func(set values, typed ...settingrow.Name) run {
			merged := values{"compare": files}
			maps.Copy(merged, set)

			return run{facts: calibrating, set: merged, typed: append([]settingrow.Name{"compare", "id"}, typed...)}
		}

		for _, refused := range []struct {
			given run
			want  string
		}{
			{
				given: run{facts: calibrating, set: values{"false_alarms": "0.1"}, typed: []settingrow.Name{"false_alarms"}},
				want:  "--false-alarms sets the budgets of a --compare report. Pass --compare or drop it",
			},
			{
				given: compared(values{"compare": []string{"a.jsonl", "b.jsonl", "a.jsonl"}}),
				want:  "--compare names a.jsonl twice",
			},
			{
				given: run{facts: calibrating, set: values{"compare": files}, typed: []settingrow.Name{"compare"}},
				want:  "--compare pairs the records with each file's answers by id. Pass --id",
			},
			{
				given: compared(values{"mock": "m.json"}, "mock"),
				want:  "--compare reads saved answers, and --mock answers from fixtures. Drop one",
			},
			{given: compared(values{"out": "x.jsonl"}, "out"), want: "so --out does not apply"},
			{given: compared(values{"resume": true}, "resume"), want: "so --resume does not apply"},
			{given: compared(values{"offline": true}, "offline"), want: "so --offline does not apply"},
			{given: compared(values{"prune": true}, "prune"), want: "so --prune does not apply"},
			{given: compared(values{"print_request": true}, "print_request"), want: "so --print-request does not apply"},
			{given: compared(values{"cache": true}, "cache"), want: "so --cache does not apply"},
			{given: compared(values{"usage": true}, "usage"), want: "--compare shows usage whenever the files carry it, so --usage does not apply. Drop it"},
			{given: compared(values{"model": "jev-latest"}, "model"), want: "so -m/--model does not apply"},
			{given: compared(values{"provider": "openrouter"}, "provider"), want: "so --provider does not apply"},
			{given: compared(values{"endpoint": "https://x.example"}, "endpoint"), want: "so --base-url does not apply"},
			{given: compared(values{"long_context": true}, "long_context"), want: "so --long-context does not apply"},
			{given: compared(values{"jobs": 4}, "jobs"), want: "so -j/--jobs does not apply"},
			{given: compared(values{"stats": true}, "stats"), want: "so --stats does not apply"},
			{
				given: compared(values{"compare": []string{"a.jsonl", "./a.jsonl"}}),
				want:  "--compare names ./a.jsonl twice",
			},
		} {
			_, _, err := settings.Validate(refused.given.view(t), []settings.Rule{ruleByID(t, "compare-reads-files")})
			if err == nil || !strings.Contains(err.Error(), refused.want) {
				t.Errorf("error = %v, want %q", err, refused.want)
			}
		}
	})

	t.Run("should cover every rule in Rules that refuses", func(t *testing.T) {
		t.Parallel()

		var covered []string
		for _, tc := range ruleCases() {
			covered = append(covered, tc.rule)
		}

		for _, rule := range settings.Rules() {
			refuses := rule.Check != nil || rule.Inspect != nil
			if refuses && !slices.Contains(covered, rule.ID) {
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
					if rule.Check == nil && rule.Notice == nil && rule.Inspect == nil {
						t.Errorf("rule %s has no Check, Warn, Notice or Inspect", rule.ID)
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
	calibrating := settings.Facts{Command: settingrow.CommandCalibrate}

	return []ruleCase{
		{
			rule:    "calibrate-reads-records",
			refused: run{facts: calibrating},
			passed:  run{facts: calibrating, set: values{"input": "jsonl"}, typed: []settingrow.Name{"input"}},
			wantErr: "calibrate needs -i jsonl, csv or tsv, since only a record has fields to label",
		},
		{
			rule:    "offline-reads-out",
			refused: run{facts: calibrating, set: values{"offline": true, "print_request": true}},
			passed:  run{facts: calibrating, set: values{"offline": true, "out": "answers.jsonl", "resume": true}},
			wantErr: "--offline reads answers, and --print-request sends no request. Drop one",
		},
		{
			rule: "compare-reads-files",
			refused: run{
				facts: calibrating, set: values{"compare": []string{"a.jsonl"}}, typed: []settingrow.Name{"compare"},
			},
			passed: run{
				facts: calibrating, set: values{"compare": []string{"a.jsonl", "b.jsonl"}},
				typed: []settingrow.Name{"compare", "id"},
			},
			wantErr: "--compare needs two answers files or more, the first being the baseline, and got only a.jsonl",
		},
		{
			rule: "change-needs-compare",
			refused: run{
				facts: calibrating, set: values{"require": []string{"lower(u.catches_change) >= -0.1 at 0.5"}},
				typed: []settingrow.Name{"require"},
			},
			passed: run{
				facts: calibrating,
				set:   values{"require": []string{"lower(u.catches_change) >= -0.1 at 0.5"}, "compare": []string{"a.jsonl", "b.jsonl"}},
				typed: []settingrow.Name{"require", "compare", "id"},
			},
			wantErr: "--require 'lower(u.catches_change) >= -0.1 at 0.5' reads catches_change, a change from a baseline, which needs --compare",
		},
		{
			rule:    "false-alarms-parse",
			refused: run{facts: calibrating, set: values{"false_alarms": "1.5"}, typed: []settingrow.Name{"false_alarms"}},
			passed:  run{facts: calibrating, set: values{"false_alarms": "0.05,0.1"}, typed: []settingrow.Name{"false_alarms"}},
			wantErr: "--false-alarms takes numbers between 0 and 1, got '1.5'",
		},
		{
			rule:    "calibrate-needs-map",
			refused: run{facts: calibrating},
			passed:  run{facts: calibrating, typed: []settingrow.Name{"map"}},
			wantErr: "calibrate needs --map. Without it the whole record, label included, " +
				"would be the state, and the report would flatter the question",
		},
		{
			rule:    "calibrate-report",
			refused: run{facts: calibrating, set: values{"output": "csv"}},
			passed:  run{facts: calibrating, set: values{"output": "json"}},
			wantErr: "calibrate -o takes table, json or auto, got csv",
		},
		{
			rule:    "cuts-parse",
			refused: run{facts: calibrating, set: values{"cuts": " "}, typed: []settingrow.Name{"cuts"}},
			passed:  run{facts: calibrating, set: values{"cuts": "0.5"}, typed: []settingrow.Name{"cuts"}},
			wantErr: "--cuts takes a comma separated list of numbers between 0 and 1, got nothing",
		},
		{
			rule:    "calibrate-resumes-by-id",
			refused: run{facts: calibrating, set: values{"resume": true}},
			passed:  run{facts: calibrating, set: values{"resume": true}, typed: []settingrow.Name{"id"}},
			wantErr: "calibrate resumes by id, since which records are asked follows the labels. Pass --id",
		},
		{
			rule:    "calibrate-dry-run-out",
			refused: run{facts: calibrating, set: values{"print_request": true, "out": "answers.jsonl"}},
			passed: run{
				facts: calibrating, set: values{"print_request": true, "out": "answers.jsonl", "resume": true},
			},
			wantErr: "--print-request writes request bodies to stdout, so --out has no answers to hold. Drop --out",
		},
		{
			rule:    "calibrate-usage",
			refused: run{facts: calibrating, set: values{"usage": true}},
			passed:  run{facts: calibrating, set: values{"usage": true, "output": "json"}},
			wantErr: "--usage adds token counts to the answers file or the json report, and this run writes neither",
		},
		{
			rule:    "requires-parse",
			refused: run{facts: calibrating, set: values{"require": []string{"nonsense"}}},
			passed:  run{facts: calibrating, set: values{"require": []string{"urgent.catches >= 0.9"}}},
			wantErr: "--require 'nonsense' needs an operator, >=, >, <= or <, in 'nonsense'",
		},
		{
			rule:    "needs-a-question",
			refused: run{planned: true},
			passed:  run{planned: true, positional: "is this urgent"},
			wantErr: "no question given. Pass a question, --ask, or -f",
		},
		{
			rule:    "list-models-alone",
			refused: run{set: values{"list_models": true}, typed: []settingrow.Name{"model"}},
			passed:  run{set: values{"list_models": true, "stats": true}},
			wantErr: "-m does not apply to --list-models, which only lists the models a provider serves",
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
			passed:  run{set: values{"prune": true, "resume": true}, typed: []settingrow.Name{"id"}},
			wantErr: "--prune applies to --resume, which was not given",
		},
		{
			rule:    "dry-run-and-request",
			refused: run{set: values{"print_request": true, "mock": "answers.jsonl"}, typed: []settingrow.Name{"mock"}},
			passed:  run{set: values{"print_request": true, "input": "request", "stop_on": "error"}},
			wantErr: "--mock does not apply to --print-request, which writes the request bodies and sends none",
		},
		{
			rule:    "cache-beside-mock",
			refused: run{set: values{"cache": true, "mock": "m.json"}, typed: []settingrow.Name{"cache", "mock"}},
			passed:  run{set: values{"cache": true, "mock": "m.json"}, typed: []settingrow.Name{"mock"}, fromEnv: []settingrow.Name{"cache"}},
			wantErr: "--cache stores answers from the API, which --mock replaces. Drop one",
		},
		{
			rule:    "delimiter-needs-csv",
			refused: run{set: values{"delimiter": ";", "input": "jsonl"}, typed: []settingrow.Name{"delimiter"}},
			passed:  run{set: values{"delimiter": ";", "input": "csv"}, typed: []settingrow.Name{"delimiter"}},
			wantErr: "--delimiter applies to -i csv and -o csv. -i jsonl has no delimiter",
		},
		{
			rule:    "abstain-needs-assert",
			refused: run{set: values{"abstain_if": []string{"answer.value > 0.4"}}},
			passed: run{
				set:      values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}},
				fromFile: []settingrow.Name{"assert"},
			},
			wantErr: "--abstain-if needs --assert, since without one every record ends answered",
		},
		{
			rule:    "quiet-without-markdown",
			refused: run{set: values{"quiet": true, "output": "markdown"}},
			passed:  run{set: values{"quiet": true}},
			wantErr: "-q suppresses output, which leaves -o markdown nothing to write",
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
			rule:    "streaming-flags-need-a-stream",
			refused: run{set: values{"unordered": true}},
			passed:  run{set: values{"unordered": true, "input": "jsonl"}},
			wantErr: "--unordered applies to streaming input. -i text reads one record",
		},
		{
			rule:    "stream-reads-its-own-state",
			refused: run{set: values{"input": "jsonl"}, typed: []settingrow.Name{"state"}},
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
			rule:    "stop-on-verdict-needs-gate",
			refused: run{set: values{"stop_on": "fail"}},
			passed:  run{set: values{"stop_on": "fail", "assert": []string{"answer.value > 0.5"}}},
			wantErr: "--stop-on=fail needs --assert, since without one no record can fail",
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
			refused: run{planned: true, set: values{"input": "jsonl"}, events: []argv.Event{{Name: "ask", Value: "line=q"}}},
			passed:  run{planned: true, set: values{"input": "jsonl", "merge": true}, events: []argv.Event{{Name: "ask", Value: "line=q"}}},
			wantErr: "question id 'line' is reserved under -i jsonl, which writes the input line number as line",
		},
		{
			rule: "file-question-shape",
			refused: run{
				planned: true, file: bodyQuestions(1), events: []argv.Event{{Name: "pick", Value: "x,y"}},
			},
			passed: run{
				planned: true, file: bodyQuestions(1), events: []argv.Event{{Name: "fallback", Value: "no"}},
			},
			wantErr: "--pick cannot reshape a request body's question. A body carries its own type and criteria",
		},
		{
			rule: "for-selects-a-file-question",
			refused: run{
				planned: true, file: fileNouls("urgent"), set: values{"file": "one.yaml"},
				events: []argv.Event{{Name: "for", Value: "nope"}},
			},
			passed: run{
				planned: true, file: fileNouls("urgent"), set: values{"file": "one.yaml"},
				events: []argv.Event{{Name: "for", Value: "urgent"}},
			},
			wantErr: "--for nope names no question in one.yaml, which has: 'urgent'. " +
				"Define a new question with --ask nope=QUESTION",
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
			rule: "policy-binding",
			refused: run{
				planned: true, file: bodyQuestions(2), events: []argv.Event{{Name: "threshold", Value: "0.8"}},
			},
			passed: run{
				planned: true, file: bodyQuestions(1), events: []argv.Event{{Name: "threshold", Value: "0.8"}},
			},
			wantErr: "--threshold applies to one question, and 2 were asked: 'b0', 'b1'. " +
				"Put it after --for ID to set one, as in --for b0 --threshold 0.8",
		},
		{
			rule:    "quiet-needs-one-question",
			refused: run{planned: true, set: values{"quiet": true}, events: two},
			passed:  run{planned: true, set: values{"quiet": true, "assert": []string{"answer.value > 0.5"}}, events: two},
			wantErr: "-q needs a single question, or --assert to gate several. 'a', 'b' were asked",
		},
		{
			rule:    "raw-needs-one-question",
			refused: run{planned: true, set: values{"output": "raw"}, events: two},
			passed:  run{planned: true, set: values{"output": "raw"}, positional: "is this urgent"},
			wantErr: "-o raw needs a single question. 'a', 'b' were asked",
		},
		{
			rule: "one-state",
			refused: run{
				facts: settings.Facts{BodyState: true}, set: values{"file": "body.json"}, typed: []settingrow.Name{"state"},
			},
			passed:  run{facts: settings.Facts{BodyState: true}, set: values{"file": "body.json"}},
			wantErr: "--state and the state in body.json both give a state. Drop one",
		},
		{
			rule:    "quiet-writes-nothing",
			refused: run{set: values{"quiet": true, "output": "raw"}},
			passed:  run{set: values{"quiet": true}},
			wantErr: "-q suppresses output, which leaves -o raw nothing to write",
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
	typed      []settingrow.Name
	fromEnv    []settingrow.Name
	fromFile   []settingrow.Name
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

	for _, name := range slices.Concat(r.typed, r.fromEnv, r.fromFile) {
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

func (r run) sourceOf(name settingrow.Name) settings.Source {
	switch {
	case slices.Contains(r.typed, name):
		return settings.SourceFlag
	case slices.Contains(r.fromEnv, name):
		return settings.SourceEnv
	case slices.Contains(r.fromFile, name):
		return settings.SourceFile
	default:
		return settings.SourceDefault
	}
}

type values = map[settingrow.Name]any

func typedGiven(t *testing.T, args ...string) *settings.Given {
	t.Helper()

	root := register(settingrow.CommandRoot)
	root.parse(t, args...)

	return root.given
}

func bodyQuestions(n int) []plan.Question {
	questions := make([]plan.Question, 0, n)
	for i := range n {
		questions = append(questions, plan.Question{
			ID: "b" + strconv.Itoa(i), Shape: plan.Noul, Instructions: "q", Origin: plan.OriginBody,
		})
	}

	return questions
}

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

func fileNouls(ids ...string) []plan.Question {
	questions := make([]plan.Question, 0, len(ids))
	for _, id := range ids {
		questions = append(questions, plan.Question{ID: id, Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile})
	}

	return questions
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
