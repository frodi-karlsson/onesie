package plan_test

import (
	"encoding/json"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestWireAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions []plan.Question
		want      string
	}{
		{
			name: "should send a bare rate's labels as its criteria",
			questions: []plan.Question{{
				ID: "r", Shape: plan.Rate, Instructions: "how rude", Labelled: true,
				Levels: []plan.Level{{Label: "calm"}, {Label: "rude", Desc: "shouting"}},
			}},
			want: `{"r":{"type":"score","instructions":"how rude","criteria":["calm","shouting"]}}`,
		},
		{
			name: "should send a body rate's undescribed level as null",
			questions: []plan.Question{{
				ID: "r", Shape: plan.Rate, Instructions: "how rude", Levels: []plan.Level{{}, {Desc: "loud"}},
			}},
			want: `{"r":{"type":"score","instructions":"how rude","criteria":[null,"loud"]}}`,
		},
		{
			name: "should send a pick's options in order",
			questions: []plan.Question{{
				ID: "t", Shape: plan.Pick, Instructions: "which team",
				Options: []plan.Option{{Name: "platform"}, {Name: "billing", Desc: "money"}},
			}},
			want: `{"t":{"type":"choice","instructions":"which team","criteria":{"platform":null,"billing":"money"}}}`,
		},
		{
			name: "should send a yes or no rubric with a missing side as empty",
			questions: []plan.Question{{
				ID: "u", Instructions: "is it urgent", Criteria: &plan.YesNoCriteria{Yes: "shouting"},
			}},
			want: `{"u":{"type":"noul","instructions":"is it urgent","criteria":{"true":"shouting","false":""}}}`,
		},
		{
			name:      "should send a box with its instructions alone",
			questions: []plan.Question{{ID: "b", Shape: plan.Box, Instructions: "the mug"}},
			want:      `{"b":{"type":"box","instructions":"the mug"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(plan.WireAll(tc.questions))
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}

			if string(encoded) != tc.want {
				t.Errorf("wire\n got: %s\nwant: %s", encoded, tc.want)
			}
		})
	}
}
