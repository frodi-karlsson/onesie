package plan_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/argv"
	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	readFile := func(string) ([]byte, error) { return nil, nil }

	tests := []struct {
		name         string
		events       []argv.Event
		file         []plan.Question
		positional   string
		cfg          plan.Config
		wantErr      string
		wantExact    string
		wantWarning  string
		wantFallback *bool
	}{
		{
			name:    "should reject an empty invocation",
			wantErr: "jev: no question given. Pass a question, --ask, or -f",
		},
		{
			name:       "should reject a positional combined with ask",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "ask", Value: "a=first"}},
			wantErr:    "jev: a positional question cannot be combined with --ask or -f",
		},
		{
			name:       "should reject a positional combined with a file question",
			positional: "is this urgent",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Origin: plan.OriginFile},
			},
			wantErr: "jev: a positional question cannot be combined with --ask or -f",
		},
		{
			name:    "should reject a reserved id",
			events:  []argv.Event{{Name: "ask", Value: "usage=first"}},
			wantErr: "jev: question id 'usage' is reserved",
		},
		{
			name:    "should reject the positional id used explicitly",
			events:  []argv.Event{{Name: "ask", Value: "answer=first"}},
			wantErr: "jev: question id 'answer' is reserved",
		},
		{
			name:    "should reject an id beginning with two underscores",
			events:  []argv.Event{{Name: "ask", Value: "__private=first"}},
			wantErr: "jev: question id '__private' is reserved",
		},
		{
			name: "should reject a single option pick",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "safe"},
			},
			wantErr: "jev: --pick needs at least two options in question 'a', got 1: safe",
		},
		{
			name: "should reject a single level rate",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "rate", Value: "calm"},
			},
			wantErr: "jev: --rate needs at least two levels in question 'a', got 1: calm",
		},
		{
			name: "should reject a duplicate option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,technical,billing"},
			},
			wantErr: "jev: --pick option 'billing' is listed twice in question 'team'",
		},
		{
			name: "should reject a duplicate level",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,high,high"},
			},
			wantErr: "jev: --rate label 'high' is listed twice in question 'severity'",
		},
		{
			name: "should reject pick and rate together",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "rate", Value: "p,q"},
			},
			wantErr: "jev: --pick and --rate are mutually exclusive",
		},
		{
			name: "should reject a partly described rubric",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "minor,major,critical"},
				{Name: "desc", Value: "minor=small"},
			},
			wantErr: "jev: --rate levels must all be described or all bare in question 'severity'. " +
				"Described minor but not major, critical",
		},
		{
			name: "should reject a desc naming an unknown option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,technical"},
				{Name: "desc", Value: "bilingl=typo"},
			},
			wantErr: "jev: --desc names an unknown key 'bilingl' in question 'team'. --pick has: billing, technical",
		},
		{
			name: "should reject a desc other than yes or no on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "desc", Value: "maybe=unclear"},
			},
			wantErr: "jev: --desc on a yes/no question takes 'yes' or 'no', got 'maybe'",
		},
		{
			name: "should reject min-confidence on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "min-confidence", Value: "0.7"},
				{Name: "fallback", Value: "true"},
			},
			wantErr: "jev: --min-confidence needs a confidence value. 'urgent' is a yes/no question",
		},
		{
			name: "should reject threshold on a pick question",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "threshold", Value: "0.5"},
			},
			wantErr: "jev: --threshold cuts a yes/no probability. 'team' has options",
		},
		{
			name: "should reject threshold on a rate question",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,high"},
				{Name: "threshold", Value: "0.5"},
			},
			wantErr: "jev: --threshold cuts a yes/no probability. 'severity' has levels",
		},
		{
			name: "should reject min-confidence with no fallback",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "0.7"},
			},
			wantErr: "jev: --min-confidence needs --fallback, nothing to substitute for 'team'",
		},
		{
			name: "should reject a threshold outside zero to one",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "threshold", Value: "85"},
			},
			wantErr: "jev: --threshold must be between 0 and 1, got 85",
		},
		{
			name: "should reject a min-confidence outside zero to one",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "1.5"},
				{Name: "fallback", Value: "human"},
			},
			wantErr: "jev: --min-confidence must be between 0 and 1, got 1.5",
		},
		{
			name: "should reject a non boolean fallback on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "fallback", Value: "human"},
			},
			wantErr: "jev: --fallback on a yes/no question takes true, false, yes or no, got 'human'",
		},
		{
			name: "should accept yes as a boolean fallback",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "fallback", Value: "YES"},
			},
			wantFallback: pointerTo(true),
		},
		{
			name: "should reject a shape flag with no ask to bind to",
			events: []argv.Event{
				{Name: "pick", Value: "x,y"},
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
				{Name: "ask", Value: "c=three"},
			},
			wantErr: "jev: --pick given with no --ask to bind to and 3 questions asked",
		},
		{
			name: "should reject raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			cfg:     plan.Config{Raw: true},
			wantErr: "jev: -r needs a single question. 'a', 'b' were asked",
		},
		{
			name: "should reject -o raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			cfg:     plan.Config{Output: "raw"},
			wantErr: "jev: -o raw needs a single question. 'a', 'b' were asked",
		},
		{
			name:       "should accept -o raw with a single question",
			positional: "is this urgent",
			cfg:        plan.Config{Output: "raw"},
		},
		{
			name: "should reject two questions sharing an id",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "a=two"},
			},
			wantErr: "jev: question id 'a' is given twice",
		},
		{
			name: "should reject quiet on a pick with no policy",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
			},
			cfg: plan.Config{Quiet: true},
			wantErr: "jev: -q on 'team' needs --min-confidence and --fallback. " +
				"Without a policy the exit code is always 0",
		},
		{
			name:       "should reject raw combined with output",
			positional: "is this urgent",
			cfg:        plan.Config{Raw: true, Output: "json"},
			wantErr:    "jev: -r and -o are mutually exclusive",
		},
		{
			name:       "should reject both state flags together",
			positional: "is this urgent",
			cfg:        plan.Config{HasState: true, HasStateFile: true},
			wantErr:    "jev: --state and --state-file are mutually exclusive",
		},
		{
			name:       "should warn about a partly described option set",
			positional: "which team",
			events: []argv.Event{
				{Name: "pick", Value: "billing,technical,sales"},
				{Name: "desc", Value: "billing=payments"},
				{Name: "desc", Value: "technical=bugs"},
			},
			wantWarning: "warning: 'answer' describes billing, technical but not sales",
		},
		{
			name:       "should accept a plain positional question",
			positional: "is this urgent",
		},
		{
			name: "should accept a fully described rubric",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "minor,major"},
				{Name: "desc", Value: "minor=small"},
				{Name: "desc", Value: "major=big"},
			},
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
			name:   "should reject a policy flag aimed at a two question body",
			file:   bodyQuestions(2),
			events: []argv.Event{{Name: "threshold", Value: "0.8"}},
			wantErr: "jev: policy flags apply to a request body only when it has one question. " +
				"the question file has 2",
		},
		{
			name: "should count only the body's own questions in the policy error",
			file: bodyQuestions(2),
			events: []argv.Event{
				{Name: "threshold", Value: "0.8"},
				{Name: "ask", Value: "extra=second"},
			},
			wantErr: "the question file has 2",
		},
		{
			name: "should reject policy carried by a body question itself",
			file: append(bodyQuestions(1), plan.Question{
				ID: "b2", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginBody,
				Policy: plan.Policy{Threshold: pointerTo(0.8)},
			}),
			wantErr: "the question file has 2",
		},
		{
			name:    "should name the body's file in the policy error",
			file:    bodyQuestions(2),
			events:  []argv.Event{{Name: "threshold", Value: "0.8"}},
			cfg:     plan.Config{FileName: "triage.json"},
			wantErr: "triage.json has 2",
		},
		{
			name:    "should reject the reserved assert id",
			events:  []argv.Event{{Name: "ask", Value: "assert=first"}},
			wantErr: "jev: question id 'assert' is reserved",
		},
		{
			name:       "should reject replace with no file given",
			positional: "is this urgent",
			cfg:        plan.Config{Replace: true},
			wantErr:    "jev: --replace applies to -f, which was not given",
		},
		{
			name: "should accept replace alongside a file",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile},
			},
			cfg: plan.Config{Replace: true, FileName: "triage.yaml"},
		},
		{
			name: "should report an empty pick without a trailing separator",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: ""},
			},
			wantExact: "jev: --pick needs at least two options in question 'a', got 1",
		},
		{
			name:      "should report an unlabelled body's level count without a list",
			file:      bodyLevels(1),
			wantExact: "jev: 'criteria' needs at least two levels in question 'bq', got 1",
		},
		{
			name: "should name criteria when a body's choice has one option",
			file: []plan.Question{{
				ID: "bq", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginBody,
				Options: []plan.Option{{Name: "only"}},
			}},
			wantExact: "jev: 'criteria' needs at least two options in question 'bq', got 1: only",
		},
		{
			name: "should not hold an unlabelled body to the all described or all bare rule",
			file: bodyLevels(2),
		},
		{
			name: "should accept an unlabelled body whose levels share the empty label",
			file: bodyLevels(3),
		},
		{
			name: "should not warn about a body's partly described options",
			file: []plan.Question{{
				ID: "bq", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginBody,
				Options: []plan.Option{{Name: "x", Desc: "X"}, {Name: "y"}},
			}},
		},
		{
			name:      "should name the pick key when a file question has one option",
			file:      filePick("only"),
			wantExact: "jev: 'pick' needs at least two options in question 'team', got 1: only",
		},
		{
			name: "should keep the flag spelling when the question was opened with ask",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "only"},
			},
			wantExact: "jev: --pick needs at least two options in question 'team', got 1: only",
		},
		{
			name:       "should omit the question from a positional pick's option count",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "pick", Value: "only"}},
			wantExact:  "jev: --pick needs at least two options, got 1: only",
		},
		{
			name:       "should omit the question from a positional rate's level count",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "rate", Value: "calm"}},
			wantExact:  "jev: --rate needs at least two levels, got 1: calm",
		},
		{
			name:      "should name the question when a file's pick has too many options",
			file:      filePick(generated("o", 256)...),
			wantExact: "jev: 'pick' takes at most 255 options in question 'team', got 256",
		},
		{
			name:       "should omit the question when a positional pick has too many options",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "pick", Value: strings.Join(generated("o", 256), ",")},
			},
			wantExact: "jev: --pick takes at most 255 options, got 256",
		},
		{
			name: "should name the question when a rate has too many levels",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: strings.Join(generated("l", 11), ",")},
			},
			wantExact: "jev: --rate takes at most 10 levels in question 'severity', got 11",
		},
		{
			name:       "should omit the question when a positional rate has too many levels",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "rate", Value: strings.Join(generated("l", 11), ",")},
			},
			wantExact: "jev: --rate takes at most 10 levels, got 11",
		},
		{
			name:       "should omit the question from a positional mixed rubric",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "rate", Value: "minor,major"},
				{Name: "desc", Value: "minor=small"},
			},
			wantExact: "jev: --rate levels must all be described or all bare. " +
				"Described minor but not major",
		},
		{
			name:      "should name the pick key for a file's duplicate option",
			file:      filePick("billing", "billing"),
			wantExact: "jev: 'pick' option 'billing' is listed twice in question 'team'",
		},
		{
			name:   "should name the pick key in a file question's desc vocabulary",
			file:   filePick("billing", "technical"),
			events: []argv.Event{{Name: "desc", Value: "bilingl=x"}},
			wantExact: "jev: --desc names an unknown key 'bilingl' in question 'team'. " +
				"'pick' has: billing, technical",
		},
		{
			name: "should name the rate key for a file's mixed rubric",
			file: []plan.Question{{
				ID: "severity", Shape: plan.Rate, Instructions: "q", Origin: plan.OriginFile,
				Labelled: true,
				Levels: []plan.Level{
					{Label: "minor"}, {Label: "major", Desc: "bad"},
				},
			}},
			wantExact: "jev: 'rate' levels must all be described or all bare in question " +
				"'severity'. Described major but not minor",
		},
		{
			name: "should name the file's keys when min_confidence has no fallback",
			file: withPolicy(filePick("billing", "technical"), plan.Policy{
				MinConfidence: pointerTo(0.7),
			}),
			wantExact: "jev: 'min_confidence' needs 'fallback', nothing to substitute for 'team'",
		},
		{
			name: "should name the file's keys in the threshold remedy",
			file: withPolicy(filePick("billing", "technical"), plan.Policy{
				Threshold: pointerTo(0.8),
			}),
			wantExact: "jev: 'threshold' cuts a yes/no probability. 'team' has options, " +
				"use 'min_confidence' with 'fallback'",
		},
		{
			name: "should name the file's keys when min_confidence lands on a yes/no question",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{MinConfidence: pointerTo(0.7)},
			}},
			wantExact: "jev: 'min_confidence' needs a confidence value. 'urgent' is a yes/no " +
				"question, use 'threshold', or add 'pick' or 'rate'",
		},
		{
			name: "should name the threshold key when a file's value is out of range",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{Threshold: pointerTo(85.0)},
			}},
			wantExact: "jev: 'threshold' must be between 0 and 1, got 85",
		},
		{
			name: "should name the fallback key when a file's yes/no value is not a boolean",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{Fallback: &plan.Fallback{Text: "human"}},
			}},
			wantExact: "jev: 'fallback' on a yes/no question takes true, false, yes or no, " +
				"got 'human'",
		},
		{
			name: "should name the file's keys in the quiet policy remedy",
			file: filePick("billing", "technical"),
			cfg:  plan.Config{Quiet: true},
			wantExact: "jev: -q on 'team' needs 'min_confidence' and 'fallback'. " +
				"Without a policy the exit code is always 0",
		},
		{
			name:       "should reject unordered outside a stream",
			positional: "is this urgent",
			cfg:        plan.Config{Unordered: true, InputName: "text"},
			wantErr:    "--unordered applies to streaming input. -i text reads one record",
		},
		{
			name:       "should reject stop on error outside a stream",
			positional: "is this urgent",
			cfg:        plan.Config{StopOnError: true, InputName: "json"},
			wantErr:    "--stop-on-error applies to streaming input. -i json reads one record",
		},
		{
			name:       "should reject skip blank outside a stream",
			positional: "is this urgent",
			cfg:        plan.Config{SkipBlank: true, InputName: "text"},
			wantErr:    "--skip-blank applies to streaming input",
		},
		{
			name:       "should accept unordered in a stream",
			positional: "is this urgent",
			cfg:        plan.Config{Unordered: true, Streaming: true, InputName: "lines"},
		},
		{
			name:       "should reject state with a streaming mode",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, HasState: true, InputName: "jsonl"},
			wantErr:    "--state cannot be combined with -i jsonl",
		},
		{
			name:       "should reject state-file with a streaming mode naming the flag used",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, HasStateFile: true, InputName: "lines"},
			wantErr:    "--state-file cannot be combined with -i lines",
		},
		{
			name:       "should reject merge with raw output",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, Merge: true, Output: "raw", InputName: "lines"},
			wantErr:    "--merge needs -o json or -o values",
		},
		{
			name:       "should reject merge with table output outside a stream too",
			positional: "is this urgent",
			cfg:        plan.Config{Merge: true, Output: "table", InputName: "text"},
			wantErr:    "--merge needs -o json or -o values",
		},
		{
			name:       "should reject merge with the -r spelling of raw",
			positional: "is this urgent",
			cfg:        plan.Config{Merge: true, Raw: true, InputName: "text"},
			wantErr:    "--merge needs -o json or -o values",
		},
		{
			name:       "should accept merge with values output",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, Merge: true, Output: "values", InputName: "jsonl"},
		},
		{
			name:       "should reject quiet in a stream",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, Quiet: true, InputName: "jsonl"},
			wantErr:    "-q reads one record. Drop -i jsonl",
		},
		{
			name:       "should reject a table in a stream",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, Output: "table", InputName: "lines"},
			wantErr:    "-o table reads one record. Drop -i lines or use -o json",
		},
		{
			name:       "should accept a table outside a stream",
			positional: "is this urgent",
			cfg:        plan.Config{Output: "table", InputName: "text"},
		},
		{
			name:        "should warn about jobs outside a stream",
			positional:  "is this urgent",
			cfg:         plan.Config{Jobs: 8, JobsSet: true, InputName: "text"},
			wantWarning: "-j 8 ignored. -i text reads one record",
		},
		{
			name:       "should reject a zero job count",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, Jobs: 0, JobsSet: true, InputName: "lines"},
			wantErr:    "-j takes a positive number",
		},
		{
			name:       "should reject a negative job count",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, Jobs: -2, JobsSet: true, InputName: "lines"},
			wantErr:    "-j takes a positive number",
		},
		{
			name:       "should accept a config that never set jobs",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, InputName: "lines"},
		},
		{
			name:       "should reject a timeout of zero",
			positional: "is this urgent",
			cfg:        plan.Config{Timeout: 0, TimeoutSet: true, InputName: "text"},
			wantErr:    "jev: --timeout takes a positive number of seconds, got 0",
		},
		{
			name:       "should reject a negative retry count",
			positional: "is this urgent",
			cfg:        plan.Config{Retries: -1, RetriesSet: true, InputName: "text"},
			wantErr:    "jev: --retries takes a retry count of zero or more, got -1",
		},
		{
			name:       "should reject a max retry after of zero",
			positional: "is this urgent",
			cfg:        plan.Config{MaxRetryAfter: 0, MaxRetryAfterSet: true, InputName: "text"},
			wantErr:    "jev: --max-retry-after must be a positive number of seconds, got 0",
		},
		{
			name:       "should reject a negative max retry after",
			positional: "is this urgent",
			cfg:        plan.Config{MaxRetryAfter: -5, MaxRetryAfterSet: true, InputName: "text"},
			wantErr:    "jev: --max-retry-after must be a positive number of seconds, got -5",
		},
		{
			name:       "should reject a timeout above the ceiling",
			positional: "is this urgent",
			cfg:        plan.Config{Timeout: 86401, TimeoutSet: true, InputName: "text"},
			wantErr:    "jev: --timeout takes at most 86400 seconds, got 86401",
		},
		{
			name:       "should reject a timeout that would wrap a duration to a fraction",
			positional: "is this urgent",
			cfg:        plan.Config{Timeout: 18446744074, TimeoutSet: true, InputName: "text"},
			wantErr:    "jev: --timeout takes at most 86400 seconds, got 18446744074",
		},
		{
			name:       "should reject a max retry after above the ceiling",
			positional: "is this urgent",
			cfg: plan.Config{
				MaxRetryAfter: 86401, MaxRetryAfterSet: true, InputName: "text",
			},
			wantErr: "jev: --max-retry-after takes at most 86400 seconds, got 86401",
		},
		{
			name:       "should reject a max retry after that would wrap a duration to a fraction",
			positional: "is this urgent",
			cfg: plan.Config{
				MaxRetryAfter: 18446744074, MaxRetryAfterSet: true, InputName: "text",
			},
			wantErr: "jev: --max-retry-after takes at most 86400 seconds, got 18446744074",
		},
		{
			name:       "should accept a timeout at the ceiling",
			positional: "is this urgent",
			cfg:        plan.Config{Timeout: 86400, TimeoutSet: true, InputName: "text"},
		},
		{
			name:       "should accept a retry count of zero",
			positional: "is this urgent",
			cfg:        plan.Config{Retries: 0, RetriesSet: true, InputName: "text"},
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

			warnings, err := plan.Validate(built, tc.cfg)

			if tc.wantExact != "" {
				if err == nil {
					t.Fatalf("expected the error %q, got none", tc.wantExact)
				}

				if err.Error() != tc.wantExact {
					t.Errorf("error = %q\nwant %q", err.Error(), tc.wantExact)
				}

				return
			}

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got none", tc.wantErr)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q\nwant it to contain %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantFallback != nil {
				parsed := built.Questions[0].Policy.Fallback
				if parsed == nil {
					t.Fatal("fallback = nil, want it parsed onto the question")
				}

				if parsed.Boolean != *tc.wantFallback {
					t.Errorf("fallback boolean = %v, want %v", parsed.Boolean, *tc.wantFallback)
				}
			}

			if tc.wantWarning == "" {
				if len(warnings) != 0 {
					t.Errorf("unexpected warnings: %v", warnings)
				}

				return
			}

			joined := strings.Join(warnings, "\n")
			if !strings.Contains(joined, tc.wantWarning) {
				t.Errorf("warnings = %q\nwant one containing %q", joined, tc.wantWarning)
			}
		})
	}
}

func pointerTo[T any](value T) *T {
	return &value
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

func generated(prefix string, n int) []string {
	names := make([]string, 0, n)
	for i := range n {
		names = append(names, prefix+strconv.Itoa(i))
	}

	return names
}

func withPolicy(questions []plan.Question, policy plan.Policy) []plan.Question {
	questions[0].Policy = policy

	return questions
}

func bodyLevels(n int) []plan.Question {
	levels := make([]plan.Level, 0, n)
	for i := range n {
		// Only the first level is described, which is the mixed rubric a labelled question is
		// rejected for.
		if i == 0 {
			levels = append(levels, plan.Level{Desc: "x"})

			continue
		}

		levels = append(levels, plan.Level{})
	}

	return []plan.Question{{
		ID: "bq", Shape: plan.Rate, Instructions: "q", Origin: plan.OriginBody,
		Levels: levels,
	}}
}

func bodyQuestions(n int) []plan.Question {
	questions := make([]plan.Question, 0, n)
	for i := range n {
		questions = append(questions, plan.Question{
			ID:           "b" + strconv.Itoa(i),
			Shape:        plan.Noul,
			Instructions: "q",
			Origin:       plan.OriginBody,
		})
	}

	return questions
}
