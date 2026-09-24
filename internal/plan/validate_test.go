package plan_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
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
			wantErr: "onesie: no question given. Pass a question, --ask, or -f",
		},
		{
			name:       "should reject a positional combined with ask",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "ask", Value: "a=first"}},
			wantErr:    "onesie: a positional question cannot be combined with --ask or -f",
		},
		{
			name:       "should reject a positional combined with a file question",
			positional: "is this urgent",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Origin: plan.OriginFile},
			},
			wantErr: "onesie: a positional question cannot be combined with --ask or -f",
		},
		{
			name:    "should reject a reserved id",
			events:  []argv.Event{{Name: "ask", Value: "usage=first"}},
			wantErr: "onesie: question id 'usage' is reserved",
		},
		{
			name:    "should reject the positional id used explicitly",
			events:  []argv.Event{{Name: "ask", Value: "answer=first"}},
			wantErr: "onesie: question id 'answer' is reserved",
		},
		{
			name:    "should reject an id beginning with two underscores",
			events:  []argv.Event{{Name: "ask", Value: "__private=first"}},
			wantErr: "onesie: question id '__private' is reserved",
		},
		{
			name: "should reject a single option pick",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "safe"},
			},
			wantErr: "onesie: --pick needs at least two options in question 'a', got 1: safe",
		},
		{
			name: "should reject a single level rate",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "rate", Value: "calm"},
			},
			wantErr: "onesie: --rate needs at least two levels in question 'a', got 1: calm",
		},
		{
			name: "should reject a duplicate option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,technical,billing"},
			},
			wantErr: "onesie: --pick option 'billing' is listed twice in question 'team'",
		},
		{
			name: "should reject a duplicate level",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,high,high"},
			},
			wantErr: "onesie: --rate label 'high' is listed twice in question 'severity'",
		},
		{
			name: "should reject pick and rate together",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "rate", Value: "p,q"},
			},
			wantErr: "onesie: --pick and --rate are mutually exclusive",
		},
		{
			name: "should reject a partly described rubric",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "minor,major,critical"},
				{Name: "desc", Value: "minor=small"},
			},
			wantErr: "onesie: --rate levels must all be described or all bare in question 'severity'. " +
				"Described minor but not major, critical",
		},
		{
			name: "should reject a desc naming an unknown option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,technical"},
				{Name: "desc", Value: "bilingl=typo"},
			},
			wantErr: "onesie: --desc names an unknown key 'bilingl' in question 'team'. --pick has: billing, technical",
		},
		{
			name: "should reject a desc other than yes or no on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "desc", Value: "maybe=unclear"},
			},
			wantErr: "onesie: --desc on a yes/no question takes 'yes' or 'no', got 'maybe'",
		},
		{
			name: "should reject min-confidence on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "min-confidence", Value: "0.7"},
				{Name: "fallback", Value: "true"},
			},
			wantErr: "onesie: --min-confidence needs a confidence value. 'urgent' is a yes/no question",
		},
		{
			name: "should reject threshold on a pick question",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "threshold", Value: "0.5"},
			},
			wantErr: "onesie: --threshold cuts a yes/no probability. 'team' has options",
		},
		{
			name: "should reject threshold on a rate question",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,high"},
				{Name: "threshold", Value: "0.5"},
			},
			wantErr: "onesie: --threshold cuts a yes/no probability. 'severity' has levels",
		},
		{
			name: "should reject min-confidence with no fallback",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "0.7"},
			},
			wantErr: "onesie: --min-confidence needs --fallback, nothing to substitute for 'team'",
		},
		{
			name: "should reject a threshold outside zero to one",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "threshold", Value: "85"},
			},
			wantErr: "onesie: --threshold must be between 0 and 1, got 85",
		},
		{
			name: "should reject a min-confidence outside zero to one",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "1.5"},
				{Name: "fallback", Value: "human"},
			},
			wantErr: "onesie: --min-confidence must be between 0 and 1, got 1.5",
		},
		{
			name: "should reject a non boolean fallback on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "fallback", Value: "human"},
			},
			wantErr: "onesie: --fallback on a yes/no question takes true, false, yes or no, got 'human'",
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
			wantErr: "onesie: --pick given with no --ask to bind to and 3 questions asked",
		},
		{
			name: "should reject raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			cfg:     plan.Config{Raw: true},
			wantErr: "onesie: -r needs a single question. 'a', 'b' were asked",
		},
		{
			name: "should reject -o raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			cfg:     plan.Config{Output: "raw"},
			wantErr: "onesie: -o raw needs a single question. 'a', 'b' were asked",
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
			wantErr: "onesie: question id 'a' is given twice",
		},
		{
			name: "should reject quiet on a pick with no policy",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
			},
			cfg: plan.Config{Quiet: true},
			wantErr: "onesie: -q on 'team' needs --min-confidence and --fallback, or --assert. " +
				"Without one the exit code is always 0",
		},
		{
			name: "should accept quiet on a pick carrying only an assertion",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
			},
			cfg: plan.Config{Quiet: true, HasAssert: true},
		},
		{
			name:       "should reject raw combined with output",
			positional: "is this urgent",
			cfg:        plan.Config{Raw: true, Output: "json"},
			wantErr:    "onesie: -r and -o are mutually exclusive",
		},
		{
			name:       "should reject both state flags together",
			positional: "is this urgent",
			cfg:        plan.Config{HasState: true, HasStateFile: true},
			wantErr:    "onesie: --state and --state-file are mutually exclusive",
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
			wantErr: "onesie: policy flags apply to a request body only when it has one question. " +
				"the question file has 2, freeze with --print-questions first",
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
			wantErr: "onesie: question id 'assert' is reserved",
		},
		{
			name:    "should reject the reserved abstain_if id",
			events:  []argv.Event{{Name: "ask", Value: "abstain_if=first"}},
			wantErr: "onesie: question id 'abstain_if' is reserved",
		},
		{
			name:    "should reject the reserved abstain id",
			events:  []argv.Event{{Name: "ask", Value: "abstain=first"}},
			wantErr: "onesie: question id 'abstain' is reserved",
		},
		{
			name:    "should reject the reserved id id",
			events:  []argv.Event{{Name: "ask", Value: "id=first"}},
			wantErr: "onesie: question id 'id' is reserved",
		},
		{
			name:       "should reject replace with no file given",
			positional: "is this urgent",
			cfg:        plan.Config{Replace: true},
			wantErr:    "onesie: --replace applies to -f, which was not given",
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
			wantExact: "onesie: --pick needs at least two options in question 'a', got 1",
		},
		{
			name:      "should report an unlabelled body's level count without a list",
			file:      bodyLevels(1),
			wantExact: "onesie: 'criteria' needs at least two levels in question 'bq', got 1",
		},
		{
			name: "should name criteria when a body's choice has one option",
			file: []plan.Question{{
				ID: "bq", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginBody,
				Options: []plan.Option{{Name: "only"}},
			}},
			wantExact: "onesie: 'criteria' needs at least two options in question 'bq', got 1: only",
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
			wantExact: "onesie: 'pick' needs at least two options in question 'team', got 1: only",
		},
		{
			name: "should keep the flag spelling when the question was opened with ask",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "only"},
			},
			wantExact: "onesie: --pick needs at least two options in question 'team', got 1: only",
		},
		{
			name:       "should omit the question from a positional pick's option count",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "pick", Value: "only"}},
			wantExact:  "onesie: --pick needs at least two options, got 1: only",
		},
		{
			name:       "should omit the question from a positional rate's level count",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "rate", Value: "calm"}},
			wantExact:  "onesie: --rate needs at least two levels, got 1: calm",
		},
		{
			name:      "should name the question when a file's pick has too many options",
			file:      filePick(generated("o", 256)...),
			wantExact: "onesie: 'pick' takes at most 255 options in question 'team', got 256",
		},
		{
			name:       "should omit the question when a positional pick has too many options",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "pick", Value: strings.Join(generated("o", 256), ",")},
			},
			wantExact: "onesie: --pick takes at most 255 options, got 256",
		},
		{
			name: "should name the question when a rate has too many levels",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: strings.Join(generated("l", 11), ",")},
			},
			wantExact: "onesie: --rate takes at most 10 levels in question 'severity', got 11",
		},
		{
			name:       "should omit the question when a positional rate has too many levels",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "rate", Value: strings.Join(generated("l", 11), ",")},
			},
			wantExact: "onesie: --rate takes at most 10 levels, got 11",
		},
		{
			name:       "should omit the question from a positional mixed rubric",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "rate", Value: "minor,major"},
				{Name: "desc", Value: "minor=small"},
			},
			wantExact: "onesie: --rate levels must all be described or all bare. " +
				"Described minor but not major",
		},
		{
			name:      "should name the pick key for a file's duplicate option",
			file:      filePick("billing", "billing"),
			wantExact: "onesie: 'pick' option 'billing' is listed twice in question 'team'",
		},
		{
			name:   "should name the pick key in a file question's desc vocabulary",
			file:   filePick("billing", "technical"),
			events: []argv.Event{{Name: "desc", Value: "bilingl=x"}},
			wantExact: "onesie: --desc names an unknown key 'bilingl' in question 'team'. " +
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
			wantExact: "onesie: 'rate' levels must all be described or all bare in question " +
				"'severity'. Described major but not minor",
		},
		{
			name: "should name the file's keys when min_confidence has no fallback",
			file: withPolicy(filePick("billing", "technical"), plan.Policy{
				MinConfidence: pointerTo(0.7),
			}),
			wantExact: "onesie: 'min_confidence' needs 'fallback', nothing to substitute for 'team'",
		},
		{
			name: "should name the file's keys in the threshold remedy",
			file: withPolicy(filePick("billing", "technical"), plan.Policy{
				Threshold: pointerTo(0.8),
			}),
			wantExact: "onesie: 'threshold' cuts a yes/no probability. 'team' has options, " +
				"use 'min_confidence' with 'fallback'",
		},
		{
			name: "should name the file's keys when min_confidence lands on a yes/no question",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{MinConfidence: pointerTo(0.7)},
			}},
			wantExact: "onesie: 'min_confidence' needs a confidence value. 'urgent' is a yes/no " +
				"question, use 'threshold', or add 'pick' or 'rate'",
		},
		{
			name: "should name the threshold key when a file's value is out of range",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{Threshold: pointerTo(85.0)},
			}},
			wantExact: "onesie: 'threshold' must be between 0 and 1, got 85",
		},
		{
			name: "should name the fallback key when a file's yes/no value is not a boolean",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{Fallback: &plan.Fallback{Text: "human"}},
			}},
			wantExact: "onesie: 'fallback' on a yes/no question takes true, false, yes or no, " +
				"got 'human'",
		},
		{
			name: "should name the file's keys in the quiet policy remedy",
			file: filePick("billing", "technical"),
			cfg:  plan.Config{Quiet: true},
			wantExact: "onesie: -q on 'team' needs 'min_confidence' and 'fallback', or --assert. " +
				"Without one the exit code is always 0",
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
			name:       "should reject stop on assert outside a stream",
			positional: "is this urgent",
			cfg:        plan.Config{StopOnAssert: true, InputName: "text"},
			wantErr:    "--stop-on-assert applies to streaming input. -i text reads one record",
		},
		{
			name:       "should accept stop on assert in a stream",
			positional: "is this urgent",
			cfg:        plan.Config{StopOnAssert: true, Streaming: true, InputName: "jsonl"},
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
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should reject merge with table output outside a stream too",
			positional: "is this urgent",
			cfg:        plan.Config{Merge: true, Output: "table", InputName: "text"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should name --merge-key when that is the flag given",
			positional: "is this urgent",
			cfg: plan.Config{
				Merge:     true,
				MergeName: "--merge-key",
				Output:    "table",
				InputName: "text",
			},
			wantErr: "--merge-key needs -o json, values, csv or tsv",
		},
		{
			name:       "should reject merge with the -r spelling of raw",
			positional: "is this urgent",
			cfg:        plan.Config{Merge: true, Raw: true, InputName: "text"},
			wantErr:    "--merge needs -o json, values, csv or tsv",
		},
		{
			name:       "should accept merge with values output",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, Merge: true, Output: "values", InputName: "jsonl"},
		},
		{
			name:       "should accept merge with an explicit auto output",
			positional: "is this urgent",
			cfg: plan.Config{
				Streaming: true, Merge: true, Output: "auto", InputName: "jsonl",
			},
		},
		{
			name:       "should accept merge with json output",
			positional: "is this urgent",
			cfg: plan.Config{
				Streaming: true, Merge: true, Output: "json", InputName: "jsonl",
			},
		},
		{
			name:       "should accept merge with an absent output",
			positional: "is this urgent",
			cfg:        plan.Config{Streaming: true, Merge: true, InputName: "jsonl"},
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
			name:       "should report the warnings alongside a fatal error",
			positional: "which team",
			events: []argv.Event{
				{Name: "pick", Value: "billing"},
			},
			cfg:         plan.Config{Jobs: 8, JobsSet: true, InputName: "text"},
			wantErr:     "onesie: --pick needs at least two options, got 1: billing",
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
			wantErr:    "onesie: --timeout takes a positive number of seconds, got 0",
		},
		{
			name:       "should reject a negative retry count",
			positional: "is this urgent",
			cfg:        plan.Config{Retries: -1, RetriesSet: true, InputName: "text"},
			wantErr:    "onesie: --retries takes a retry count of zero or more, got -1",
		},
		{
			name:       "should reject a retry count above the ceiling",
			positional: "is this urgent",
			cfg:        plan.Config{Retries: 100001, RetriesSet: true, InputName: "text"},
			wantErr:    "onesie: --retries takes at most 100, got 100001",
		},
		{
			name:       "should accept a retry count at the ceiling",
			positional: "is this urgent",
			cfg:        plan.Config{Retries: 100, RetriesSet: true, InputName: "text"},
		},
		{
			name:       "should reject a max retry after of zero",
			positional: "is this urgent",
			cfg:        plan.Config{MaxRetryAfter: 0, MaxRetryAfterSet: true, InputName: "text"},
			wantErr:    "onesie: --max-retry-after must be a positive number of seconds, got 0",
		},
		{
			name:       "should reject a negative max retry after",
			positional: "is this urgent",
			cfg:        plan.Config{MaxRetryAfter: -5, MaxRetryAfterSet: true, InputName: "text"},
			wantErr:    "onesie: --max-retry-after must be a positive number of seconds, got -5",
		},
		{
			name:       "should reject a timeout above the ceiling",
			positional: "is this urgent",
			cfg:        plan.Config{Timeout: 86401, TimeoutSet: true, InputName: "text"},
			wantErr:    "onesie: --timeout takes at most 86400 seconds, got 86401",
		},
		{
			name:       "should reject a timeout that would wrap a duration to a fraction",
			positional: "is this urgent",
			cfg:        plan.Config{Timeout: 18446744074, TimeoutSet: true, InputName: "text"},
			wantErr:    "onesie: --timeout takes at most 86400 seconds, got 18446744074",
		},
		{
			name:       "should reject a max retry after above the ceiling",
			positional: "is this urgent",
			cfg: plan.Config{
				MaxRetryAfter: 86401, MaxRetryAfterSet: true, InputName: "text",
			},
			wantErr: "onesie: --max-retry-after takes at most 86400 seconds, got 86401",
		},
		{
			name:       "should reject a max retry after that would wrap a duration to a fraction",
			positional: "is this urgent",
			cfg: plan.Config{
				MaxRetryAfter: 18446744074, MaxRetryAfterSet: true, InputName: "text",
			},
			wantErr: "onesie: --max-retry-after takes at most 86400 seconds, got 18446744074",
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
			assertWarning(t, warnings, tc.wantWarning, tc.wantErr != "" || tc.wantExact != "")

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
		})
	}
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

