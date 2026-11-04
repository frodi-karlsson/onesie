package stored_test

import (
	"reflect"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/stored"
)

func TestRecord(t *testing.T) {
	t.Parallel()

	built := &plan.Plan{Questions: []plan.Question{
		{ID: "urgent", Shape: plan.Noul},
		{ID: "team", Shape: plan.Pick},
	}}
	confidence := 0.8

	tests := []struct {
		name string
		line string
		want output.Record
	}{
		{
			name: "should read the model, the usage and every answer",
			line: `{"id":"a","model":"m-1","usage":{"input_tokens":3,"output_tokens":1},` +
				`"urgent":{"value":0.9},"team":{"value":"billing","confidence":0.8}}`,
			want: output.Record{
				Model: "m-1",
				Usage: &onesie.Usage{InputTokens: 3, OutputTokens: 1},
				Answers: []output.Named{
					{ID: "urgent", Answer: &answer.Answer{Value: 0.9}},
					{ID: "team", Answer: &answer.Answer{Value: "billing", Confidence: &confidence}},
				},
			},
		},
		{
			name: "should leave out an answer that does not decode",
			line: `{"urgent":"not an answer","team":{"value":"sales"}}`,
			want: output.Record{Answers: []output.Named{{ID: "team", Answer: &answer.Answer{Value: "sales"}}}},
		},
		{
			name: "should leave out a model and usage that do not decode",
			line: `{"model":7,"usage":"lots","urgent":{"value":0.1}}`,
			want: output.Record{Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.1}}}},
		},
		{
			name: "should read a line that is not an object as an empty record",
			line: `["urgent"]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := stored.Record(built, []byte(tc.line)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Record = %+v, want %+v", got, tc.want)
			}
		})
	}
}
