package qfile_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func TestReadYesNo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		wantErr bool
		check   func(t *testing.T, f *qfile.File)
	}{
		{
			name: "should read a bare string as a yes/no question",
			doc:  "urgent: Does this convey urgency?\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if len(f.Questions) != 1 {
					t.Fatalf("questions = %d, want 1", len(f.Questions))
				}

				q := f.Questions[0]
				if q.ID != "urgent" || q.Shape != plan.Noul {
					t.Errorf("got %s/%s, want urgent/noul", q.ID, q.Shape)
				}

				if q.Instructions != "Does this convey urgency?" {
					t.Errorf("instructions = %v", q.Instructions)
				}

				if q.Origin != plan.OriginFile {
					t.Error("a file question must carry the file origin, so reserved ids are rejected")
				}
			},
		},
		{
			name: "should keep questions in file order",
			doc:  "zebra: one\napple: two\nmiddle: three\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				want := []string{"zebra", "apple", "middle"}
				for i, q := range f.Questions {
					if q.ID != want[i] {
						t.Errorf("question %d = %s, want %s", i, q.ID, want[i])
					}
				}
			},
		},
		{
			name: "should read the long form with rubrics",
			doc: "spam:\n  ask: Is this automated spam?\n" +
				"  yes_means: Bulk, templated or bot sent\n" +
				"  no_means: Written by a person\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				q := f.Questions[0]
				if q.Criteria == nil {
					t.Fatal("criteria should be set")
				}

				if q.Criteria.Yes != "Bulk, templated or bot sent" {
					t.Errorf("yes = %v", q.Criteria.Yes)
				}

				if q.Criteria.No != "Written by a person" {
					t.Errorf("no = %v", q.Criteria.No)
				}
			},
		},
		{
			name: "should accept true and false as aliases",
			doc:  "spam:\n  ask: Is this spam?\n  true: bulk\n  false: personal\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				q := f.Questions[0]
				if q.Criteria == nil || q.Criteria.Yes != "bulk" || q.Criteria.No != "personal" {
					t.Errorf("criteria = %+v, want the true and false keys mapped", q.Criteria)
				}
			},
		},
		{
			name: "should prefer the _means spelling when both are present",
			doc:  "spam:\n  ask: q\n  yes_means: documented\n  true: alias\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if f.Questions[0].Criteria.Yes != "documented" {
					t.Errorf("yes = %v, want the _means spelling to win",
						f.Questions[0].Criteria.Yes)
				}
			},
		},
		{
			name: "should carry a structured instruction through as an object",
			doc: "spam:\n  ask:\n    what: Is this spam?\n" +
				"    examples: [\"buy now\"]\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				encoded, err := json.Marshal(f.Questions[0].Instructions)
				if err != nil {
					t.Fatalf("marshalling: %v", err)
				}

				want := `{"what":"Is this spam?","examples":["buy now"]}`
				if string(encoded) != want {
					t.Errorf("got  %s\nwant %s", encoded, want)
				}
			},
		},
		{
			name:    "should reject a question that is neither a string nor a mapping",
			doc:     "urgent: [1, 2]\n",
			wantErr: true,
		},
		{
			name:    "should reject a mapping with no ask",
			doc:     "urgent:\n  yes_means: something\n",
			wantErr: true,
		},
		{
			name:    "should reject a file that is not a mapping",
			doc:     "- just\n- a list\n",
			wantErr: true,
		},
		{
			name:    "should reject a yes rubric alongside pick",
			doc:     "team:\n  ask: q\n  yes_means: something\n  pick: [a, b]\n",
			wantErr: true,
		},
		{
			name:    "should reject a false alias alongside rate",
			doc:     "severity:\n  ask: q\n  false: nope\n  rate: [a, b]\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.check(t, got)
		})
	}
}

