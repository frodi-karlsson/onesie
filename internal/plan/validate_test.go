package plan_test

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	readFile := func(string) ([]byte, error) { return nil, nil }

	tests := []struct {
		name         string
		events       []argv.Event
		file         []plan.Question
		positional   string
		fileName     string
		wantErr      string
		wantExact    string
		wantWarning  string
		wantFallback *bool
	}{
		{
			name:   "should accept a bare box",
			events: []argv.Event{{Name: "ask", Value: "mug=the mug"}, {Name: "box", Value: "true"}},
		},

		{
			name:    "should refuse --box beside --pick",
			events:  []argv.Event{{Name: "ask", Value: "mug=x"}, {Name: "box", Value: "true"}, {Name: "pick", Value: "a,b"}},
			wantErr: "--box, --pick and --rate are mutually exclusive. A question is one of them",
		},
		{
			name:    "should refuse --desc on a box",
			events:  []argv.Event{{Name: "ask", Value: "mug=x"}, {Name: "box", Value: "true"}, {Name: "desc", Value: "a=b"}},
			wantErr: "--desc does not apply to 'mug', which is a box question with no options",
		},
		{
			name:    "should refuse --min-confidence on a box",
			events:  []argv.Event{{Name: "ask", Value: "mug=x"}, {Name: "box", Value: "true"}, {Name: "min-confidence", Value: "0.5"}},
			wantErr: "--min-confidence does not apply to 'mug', which is a box question. Gate a box with --assert",
		},
		{
			name:       "should refuse a positional combined with ask",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "ask", Value: "a=first"}},
			wantErr:    "a positional question cannot be combined with --ask or -f",
		},
		{
			name:       "should refuse a positional combined with a file question",
			positional: "is this urgent",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Origin: plan.OriginFile},
			},
			wantErr: "a positional question cannot be combined with --ask or -f",
		},
		{
			name:    "should refuse a reserved id",
			events:  []argv.Event{{Name: "ask", Value: "usage=first"}},
			wantErr: "question id 'usage' is reserved",
		},
		{
			name:    "should refuse an empty --ask question",
			events:  []argv.Event{{Name: "ask", Value: "a="}},
			wantErr: "--ask a= is blank, an empty question is one the model cannot answer",
		},
		{
			name:    "should refuse an all space --ask question",
			events:  []argv.Event{{Name: "ask", Value: "a= \t "}},
			wantErr: "--ask a= is blank, an empty question is one the model cannot answer",
		},
		{
			name:       "should refuse an all space positional question",
			positional: "   ",
			wantErr:    "the question is blank, an empty question is one the model cannot answer",
		},
		{
			name: "should refuse an empty question in a question file",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: " ", Origin: plan.OriginFile},
			},
			wantErr: "question 'urgent' has a blank 'ask', an empty question is one the model cannot answer",
		},
		{
			name: "should refuse empty instructions in a request body",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "", Origin: plan.OriginBody},
			},
			wantErr: "question 'urgent' has blank 'instructions', an empty question is one the model cannot answer",
		},
		{
			name: "should refuse null instructions in a request body",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: nil, Origin: plan.OriginBody},
			},
			wantErr: "question 'urgent' has null 'instructions', an empty question is one the model cannot answer",
		},
		{
			name: "should refuse a null ask in a question file",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: nil, Origin: plan.OriginFile},
			},
			wantErr: "question 'urgent' has a null 'ask', an empty question is one the model cannot answer",
		},
		{
			name: "should refuse number instructions in a request body",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: uint64(7), Origin: plan.OriginBody},
			},
			wantErr: "question 'urgent' has 'instructions' that are not text or a mapping, so the " +
				"model has no question to answer",
		},
		{
			name: "should refuse boolean instructions in a request body",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: true, Origin: plan.OriginBody},
			},
			wantErr: "question 'urgent' has 'instructions' that are not text or a mapping, so the " +
				"model has no question to answer",
		},
		{
			name: "should refuse empty structured instructions in a request body",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: json.RawMessage(`{}`), Origin: plan.OriginBody},
			},
			wantErr: "question 'urgent' has blank 'instructions', an empty question is one the model cannot answer",
		},
		{
			name: "should accept structured instructions in a request body",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: json.RawMessage(`{"q":"same person"}`), Origin: plan.OriginBody},
			},
		},
		{
			name:    "should refuse the positional id used explicitly",
			events:  []argv.Event{{Name: "ask", Value: "answer=first"}},
			wantErr: "question id 'answer' is reserved",
		},
		{
			name:    "should refuse an id beginning with two underscores",
			events:  []argv.Event{{Name: "ask", Value: "__private=first"}},
			wantErr: "question id '__private' is reserved",
		},
		{
			name: "should refuse a single option pick",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "safe"},
			},
			wantErr: "--pick needs at least two options in question 'a', got 1: safe",
		},
		{
			name: "should refuse a single level rate",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "rate", Value: "calm"},
			},
			wantErr: "--rate needs at least two levels in question 'a', got 1: calm",
		},
		{
			name: "should refuse a duplicate option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,technical,billing"},
			},
			wantErr: "--pick option 'billing' is listed twice in question 'team'",
		},
		{
			name: "should refuse an empty option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,,technical"},
			},
			wantExact: "--pick option 2 in question 'team' is empty",
		},
		{
			name: "should refuse an option of only spaces",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing, "},
			},
			wantExact: "--pick option 2 in question 'team' is empty",
		},
		{
			name: "should refuse an empty level",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,,high"},
			},
			wantExact: "--rate level 2 in question 'severity' is empty",
		},
		{
			name:      "should refuse a file's blank option",
			file:      filePick("billing", "  "),
			wantExact: "'pick' option 2 in question 'team' is empty",
		},
		{
			name: "should match a description against the trimmed option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing, technical"},
				{Name: "desc", Value: "technical=it"},
				{Name: "desc", Value: "billing =money"},
			},
		},
		{
			name: "should refuse a duplicate level",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,high,high"},
			},
			wantErr: "--rate label 'high' is listed twice in question 'severity'",
		},
		{
			name: "should refuse pick and rate together",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "rate", Value: "p,q"},
			},
			wantErr: "--pick and --rate are mutually exclusive",
		},
		{
			name: "should refuse a partly described rubric",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "minor,major,critical"},
				{Name: "desc", Value: "minor=small"},
			},
			wantErr: "--rate levels must all be described or all bare in question 'severity'. " +
				"Described minor but not major, critical",
		},
		{
			name: "should refuse a desc naming an unknown option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,technical"},
				{Name: "desc", Value: "bilingl=typo"},
			},
			wantErr: "--desc names an unknown key 'bilingl' in question 'team'. --pick has: billing, technical",
		},
		{
			name: "should refuse a desc other than yes or no on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "desc", Value: "maybe=unclear"},
			},
			wantErr: "--desc on a yes or no question takes 'yes' or 'no', got 'maybe'",
		},
		{
			name: "should refuse min-confidence on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "min-confidence", Value: "0.7"},
				{Name: "fallback", Value: "true"},
			},
			wantErr: "--min-confidence needs a confidence value, and 'urgent' is a yes or no question",
		},
		{
			name: "should refuse threshold on a pick question",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "threshold", Value: "0.5"},
			},
			wantErr: "--threshold cuts a yes or no probability, and 'team' has options",
		},
		{
			name: "should refuse threshold on a rate question",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,high"},
				{Name: "threshold", Value: "0.5"},
			},
			wantErr: "--threshold cuts a yes or no probability, and 'severity' has levels",
		},
		{
			name: "should refuse min-confidence with no fallback",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "0.7"},
			},
			wantErr: "--min-confidence needs --fallback, nothing to substitute for 'team'",
		},
		{
			name: "should refuse a threshold outside zero to one",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "threshold", Value: "85"},
			},
			wantErr: "--threshold must be between 0 and 1, got 85",
		},
		{
			name: "should refuse a min-confidence outside zero to one",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "1.5"},
				{Name: "fallback", Value: "human"},
			},
			wantErr: "--min-confidence must be between 0 and 1, got 1.5",
		},
		{
			name: "should refuse a non boolean fallback on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "fallback", Value: "human"},
			},
			wantErr: "--fallback on a yes or no question takes true, false, yes or no, got 'human'",
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
			name: "should accept a pick fallback that is none of the options",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,sales"},
				{Name: "min-confidence", Value: "0.7"},
				{Name: "fallback", Value: "human"},
			},
		},
		{
			name: "should accept a pick fallback one edit from an option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,sales"},
				{Name: "min-confidence", Value: "0.7"},
				{Name: "fallback", Value: "billin"},
			},
		},
		{
			name: "should refuse two questions sharing an id",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "a=two"},
			},
			wantErr: "question id 'a' is given twice",
		},
		{
			name: "should refuse a blank earlier question before a later level count",
			events: []argv.Event{
				{Name: "ask", Value: "a="},
				{Name: "ask", Value: "b=x"},
				{Name: "rate", Value: "one"},
			},
			wantExact: "--ask a= is blank, an empty question is one the model cannot answer",
		},
		{
			name: "should refuse --pick beside --rate before counting the levels",
			events: []argv.Event{
				{Name: "ask", Value: "a=x"},
				{Name: "pick", Value: "p,q"},
				{Name: "rate", Value: "one"},
			},
			wantExact: "--pick and --rate are mutually exclusive. A question is one or the other",
		},
		{
			name: "should refuse a reserved id before counting its levels",
			events: []argv.Event{
				{Name: "ask", Value: "abstain=x"},
				{Name: "rate", Value: strings.Join(generated("l", 11), ",")},
			},
			wantExact: "question id 'abstain' is reserved",
		},
		{
			name: "should refuse a repeated option as listed twice when the distinct options fit",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: strings.Join(append(generated("o", 255), "o1"), ",")},
			},
			wantExact: "--pick option 'o1' is listed twice in question 'team'",
		},
		{
			name: "should count every option the user typed when the distinct options pass the limit",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: strings.Join(append(generated("o", 256), "o1"), ",")},
			},
			wantExact: "--pick takes at most 255 options in question 'team', got 257",
		},
		{
			name:       "should warn about a partly described option set",
			positional: "which team",
			events: []argv.Event{
				{Name: "pick", Value: "billing,technical,sales"},
				{Name: "desc", Value: "billing=payments"},
				{Name: "desc", Value: "technical=bugs"},
			},
			wantWarning: "the question describes billing, technical but not sales",
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
			name: "should report an empty pick without a trailing separator",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: ""},
			},
			wantExact: "--pick needs at least two options in question 'a', got 1",
		},
		{
			name:      "should report an unlabelled body's level count without a list",
			file:      bodyLevels(1),
			wantExact: "'criteria' needs at least two levels in question 'bq', got 1",
		},
		{
			name: "should name criteria when a body's choice has one option",
			file: []plan.Question{{
				ID: "bq", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginBody,
				Options: []plan.Option{{Name: "only"}},
			}},
			wantExact: "'criteria' needs at least two options in question 'bq', got 1: only",
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
			wantExact: "'pick' needs at least two options in question 'team', got 1: only",
		},
		{
			name: "should keep the flag spelling when the question was opened with ask",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "only"},
			},
			wantExact: "--pick needs at least two options in question 'team', got 1: only",
		},
		{
			name:       "should omit the question from a positional pick's option count",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "pick", Value: "only"}},
			wantExact:  "--pick needs at least two options, got 1: only",
		},
		{
			name:       "should omit the question from a positional rate's level count",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "rate", Value: "calm"}},
			wantExact:  "--rate needs at least two levels, got 1: calm",
		},
		{
			name:      "should name the question when a file's pick has too many options",
			file:      filePick(generated("o", 256)...),
			wantExact: "'pick' takes at most 255 options in question 'team', got 256",
		},
		{
			name:       "should omit the question when a positional pick has too many options",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "pick", Value: strings.Join(generated("o", 256), ",")},
			},
			wantExact: "--pick takes at most 255 options, got 256",
		},
		{
			name: "should name the question when a rate has too many levels",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: strings.Join(generated("l", 11), ",")},
			},
			wantExact: "--rate takes at most 10 levels in question 'severity', got 11",
		},
		{
			name:       "should omit the question when a positional rate has too many levels",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "rate", Value: strings.Join(generated("l", 11), ",")},
			},
			wantExact: "--rate takes at most 10 levels, got 11",
		},
		{
			name:       "should omit the question from a positional mixed rubric",
			positional: "is this urgent",
			events: []argv.Event{
				{Name: "rate", Value: "minor,major"},
				{Name: "desc", Value: "minor=small"},
			},
			wantExact: "--rate levels must all be described or all bare. " +
				"Described minor but not major",
		},
		{
			name:      "should name the pick key for a file's duplicate option",
			file:      filePick("billing", "billing"),
			wantExact: "'pick' option 'billing' is listed twice in question 'team'",
		},
		{
			name:   "should name the pick key in a file question's desc vocabulary",
			file:   filePick("billing", "technical"),
			events: []argv.Event{{Name: "desc", Value: "bilingl=x"}},
			wantExact: "--desc names an unknown key 'bilingl' in question 'team'. " +
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
			wantExact: "'rate' levels must all be described or all bare in question " +
				"'severity'. Described major but not minor",
		},
		{
			name: "should name the file's keys when min_confidence has no fallback",
			file: withPolicy(filePick("billing", "technical"), plan.Policy{
				MinConfidence: pointerTo(0.7),
			}),
			wantExact: "'min_confidence' needs 'fallback', nothing to substitute for 'team'",
		},
		{
			name: "should name the file's keys in the threshold remedy",
			file: withPolicy(filePick("billing", "technical"), plan.Policy{
				Threshold: pointerTo(0.8),
			}),
			wantExact: "'threshold' cuts a yes or no probability, and 'team' has options. " +
				"Use 'min_confidence' with 'fallback'",
		},
		{
			name: "should name the file's keys when min_confidence lands on a yes/no question, with no shape to add",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{MinConfidence: pointerTo(0.7)},
			}},
			wantExact: "'min_confidence' needs a confidence value, and 'urgent' is a yes or no " +
				"question. Use 'threshold'",
		},
		{
			name: "should refuse a threshold of nan",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "threshold", Value: "nan"},
			},
			wantExact: "--threshold must be between 0 and 1, got NaN",
		},
		{
			name: "should refuse a min confidence of nan",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "NaN"},
				{Name: "fallback", Value: "x"},
			},
			wantExact: "--min-confidence must be between 0 and 1, got NaN",
		},
		{
			name: "should refuse a file's threshold of nan",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{Threshold: pointerTo(math.NaN())},
			}},
			wantExact: "'threshold' must be between 0 and 1, got NaN",
		},
		{
			name: "should name the threshold key when a file's value is out of range",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{Threshold: pointerTo(85.0)},
			}},
			wantExact: "'threshold' must be between 0 and 1, got 85",
		},
		{
			name: "should name the fallback key when a file's yes/no value is not a boolean",
			file: []plan.Question{{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{Fallback: &plan.Fallback{Text: "human"}},
			}},
			wantExact: "'fallback' on a yes or no question takes true, false, yes or no, " +
				"got 'human'",
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

			warnings, err := plan.Validate(built)
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

	t.Run("should pass through a refusal it has no wording for, in onesie-go's words", func(t *testing.T) {
		t.Parallel()

		// An empty plan is the one such refusal today. The CLI refuses it earlier as no question given.
		if _, err := plan.Validate(&plan.Plan{}); err == nil || err.Error() != "at least one question is required" {
			t.Errorf("Validate = %v, want onesie-go's reason", err)
		}
	})

	t.Run("should mention the positional question without the id the user never typed", func(t *testing.T) {
		t.Parallel()

		ev := func(pairs ...string) []argv.Event {
			events := make([]argv.Event, 0, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				events = append(events, argv.Event{Name: pairs[i], Value: pairs[i+1]})
			}

			return events
		}

		for _, tc := range []struct {
			events []argv.Event
			want   string
		}{
			{ev("pick", "x,y,x"), "--pick option 'x' is listed twice"},
			{ev("rate", "lo,hi,lo"), "--rate label 'lo' is listed twice"},
			{ev("pick", "x,y", "desc", "z=w"), "--desc names an unknown key 'z'. --pick has: x, y"},
			{ev("box", "true", "desc", "a=b"), "--desc does not apply to the question, which is a box question with no options"},
			{ev("box", "true", "threshold", "0.5"), "--threshold does not apply to the question, which is a box question. Gate a box with --assert"},
			{ev("pick", "x,y", "threshold", "0.5"), "--threshold cuts a yes or no probability, and the question has options. Use --min-confidence with --fallback"},
			{ev("min-confidence", "0.5"), "--min-confidence needs a confidence value, and the question is a yes or no question. Use --threshold, or add --pick or --rate"},
			{ev("pick", "x,y", "min-confidence", "0.5"), "--min-confidence needs --fallback, nothing to substitute for the question"},
			{ev("pick", "x,y", "desc", "x=one"), "the question describes x but not y"},
		} {
			built, err := plan.Assemble(plan.Source{Events: tc.events, Positional: "which", ReadFile: readFile})
			if err != nil {
				t.Fatalf("assembling %v: %v", tc.events, err)
			}

			warnings, err := plan.Validate(built)

			got := strings.Join(warnings, "\n")
			if err != nil {
				got = err.Error()
			}

			if got != tc.want || strings.Contains(got, "'answer'") {
				t.Errorf("%v: got %q, want %q", tc.events, got, tc.want)
			}
		}
	})
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
		// refused for.
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

func TestSpelling(t *testing.T) {
	t.Run("should use the file key of every row", func(t *testing.T) {
		for _, row := range settingrow.Table() {
			if row.FileKey == "" || row.Flag.Long == "" || row.InBody {
				continue
			}
			flag := "--" + row.Flag.Long
			if got := plan.Spelling(plan.OriginFile, flag); got != "'"+row.FileKey+"'" {
				t.Errorf("Spelling %s = %s, want file key %s", flag, got, row.FileKey)
			}
			if got := plan.Spelling(plan.OriginFlag, flag); got != flag {
				t.Errorf("Spelling flag = %s, want %s", got, flag)
			}
		}
	})
}