func TestCheckFlags(t *testing.T) {
	t.Parallel()

	request := func(apply func(*plan.Config)) plan.Config {
		cfg := plan.Config{Streaming: true, RequestMode: true, InputName: "request", Jobs: 1}
		apply(&cfg)

		return cfg
	}

	tests := []struct {
		name    string
		cfg     plan.Config
		wantErr string
	}{
		{
			name:    "should reject -o csv with --merge over jsonl",
			cfg:     plan.Config{Streaming: true, InputName: "jsonl", Output: "csv", Merge: true, Jobs: 1},
			wantErr: "onesie: -o csv with --merge needs -i csv or -i tsv, since other input has no fixed columns",
		},
		{
			name: "should accept -o tsv with --merge over csv",
			cfg:  plan.Config{Streaming: true, InputName: "csv", Output: "tsv", Merge: true, Jobs: 1},
		},
		{
			name:    "should reject --merge-key with -o csv",
			cfg:     plan.Config{Streaming: true, InputName: "csv", Output: "csv", Merge: true, MergeName: "--merge-key", Jobs: 1},
			wantErr: "onesie: --merge-key does not apply to -o csv, which puts the answers in columns",
		},
		{
			name:    "should reject --usage with -o tsv",
			cfg:     plan.Config{Streaming: true, InputName: "csv", Output: "tsv", Usage: true, Jobs: 1},
			wantErr: "onesie: --usage does not apply to -o tsv, which has no column for it",
		},
		{
			name:    "should reject -j above the cap",
			cfg:     plan.Config{Streaming: true, InputName: "jsonl", Jobs: 2000000, JobsSet: true},
			wantErr: "onesie: -j takes at most 256 records in flight, got 2000000",
		},
		{
			name:    "should reject --resume without --out",
			cfg:     plan.Config{Streaming: true, InputName: "jsonl", Resume: true},
			wantErr: "onesie: --resume needs --out, the file it picks up from",
		},
		{
			name:    "should reject --resume on a single record",
			cfg:     plan.Config{InputName: "text", Resume: true, Out: "answers.txt"},
			wantErr: "onesie: --resume applies to streaming input. -i text reads one record",
		},
		{
			name:    "should reject --resume with --unordered",
			cfg:     plan.Config{Streaming: true, InputName: "jsonl", Resume: true, Out: "a.jsonl", Unordered: true},
			wantErr: "onesie: --resume relies on input order, which --unordered gives up. Pass --id to resume by id",
		},
		{
			name: "should accept --resume with --unordered when --id names the records",
			cfg: plan.Config{
				Streaming: true, InputName: "jsonl", Resume: true, Out: "a.jsonl", Unordered: true,
				HasID: true, Jobs: 1,
			},
		},
		{
			name:    "should reject --resume with --id and -r, which writes no id",
			cfg:     plan.Config{Streaming: true, InputName: "jsonl", Resume: true, Out: "a.txt", HasID: true, Raw: true},
			wantErr: "onesie: --resume with --id reads the id back from each line, which raw output leaves out. Use -o json, values, csv or tsv",
		},
		{
			name:    "should reject --resume with --id and -o raw, which writes no id",
			cfg:     plan.Config{Streaming: true, InputName: "jsonl", Resume: true, Out: "a.txt", HasID: true, Output: "raw"},
			wantErr: "onesie: --resume with --id reads the id back from each line, which raw output leaves out. Use -o json, values, csv or tsv",
		},
		{
			name:    "should reject --resume with --list-models",
			cfg:     plan.Config{ListModels: true, InputName: "text", Resume: true, Out: "m.txt"},
			wantErr: "onesie: --resume applies to streaming input, which --list-models does not read",
		},
		{
			name:    "should reject --resume with --print-questions",
			cfg:     plan.Config{PrintQuestions: true, InputName: "text", Resume: true, Out: "q.yaml"},
			wantErr: "onesie: --resume applies to streaming input, which --print-questions does not read",
		},
		{
			name: "should reject a positional question with -i request",
			cfg:  request(func(c *plan.Config) { c.HasPositional = true }),
			wantErr: "onesie: -i request carries its own questions. " +
				"Drop the question argument",
		},
		{
			name:    "should reject --ask with -i request",
			cfg:     request(func(c *plan.Config) { c.HasAsk = true }),
			wantErr: "onesie: -i request carries its own questions. Drop --ask",
		},
		{
			name: "should reject -f with -i request",
			cfg:  request(func(c *plan.Config) { c.FileName = "q.yaml" }),
			wantErr: "onesie: -f does not apply to -i request, " +
				"which carries its own questions",
		},
		{
			name:    "should reject --replace with -i request",
			cfg:     request(func(c *plan.Config) { c.Replace = true }),
			wantErr: "onesie: --replace applies to -f, which -i request does not accept",
		},
		{
			name:    "should reject --pick with -i request",
			cfg:     request(func(c *plan.Config) { c.GroupFlags = []string{"pick"} }),
			wantErr: "onesie: -i request carries its own questions. Drop --pick",
		},
		{
			name:    "should reject --rate with -i request",
			cfg:     request(func(c *plan.Config) { c.GroupFlags = []string{"rate"} }),
			wantErr: "onesie: -i request carries its own questions. Drop --rate",
		},
		{
			name:    "should reject --desc with -i request",
			cfg:     request(func(c *plan.Config) { c.GroupFlags = []string{"desc"} }),
			wantErr: "onesie: -i request carries its own questions. Drop --desc",
		},
		{
			name:    "should reject --sep with -i request",
			cfg:     request(func(c *plan.Config) { c.GroupFlags = []string{"sep"} }),
			wantErr: "onesie: -i request carries its own questions. Drop --sep",
		},
		{
			name: "should reject --threshold with -i request",
			cfg:  request(func(c *plan.Config) { c.GroupFlags = []string{"threshold"} }),
			wantErr: "onesie: --threshold does not apply to -i request, " +
				"which carries no policy",
		},
		{
			name: "should reject --min-confidence with -i request",
			cfg:  request(func(c *plan.Config) { c.GroupFlags = []string{"min-confidence"} }),
			wantErr: "onesie: --min-confidence does not apply to -i request, " +
				"which carries no policy",
		},
		{
			name: "should reject --fallback with -i request",
			cfg:  request(func(c *plan.Config) { c.GroupFlags = []string{"fallback"} }),
			wantErr: "onesie: --fallback does not apply to -i request, " +
				"which carries no policy",
		},
		{
			name: "should reject --state with -i request",
			cfg:  request(func(c *plan.Config) { c.HasState = true }),
			wantErr: "onesie: --state does not apply to -i request, " +
				"whose bodies carry their own state",
		},
		{
			name: "should reject --state-file with -i request",
			cfg:  request(func(c *plan.Config) { c.HasStateFile = true }),
			wantErr: "onesie: --state-file does not apply to -i request, " +
				"whose bodies carry their own state",
		},
		{
			name:    "should reject --map with -i request",
			cfg:     request(func(c *plan.Config) { c.HasMap = true }),
			wantErr: "onesie: --map does not apply to -i request, whose bodies carry their own state",
		},
		{
			name:    "should reject --map with --list-models",
			cfg:     plan.Config{ListModels: true, HasMap: true, InputName: "text"},
			wantErr: "onesie: --map does not apply to --list-models, which reads no state",
		},
		{
			name:    "should reject --map with --print-questions",
			cfg:     plan.Config{PrintQuestions: true, HasMap: true, InputName: "text"},
			wantErr: "onesie: --map does not apply to --print-questions, which reads no state",
		},
		{
			name: "should accept --map with --print-request",
			cfg:  plan.Config{PrintRequest: true, HasMap: true, Streaming: true, InputName: "jsonl", Jobs: 1},
		},
		{
			name:    "should reject --id with -i request",
			cfg:     request(func(c *plan.Config) { c.HasID = true }),
			wantErr: "onesie: --id does not apply to -i request, which forwards raw responses",
		},
		{
			name:    "should reject --id with --list-models",
			cfg:     plan.Config{ListModels: true, HasID: true, InputName: "text"},
			wantErr: "onesie: --id does not apply to --list-models, which reads no input",
		},
		{
			name:    "should reject --id with --print-questions",
			cfg:     plan.Config{PrintQuestions: true, HasID: true, InputName: "text"},
			wantErr: "onesie: --id does not apply to --print-questions, which reads no input",
		},
		{
			name:    "should reject --id with -i text",
			cfg:     plan.Config{HasID: true, InputName: "text"},
			wantErr: "onesie: --id applies to streaming input. -i text reads one record",
		},
		{
			name:    "should reject --id with -i json",
			cfg:     plan.Config{HasID: true, InputName: "json"},
			wantErr: "onesie: --id applies to streaming input. -i json reads one record",
		},
		{
			name: "should accept --id with streaming input",
			cfg:  plan.Config{HasID: true, Streaming: true, InputName: "jsonl", Jobs: 1},
		},
		{
			name: "should accept --id with --print-request",
			cfg:  plan.Config{PrintRequest: true, HasID: true, Streaming: true, InputName: "csv", Jobs: 1},
		},
		{
			name:    "should reject -o with -i request",
			cfg:     request(func(c *plan.Config) { c.Output = "json" }),
			wantErr: "onesie: -o does not apply to -i request, which forwards raw responses",
		},
		{
			name:    "should reject -r with -i request",
			cfg:     request(func(c *plan.Config) { c.Raw = true }),
			wantErr: "onesie: -r does not apply to -i request, which forwards raw responses",
		},
		{
			name:    "should reject -q with -i request",
			cfg:     request(func(c *plan.Config) { c.Quiet = true }),
			wantErr: "onesie: -q needs a policy to report, which -i request has none of",
		},
		{
			name: "should reject --usage with -i request",
			cfg:  request(func(c *plan.Config) { c.Usage = true }),
			wantErr: "onesie: --usage does not apply to -i request, " +
				"whose response bodies already carry usage",
		},
		{
			name:    "should reject --merge with -i request",
			cfg:     request(func(c *plan.Config) { c.Merge = true }),
			wantErr: "onesie: --merge does not apply to -i request, which forwards raw responses",
		},
		{
			name: "should name --merge-key when that is the flag given",
			cfg: request(func(c *plan.Config) {
				c.Merge = true
				c.MergeName = "--merge-key"
			}),
			wantErr: "onesie: --merge-key does not apply to -i request, " +
				"which forwards raw responses",
		},
		{
			name: "should reject -m with -i request",
			cfg:  request(func(c *plan.Config) { c.HasModel = true }),
			wantErr: "onesie: -m does not apply to -i request, " +
				"whose bodies carry their own model",
		},
		{
			name: "should reject --print-questions with -i request",
			cfg:  request(func(c *plan.Config) { c.PrintQuestions = true }),
			wantErr: "onesie: --print-questions needs questions of its own, " +
				"which -i request does not build",
		},
		{
			name: "should accept the streaming flags with -i request",
			cfg: request(func(c *plan.Config) {
				c.Unordered = true
				c.StopOnError = true
				c.SkipBlank = true
				c.Jobs = 4
				c.JobsSet = true
			}),
		},
		{
			name: "should still reject -j 0 with -i request",
			cfg: request(func(c *plan.Config) {
				c.Jobs = 0
				c.JobsSet = true
			}),
			wantErr: "onesie: -j takes a positive number of records in flight, got 0",
		},
		{
			name:    "should reject -r with -o outside request mode",
			cfg:     plan.Config{Raw: true, Output: "json", InputName: "text"},
			wantErr: "onesie: -r and -o are mutually exclusive",
		},
		{
			name: "should reject both print flags at once",
			cfg: plan.Config{
				PrintRequest: true, PrintQuestions: true, InputName: "text",
			},
			wantErr: "onesie: --print-request and --print-questions each write a different " +
				"thing to stdout. Pass one",
		},
		{
			name:    "should reject -o with --print-questions",
			cfg:     plan.Config{PrintQuestions: true, Output: "json", InputName: "text"},
			wantErr: "onesie: -o does not apply to --print-questions, which writes a question file",
		},
		{
			name:    "should reject -r with --print-request",
			cfg:     plan.Config{PrintRequest: true, Raw: true, InputName: "text"},
			wantErr: "onesie: -r does not apply to --print-request, which writes a request body",
		},
		{
			name: "should name --merge-key when that is the spelling given",
			cfg: plan.Config{
				PrintRequest: true, Merge: true, MergeName: "--merge-key", InputName: "text",
			},
			wantErr: "onesie: --merge-key needs answers to fold in, " +
				"which --print-request does not produce",
		},
		{
			name:    "should reject -q with --print-request",
			cfg:     plan.Config{PrintRequest: true, Quiet: true, InputName: "text"},
			wantErr: "onesie: -q suppresses output, which leaves --print-request nothing to write",
		},
		{
			name: "should reject --stats with --print-questions",
			cfg:  plan.Config{PrintQuestions: true, Stats: true, InputName: "text"},
			wantErr: "onesie: --stats has nothing to report with --print-questions, " +
				"which makes no request",
		},
		{
			name: "should reject --stats with --print-request under -i request",
			cfg: request(func(c *plan.Config) {
				c.PrintRequest = true
				c.Stats = true
			}),
			wantErr: "onesie: --stats has nothing to report with --print-request, " +
				"which makes no request",
		},
		{
			name: "should accept --stats on its own under -i request",
			cfg:  request(func(c *plan.Config) { c.Stats = true }),
		},
		{
			// The request mode reason is untrue once --print-request is set, since nothing is
			// forwarded and no request is made.
			name: "should blame the dry run rather than the mode for -o under -i request",
			cfg: request(func(c *plan.Config) {
				c.PrintRequest = true
				c.Output = "json"
			}),
			wantErr: "onesie: -o does not apply to --print-request, which writes a request body",
		},
		{
			// Same, and there is no response body to carry a usage object either.
			name: "should blame the dry run rather than the mode for --usage under -i request",
			cfg: request(func(c *plan.Config) {
				c.PrintRequest = true
				c.Usage = true
			}),
			wantErr: "onesie: --usage reports the tokens a question cost, " +
				"which --print-request does not ask",
		},
		{
			name: "should keep the request mode reason for a flag the dry run has no rule for",
			cfg: request(func(c *plan.Config) {
				c.PrintRequest = true
				c.HasAsk = true
			}),
			wantErr: "onesie: -i request carries its own questions. Drop --ask",
		},
		{
			name: "should reject --assert with -i request",
			cfg:  request(func(c *plan.Config) { c.HasAssert = true }),
			wantErr: "onesie: --assert does not apply to -i request, " +
				"which forwards raw responses",
		},
		{
			name: "should reject --assert with --list-models",
			cfg: plan.Config{
				ListModels: true, HasAssert: true, InputName: "text",
			},
			wantErr: "onesie: --assert judges an answer, which --list-models does not produce",
		},
		{
			name: "should reject --assert with --print-request",
			cfg: plan.Config{
				PrintRequest: true, HasAssert: true, InputName: "text",
			},
			wantErr: "onesie: --assert judges an answer, which --print-request does not produce",
		},
		{
			name: "should name the file's key when only the file carried the assertion",
			cfg: plan.Config{
				PrintRequest: true, HasAssert: true, AssertName: "'assert'",
				FileName: "q.yaml", InputName: "text",
			},
			wantErr: "onesie: 'assert' judges an answer, which --print-request does not produce",
		},
		{
			name: "should accept --assert with --print-questions",
			cfg: plan.Config{
				PrintQuestions: true, HasAssert: true, InputName: "text",
			},
		},
		{
			name: "should reject --stop-on-assert with --print-request",
			cfg: plan.Config{
				PrintRequest: true, StopOnAssert: true, Streaming: true, InputName: "lines",
			},
			wantErr: "onesie: --stop-on-assert ends a stream on a false assertion, " +
				"which --print-request does not produce",
		},
		{
			name:    "should reject --abstain-if without an assertion",
			cfg:     plan.Config{HasAbstainIf: true, InputName: "text"},
			wantErr: "onesie: --abstain-if needs --assert, since without one every record is a yes",
		},
		{
			name: "should name the file's key when only the file carried the abstain expression",
			cfg: plan.Config{
				HasAbstainIf: true, AbstainIfName: "'abstain_if'", FileName: "q.yaml", InputName: "text",
			},
			wantErr: "onesie: 'abstain_if' needs --assert, since without one every record is a yes",
		},
		{
			name: "should accept --abstain-if when the only assertion comes from the file",
			cfg: plan.Config{
				HasAssert: true, AssertName: "'assert'", HasAbstainIf: true,
				FileName: "q.yaml", InputName: "text",
			},
		},
		{
			name: "should reject --abstain-if with --list-models",
			cfg: plan.Config{
				ListModels: true, HasAbstainIf: true, InputName: "text",
			},
			wantErr: "onesie: --abstain-if judges an answer, which --list-models does not produce",
		},
		{
			name: "should reject --abstain-if with -i request",
			cfg:  request(func(c *plan.Config) { c.HasAbstainIf = true }),
			wantErr: "onesie: --abstain-if does not apply to -i request, " +
				"which forwards raw responses",
		},
		{
			name: "should name --assert first when --print-request is given both gates",
			cfg: plan.Config{
				PrintRequest: true, HasAssert: true, HasAbstainIf: true, InputName: "text",
			},
			wantErr: "onesie: --assert judges an answer, which --print-request does not produce",
		},
		{
			name: "should reject --abstain-if with --print-request",
			cfg: plan.Config{
				PrintRequest: true, HasAbstainIf: true, InputName: "text",
			},
			wantErr: "onesie: --abstain-if judges an answer, which --print-request does not produce",
		},
		{
			name: "should accept --abstain-if with --print-questions",
			cfg: plan.Config{
				PrintQuestions: true, HasAssert: true, HasAbstainIf: true, InputName: "text",
			},
		},
		{
			name: "should accept --stop-on-error with --print-request",
			cfg: plan.Config{
				PrintRequest: true, StopOnError: true, Streaming: true, InputName: "lines",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warning, err := plan.CheckFlags(tc.cfg)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if warning != "" {
					t.Errorf("warning = %q, want none", warning)
				}

				return
			}

			if err == nil {
				t.Fatalf("expected an error, got none")
			}

			if err.Error() != tc.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}
