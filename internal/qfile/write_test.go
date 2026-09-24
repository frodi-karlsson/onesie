package qfile_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func TestWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions []plan.Question
		assertion string
		want      string
		wantErr   string
	}{
		{
			name: "should write a noul question with its policy",
			questions: []plan.Question{
				{
					ID:           "urgent",
					Shape:        plan.Noul,
					Instructions: "does this convey urgency",
					Policy:       plan.Policy{Threshold: ptr(0.5)},
				},
			},
			want: "urgent:\n  ask: does this convey urgency\n  threshold: 0.5\n",
		},
		{
			name: "should write questions in plan order",
			questions: []plan.Question{
				{ID: "zebra", Shape: plan.Noul, Instructions: "z"},
				{ID: "alpha", Shape: plan.Noul, Instructions: "a"},
			},
			want: "zebra:\n  ask: z\nalpha:\n  ask: a\n",
		},
		{
			name: "should write a labelled rate as a sequence",
			questions: []plan.Question{
				{
					ID:       "mood",
					Shape:    plan.Rate,
					Labelled: true,
					Levels: []plan.Level{
						{Label: "calm", Desc: "nothing happening"},
						{Label: "angry", Desc: "sky falling"},
					},
					Instructions: "how is it",
				},
			},
			want: "mood:\n  ask: how is it\n  rate:\n  - calm: nothing happening\n" +
				"  - angry: sky falling\n",
		},
		{
			name: "should write an unlabelled rate with index labels",
			questions: []plan.Question{
				{
					ID:       "mood",
					Shape:    plan.Rate,
					Labelled: false,
					Levels: []plan.Level{
						{Desc: "nothing happening"},
						{Desc: "sky falling"},
					},
					Instructions: "how is it",
				},
			},
			want: "mood:\n  ask: how is it\n  rate:\n  - \"0\": nothing happening\n" +
				"  - \"1\": sky falling\n",
		},
		{
			name: "should write noul criteria under the _means keys",
			questions: []plan.Question{
				{
					ID:           "spam",
					Shape:        plan.Noul,
					Instructions: "is this spam",
					Criteria: &plan.YesNoCriteria{
						Yes: "bulk or bot sent",
						No:  "written by a person",
					},
				},
			},
			want: "spam:\n  ask: is this spam\n  yes_means: bulk or bot sent\n" +
				"  no_means: written by a person\n",
		},
		{
			name: "should write a structured instruction as a mapping",
			questions: []plan.Question{
				{
					ID:           "urgent",
					Shape:        plan.Noul,
					Instructions: json.RawMessage(`{"zebra":"z","alpha":"a"}`),
				},
			},
			want: "urgent:\n  ask:\n    zebra: z\n    alpha: a\n",
		},
		{
			name: "should write a pick in option order",
			questions: []plan.Question{
				{
					ID:           "team",
					Shape:        plan.Pick,
					Instructions: "who owns this",
					Options: []plan.Option{
						{Name: "zebra", Desc: "z"},
						{Name: "alpha", Desc: "a"},
					},
				},
			},
			want: "team:\n  ask: who owns this\n  pick:\n    zebra: z\n    alpha: a\n",
		},
		{
			name: "should quote a tab so the parser keeps it",
			questions: []plan.Question{
				{ID: "q", Shape: plan.Noul, Instructions: "col\tvalue"},
			},
			want: "q:\n  ask: \"col\\tvalue\"\n",
		},
		{
			name: "should quote a carriage return so it stays a carriage return",
			questions: []plan.Question{
				{ID: "q", Shape: plan.Noul, Instructions: "a\rb"},
			},
			want: "q:\n  ask: \"a\\rb\"\n",
		},
		{
			name: "should leave a line feed as a block scalar",
			questions: []plan.Question{
				{ID: "q", Shape: plan.Noul, Instructions: "line one\nline two"},
			},
			want: "q:\n  ask: |-\n    line one\n    line two\n",
		},
		{
			name: "should write an assertion above the questions",
			questions: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent"},
			},
			assertion: `urgent.value > 0.5`,
			want:      "assert: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n",
		},
		{
			name: "should write no assert key for an empty assertion",
			questions: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent"},
			},
			want: "urgent:\n  ask: is this urgent\n",
		},
		{
			name: "should reject the reserved positional id",
			questions: []plan.Question{
				{ID: "answer", Shape: plan.Noul, Instructions: "q"},
			},
			wantErr: "onesie: --print-questions needs a named question. Use --ask NAME=QUESTION",
		},
		{
			name: "should reject a rubric on a pick question",
			questions: []plan.Question{
				{
					ID: "team", Shape: plan.Pick, Instructions: "who owns this",
					Options:  []plan.Option{{Name: "billing", Desc: "money"}},
					Criteria: &plan.YesNoCriteria{Yes: "y", No: "n"},
				},
			},
			wantErr: "onesie: question 'team' has both 'yes_means' and 'pick'. " +
				"A question is one or the other",
		},
		{
			name: "should reject a rubric on a rate question",
			questions: []plan.Question{
				{
					ID: "mood", Shape: plan.Rate, Labelled: true, Instructions: "how is it",
					Levels:   []plan.Level{{Label: "calm", Desc: "nothing"}},
					Criteria: &plan.YesNoCriteria{Yes: "y", No: "n"},
				},
			},
			wantErr: "onesie: question 'mood' has both 'yes_means' and 'rate'. " +
				"A question is one or the other",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Write(tc.questions, tc.assertion)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("Write error = %v, want %s", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("Write =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}

	t.Run("should round trip through Load", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			questions []plan.Question
			assertion string
		}{
			{
				name: "should reload a labelled rate to the same plan",
				questions: []plan.Question{
					{
						ID: "mood", Shape: plan.Rate, Labelled: true,
						Instructions: "how is it",
						Levels: []plan.Level{
							{Label: "calm", Desc: "nothing"},
							{Label: "angry", Desc: "everything"},
						},
					},
				},
			},
			{
				name: "should reload noul criteria to the same plan",
				questions: []plan.Question{
					{
						ID: "spam", Shape: plan.Noul, Instructions: "is this spam",
						Criteria: &plan.YesNoCriteria{
							Yes: "bulk or bot sent",
							No:  "written by a person",
						},
					},
				},
			},
			{
				name: "should reload a pick with policy to the same plan",
				questions: []plan.Question{
					{
						ID: "team", Shape: plan.Pick, Instructions: "who owns this",
						Options: []plan.Option{
							{Name: "billing", Desc: "money"},
							{Name: "platform", Desc: "systems"},
						},
						Policy: plan.Policy{MinConfidence: ptr(0.7)},
					},
				},
			},
			{
				name: "should reload a structured instruction in its written order",
				questions: []plan.Question{
					{
						ID: "urgent", Shape: plan.Noul,
						Instructions: json.RawMessage(`{"zebra":"z","mike":"m","alpha":"a"}`),
					},
				},
			},
			{
				name: "should reload questions in their written order",
				questions: []plan.Question{
					{ID: "zebra", Shape: plan.Noul, Instructions: "z"},
					{ID: "mike", Shape: plan.Noul, Instructions: "m"},
					{ID: "alpha", Shape: plan.Noul, Instructions: "a"},
				},
			},
			{
				name: "should reload a threshold and a fallback to the same plan",
				questions: []plan.Question{
					{
						ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent",
						Policy: plan.Policy{
							Threshold: ptr(0.75),
							Fallback:  &plan.Fallback{Text: "true", Boolean: true},
						},
					},
				},
			},
			{
				name: "should reload a tab in a question id",
				questions: []plan.Question{
					{ID: "a\tb", Shape: plan.Noul, Instructions: "is this urgent"},
				},
			},
			{
				name: "should reload a carriage return in a question id",
				questions: []plan.Question{
					{ID: "a\rb", Shape: plan.Noul, Instructions: "is this urgent"},
				},
			},
			{
				name: "should reload a tab in a pick option name",
				questions: []plan.Question{
					{
						ID: "team", Shape: plan.Pick, Instructions: "who owns this",
						Options: []plan.Option{
							{Name: "bil\tling", Desc: "money"},
							{Name: "platform", Desc: "systems"},
						},
					},
				},
			},
			{
				name: "should reload a carriage return in a pick option name",
				questions: []plan.Question{
					{
						ID: "team", Shape: plan.Pick, Instructions: "who owns this",
						Options: []plan.Option{
							{Name: "bil\rling", Desc: "money"},
							{Name: "platform", Desc: "systems"},
						},
					},
				},
			},
			{
				name: "should reload a tab in a rate level label",
				questions: []plan.Question{
					{
						ID: "mood", Shape: plan.Rate, Labelled: true, Instructions: "how is it",
						Levels: []plan.Level{
							{Label: "ca\tlm", Desc: "nothing"},
							{Label: "angry", Desc: "everything"},
						},
					},
				},
			},
			{
				name: "should reload a carriage return in a rate level label",
				questions: []plan.Question{
					{
						ID: "mood", Shape: plan.Rate, Labelled: true, Instructions: "how is it",
						Levels: []plan.Level{
							{Label: "ca\rlm", Desc: "nothing"},
							{Label: "angry", Desc: "everything"},
						},
					},
				},
			},
			{
				name: "should reload a tab in an instruction",
				questions: []plan.Question{
					{ID: "urgent", Shape: plan.Noul, Instructions: "col\tvalue"},
				},
			},
			{
				name: "should reload a carriage return in an instruction",
				questions: []plan.Question{
					{ID: "urgent", Shape: plan.Noul, Instructions: "line one\rline two"},
				},
			},
			{
				name: "should reload a tab in a description",
				questions: []plan.Question{
					{
						ID: "team", Shape: plan.Pick, Instructions: "who owns this",
						Options: []plan.Option{{Name: "billing", Desc: "col\tvalue"}},
					},
				},
			},
			{
				name: "should reload a carriage return in a description",
				questions: []plan.Question{
					{
						ID: "team", Shape: plan.Pick, Instructions: "who owns this",
						Options: []plan.Option{{Name: "billing", Desc: "line one\rline two"}},
					},
				},
			},
			{
				name: "should reload a structured instruction that is a sequence",
				questions: []plan.Question{
					{
						ID: "urgent", Shape: plan.Noul,
						Instructions: json.RawMessage(`["first","second","third"]`),
					},
				},
			},
			{
				name: "should reload a pick whose options have no description",
				questions: []plan.Question{
					{
						ID: "team", Shape: plan.Pick, Instructions: "who owns this",
						Options: []plan.Option{{Name: "billing"}, {Name: "platform"}},
					},
				},
			},
			{
				name: "should reload an assertion holding a quoted string",
				questions: []plan.Question{
					{
						ID: "team", Shape: plan.Pick, Instructions: "who owns this",
						Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
					},
				},
				assertion: `team.value == "billing"`,
			},
			{
				name: "should reload an assertion holding a list",
				questions: []plan.Question{
					{
						ID: "team", Shape: plan.Pick, Instructions: "who owns this",
						Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
					},
				},
				assertion: `team.value in ["billing", "technical"]`,
			},
			{
				name: "should reload an assertion that opens with a quoted string",
				questions: []plan.Question{
					{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent"},
				},
				assertion: `"yes" == "yes" and urgent.value > 0.5`,
			},
			{
				name: "should reload all three policy keys on one question",
				questions: []plan.Question{
					{
						ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent",
						Policy: plan.Policy{
							Threshold:     ptr(0.75),
							MinConfidence: ptr(0.6),
							Fallback:      &plan.Fallback{Text: "false"},
						},
					},
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				written, err := qfile.Write(tc.questions, tc.assertion)
				if err != nil {
					t.Fatalf("Write: %v", err)
				}

				reloaded, err := qfile.Load(written)
				if err != nil {
					t.Fatalf("Load: %v\nfile was\n%s", err, written)
				}

				if reloaded.Assert != tc.assertion {
					t.Errorf("round trip changed the assertion\nwant %q\ngot  %q\nfile was\n%s",
						tc.assertion, reloaded.Assert, written)
				}

				want := fileShaped(tc.questions)
				got := fileShaped(reloaded.Questions)

				if !reflect.DeepEqual(want, got) {
					t.Errorf("round trip lost information\nwant %#v\ngot  %#v\nfile was\n%s",
						want, got, written)
				}

				rewritten, err := qfile.Write(reloaded.Questions, reloaded.Assert)
				if err != nil {
					t.Fatalf("Write after Load: %v", err)
				}

				if string(rewritten) != string(written) {
					t.Errorf("rewriting changed the file\nfirst\n%s\nsecond\n%s", written, rewritten)
				}
			})
		}
	})
}

func fileShaped(questions []plan.Question) []plan.Question {
	out := make([]plan.Question, 0, len(questions))

	for _, question := range questions {
		// Origin, DescOrder and UnknownDesc record how a question was authored, which a file cannot
		// carry, so they are cleared on both sides rather than compared.
		question.Origin = plan.OriginPositional
		question.DescOrder = nil
		question.UnknownDesc = nil

		out = append(out, question)
	}

	return out
}

func ptr[T any](value T) *T {
	return &value
}
