package qfile_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func TestWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions []plan.Question
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
			name: "should reject the reserved positional id",
			questions: []plan.Question{
				{ID: "answer", Shape: plan.Noul, Instructions: "q"},
			},
			wantErr: "jev: --print-questions needs a named question. Use --ask NAME=QUESTION",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Write(tc.questions)
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
}

func TestWriteRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions []plan.Question
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			written, err := qfile.Write(tc.questions)
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			reloaded, err := qfile.Load(written)
			if err != nil {
				t.Fatalf("Load: %v\nfile was\n%s", err, written)
			}

			want := fileShaped(tc.questions)
			got := fileShaped(reloaded.Questions)

			if !reflect.DeepEqual(want, got) {
				t.Errorf("round trip lost information\nwant %#v\ngot  %#v\nfile was\n%s",
					want, got, written)
			}
		})
	}
}

func fileShaped(questions []plan.Question) []plan.Question {
	out := make([]plan.Question, 0, len(questions))

	for _, question := range questions {
		// Origin is where the question was authored, which a file cannot carry and the loader
		// always reports as OriginFile. DescOrder and UnknownDesc record how --desc was written,
		// which only the command line path produces. All three are plan internals rather than
		// content, so they are cleared on both sides rather than compared.
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