func TestReadPick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		wantErr bool
		check   func(t *testing.T, q plan.Question)
	}{
		{
			name: "should read a mapping of option to description in file order",
			doc: "team:\n  ask: Which team?\n  pick:\n" +
				"    zebra: Z\n    apple: A\n    middle: M\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Shape != plan.Pick {
					t.Fatalf("shape = %s, want pick", q.Shape)
				}

				want := []string{"zebra", "apple", "middle"}
				for i, option := range q.Options {
					if option.Name != want[i] {
						t.Errorf("option %d = %s, want %s", i, option.Name, want[i])
					}
				}

				if q.Options[0].Desc != "Z" {
					t.Errorf("desc = %v, want Z", q.Options[0].Desc)
				}
			},
		},
		{
			name: "should read an empty description as a null criteria",
			doc:  "team:\n  ask: Which team?\n  pick:\n    billing: Payments\n    sales:\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Options[1].Desc != nil {
					t.Errorf("desc = %v, want nil for an empty value", q.Options[1].Desc)
				}
			},
		},
		{
			name: "should read a sequence of strings as undescribed options",
			doc:  "team:\n  ask: Which team?\n  pick: [billing, technical, sales]\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if len(q.Options) != 3 {
					t.Fatalf("options = %d, want 3", len(q.Options))
				}

				for _, option := range q.Options {
					if option.Desc != nil {
						t.Errorf("option %s should have no description", option.Name)
					}
				}
			},
		},
		{
			name: "should carry a structured description through as an object",
			doc: "team:\n  ask: Which team?\n  pick:\n    billing:\n" +
				"      what: money\n      examples: [\"refund\"]\n    sales: deals\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				encoded, err := json.Marshal(q.Options[0].Desc)
				if err != nil {
					t.Fatalf("marshalling: %v", err)
				}

				want := `{"what":"money","examples":["refund"]}`
				if string(encoded) != want {
					t.Errorf("got  %s\nwant %s", encoded, want)
				}
			},
		},
		{
			name:    "should reject a pick that is neither a mapping nor a sequence",
			doc:     "team:\n  ask: Which team?\n  pick: billing\n",
			wantErr: true,
		},
		{
			name:    "should reject a non string entry in a pick sequence",
			doc:     "team:\n  ask: Which team?\n  pick: [billing, 7]\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.check(t, got.Questions[0])
		})
	}
}

func TestReadRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		wantErr bool
		check   func(t *testing.T, q plan.Question)
	}{
		{
			name: "should read a sequence of single key mappings in order",
			doc: "frustration:\n  ask: How frustrated?\n  rate:\n" +
				"    - calm: States facts\n    - annoyed: Civil but terse\n" +
				"    - furious: Threats to leave\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Shape != plan.Rate {
					t.Fatalf("shape = %s, want rate", q.Shape)
				}

				if !q.Labelled {
					t.Error("a file rate question carries labels and must be Labelled")
				}

				want := []string{"calm", "annoyed", "furious"}
				for i, level := range q.Levels {
					if level.Label != want[i] {
						t.Errorf("level %d = %s, want %s", i, level.Label, want[i])
					}
				}

				if q.Levels[0].Desc != "States facts" {
					t.Errorf("desc = %v", q.Levels[0].Desc)
				}
			},
		},
		{
			name: "should read a sequence of strings as bare levels",
			doc:  "severity:\n  ask: How bad?\n  rate: [minor, major, critical]\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if len(q.Levels) != 3 || q.Levels[2].Label != "critical" {
					t.Fatalf("levels = %+v", q.Levels)
				}

				for _, level := range q.Levels {
					if level.Desc != nil {
						t.Errorf("level %s should carry no description", level.Label)
					}
				}
			},
		},
		{
			name: "should read a mapping in file order",
			doc:  "severity:\n  ask: How bad?\n  rate:\n    zebra: Z\n    apple: A\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Levels[0].Label != "zebra" || q.Levels[1].Label != "apple" {
					t.Errorf("levels = %s,%s, want zebra,apple",
						q.Levels[0].Label, q.Levels[1].Label)
				}
			},
		},
		{
			name: "should carry a structured level description through as an object",
			doc: "frustration:\n  ask: How frustrated?\n  rate:\n" +
				"    - calm:\n        what: no affect\n        examples: [\"following up\"]\n" +
				"    - angry: shouting\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				encoded, err := json.Marshal(q.Levels[0].Desc)
				if err != nil {
					t.Fatalf("marshalling: %v", err)
				}

				want := `{"what":"no affect","examples":["following up"]}`
				if string(encoded) != want {
					t.Errorf("got  %s\nwant %s", encoded, want)
				}
			},
		},
		{
			name:    "should reject a sequence entry with more than one key",
			doc:     "severity:\n  ask: q\n  rate:\n    - a: one\n      b: two\n",
			wantErr: true,
		},
		{
			name:    "should reject a rate that is neither a mapping nor a sequence",
			doc:     "severity:\n  ask: q\n  rate: critical\n",
			wantErr: true,
		},
		{
			name:    "should reject both pick and rate on one question",
			doc:     "severity:\n  ask: q\n  pick: [a, b]\n  rate: [c, d]\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.check(t, got.Questions[0])
		})
	}
}

func TestReadPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		wantErr string
		check   func(t *testing.T, q plan.Question)
	}{
		{
			name: "should read a threshold on a yes/no question",
			doc:  "urgent:\n  ask: q\n  threshold: 0.85\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Policy.Threshold == nil || *q.Policy.Threshold != 0.85 {
					t.Errorf("threshold = %v, want 0.85", q.Policy.Threshold)
				}
			},
		},
		{
			name: "should read min_confidence and fallback on a pick question",
			doc: "team:\n  ask: q\n  pick: [a, b]\n" +
				"  min_confidence: 0.7\n  fallback: human\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Policy.MinConfidence == nil || *q.Policy.MinConfidence != 0.7 {
					t.Errorf("min_confidence = %v, want 0.7", q.Policy.MinConfidence)
				}

				if q.Policy.Fallback == nil || q.Policy.Fallback.Text != "human" {
					t.Errorf("fallback = %+v, want human", q.Policy.Fallback)
				}
			},
		},
		{
			name: "should resolve a yes/no fallback of true to a boolean at load time",
			doc:  "urgent:\n  ask: q\n  fallback: true\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Policy.Fallback == nil {
					t.Fatal("fallback should be set")
				}

				if !q.Policy.Fallback.Boolean {
					t.Error("a yes/no fallback of true must resolve to Boolean true at load")
				}
			},
		},
		{
			name: "should resolve a yes/no fallback of yes to a boolean at load time",
			doc:  "urgent:\n  ask: q\n  fallback: yes\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				// strconv.ParseBool rejects yes, so this is the case that catches the wrong
				// parser being used. It would silently resolve to false, the opposite decision.
				if !q.Policy.Fallback.Boolean {
					t.Error("a yes/no fallback of yes must resolve to Boolean true at load")
				}
			},
		},
		{
			name: "should leave a pick fallback as text",
			doc:  "team:\n  ask: q\n  pick: [a, b]\n  fallback: human\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Policy.Fallback.Boolean {
					t.Error("a pick fallback is a string and must not set Boolean")
				}
			},
		},
		{
			name: "should read an unquoted boolean fallback as its text",
			doc:  "urgent:\n  ask: q\n  fallback: false\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Policy.Fallback.Text != "false" {
					t.Errorf("fallback text = %q, want false", q.Policy.Fallback.Text)
				}

				if q.Policy.Fallback.Boolean {
					t.Error("a fallback of false must resolve to Boolean false")
				}
			},
		},
		{
			name:    "should name the kind of a structured fallback",
			doc:     "team:\n  ask: q\n  pick: [a, b]\n  fallback:\n    x: 1\n",
			wantErr: "'fallback' in question 'team' must be a string, got a mapping",
		},
		{
			name:    "should name the kind of a sequence fallback",
			doc:     "team:\n  ask: q\n  pick: [a, b]\n  fallback: [a, b]\n",
			wantErr: "'fallback' in question 'team' must be a string, got a sequence",
		},
		{
			name:    "should name an empty fallback as null",
			doc:     "team:\n  ask: q\n  pick: [a, b]\n  fallback:\n",
			wantErr: "'fallback' in question 'team' must be a string, got null",
		},
		{
			name:    "should reject a non numeric threshold",
			doc:     "urgent:\n  ask: q\n  threshold: soon\n",
			wantErr: "'threshold' in question 'urgent' must be a number, got 'soon'",
		},
		{
			name:    "should name the kind of a structured min_confidence",
			doc:     "team:\n  ask: q\n  pick: [a, b]\n  min_confidence:\n    x: 1\n",
			wantErr: "'min_confidence' in question 'team' must be a number, got a mapping",
		},
		{
			name:    "should reject a non numeric min_confidence",
			doc:     "team:\n  ask: q\n  pick: [a, b]\n  min_confidence: high\n",
			wantErr: "'min_confidence' in question 'team' must be a number, got 'high'",
		},
		{
			name:    "should reject an unknown key in a question definition",
			doc:     "urgent:\n  ask: q\n  thresold: 0.85\n",
			wantErr: "thresold",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.check(t, got.Questions[0])
		})
	}
}

func TestReadAssert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		doc           string
		wantAssert    string
		wantAbstainIf string
		wantQuestions []string
		wantErr       string
	}{
		{
			name:          "should read a top level assert as the file's gate",
			doc:           "assert: 'urgent.value < 0.5'\nurgent: q\n",
			wantAssert:    "urgent.value < 0.5",
			wantQuestions: []string{"urgent"},
		},
		{
			name:          "should read an assert written below the questions",
			doc:           "urgent: q\nassert: 'urgent.value < 0.5'\n",
			wantAssert:    "urgent.value < 0.5",
			wantQuestions: []string{"urgent"},
		},
		{
			name:          "should leave the assertion empty when a file carries none",
			doc:           "urgent: q\n",
			wantQuestions: []string{"urgent"},
		},
		{
			name:    "should reject a mapping assert",
			doc:     "assert:\n  value: 1\nurgent: q\n",
			wantErr: "onesie: 'assert' must be a string, got a mapping",
		},
		{
			name:    "should reject an empty assert as null",
			doc:     "assert:\nurgent: q\n",
			wantErr: "onesie: 'assert' must be a string, got null",
		},
		{
			name:          "should read a top level abstain_if beside the assert",
			doc:           "assert: 'urgent.value < 0.2'\nabstain_if: 'urgent.value < 0.8'\nurgent: q\n",
			wantAssert:    "urgent.value < 0.2",
			wantAbstainIf: "urgent.value < 0.8",
			wantQuestions: []string{"urgent"},
		},
		{
			name:          "should read an abstain_if written below the questions",
			doc:           "urgent: q\nabstain_if: 'urgent.value < 0.8'\n",
			wantAbstainIf: "urgent.value < 0.8",
			wantQuestions: []string{"urgent"},
		},
		{
			name:    "should reject a mapping abstain_if",
			doc:     "abstain_if:\n  value: 1\nurgent: q\n",
			wantErr: "onesie: 'abstain_if' must be a string, got a mapping",
		},
		{
			name:    "should reject an empty abstain_if as null",
			doc:     "abstain_if:\nurgent: q\n",
			wantErr: "onesie: 'abstain_if' must be a string, got null",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				if err.Error() != tc.wantErr {
					t.Errorf("error = %q, want %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.Assert != tc.wantAssert {
				t.Errorf("assert = %q, want %q", got.Assert, tc.wantAssert)
			}

			if got.AbstainIf != tc.wantAbstainIf {
				t.Errorf("abstain_if = %q, want %q", got.AbstainIf, tc.wantAbstainIf)
			}

			ids := make([]string, 0, len(got.Questions))
			for _, question := range got.Questions {
				ids = append(ids, question.ID)
			}

			if !slices.Equal(ids, tc.wantQuestions) {
				t.Errorf("questions = %v, want %v", ids, tc.wantQuestions)
			}
		})
	}
}

func TestWireValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{
			name:    "should name the question when a structured ask cannot be sent",
			doc:     "q:\n  ask:\n    what: hi\n    score: .nan\n",
			wantErr: "onesie: 'ask' in question 'q' cannot be sent",
		},
		{
			name:    "should name the question when a scalar ask cannot be sent",
			doc:     "q:\n  ask: .nan\n",
			wantErr: "onesie: 'ask' in question 'q' cannot be sent",
		},
		{
			name:    "should name the rubric key when a yes_means cannot be sent",
			doc:     "q:\n  ask: hi\n  yes_means:\n    what: y\n    score: .nan\n",
			wantErr: "onesie: 'yes_means' in question 'q' cannot be sent",
		},
		{
			name:    "should name the option when a pick description cannot be sent",
			doc:     "q:\n  ask: hi\n  pick:\n    a:\n      score: .nan\n    b: second\n",
			wantErr: "onesie: 'pick' option 'a' in question 'q' cannot be sent",
		},
		{
			name:    "should name the level when a rate description cannot be sent",
			doc:     "q:\n  ask: hi\n  rate:\n    low:\n      score: .nan\n    high: h\n",
			wantErr: "onesie: 'rate' level 'low' in question 'q' cannot be sent",
		},
		{
			name:    "should name the level when a rate sequence description cannot be sent",
			doc:     "q:\n  ask: hi\n  rate:\n    - low:\n        score: .nan\n    - high: h\n",
			wantErr: "onesie: 'rate' level 'low' in question 'q' cannot be sent",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))
			if err == nil {
				t.Fatalf("expected an error, got %#v", got)
			}

			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}
