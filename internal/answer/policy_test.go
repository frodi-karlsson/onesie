package answer_test

import (
	"encoding/json"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestApply(t *testing.T) {
	t.Parallel()

	threshold := 0.85
	minConfidence := 0.7
	low := 0.41
	high := 0.81
	exact := 0.7

	tests := []struct {
		name     string
		question plan.Question
		in       *answer.Answer
		want     string
	}{
		{
			name:     "should leave an answer untouched with no policy",
			question: plan.Question{ID: "urgent", Shape: plan.Noul},
			in:       &answer.Answer{Value: 0.92},
			want:     `{"value":0.92}`,
		},
		{
			name: "should decide true above the threshold",
			question: plan.Question{
				ID: "urgent", Shape: plan.Noul,
				Policy: plan.Policy{Threshold: &threshold},
			},
			in:   &answer.Answer{Value: 0.92},
			want: `{"value":0.92,"decision":true}`,
		},
		{
			name: "should decide true exactly at the threshold",
			question: plan.Question{
				ID: "urgent", Shape: plan.Noul,
				Policy: plan.Policy{Threshold: &threshold},
			},
			in:   &answer.Answer{Value: 0.85},
			want: `{"value":0.85,"decision":true}`,
		},
		{
			name: "should decide false below the threshold",
			question: plan.Question{
				ID: "urgent", Shape: plan.Noul,
				Policy: plan.Policy{Threshold: &threshold},
			},
			in:   &answer.Answer{Value: 0.12},
			want: `{"value":0.12,"decision":false}`,
		},
		{
			name: "should substitute the fallback below minimum confidence",
			question: plan.Question{
				ID: "team", Shape: plan.Pick,
				Policy: plan.Policy{
					MinConfidence: &minConfidence,
					Fallback:      &plan.Fallback{Text: "human"},
				},
			},
			in: &answer.Answer{Value: "technical", Confidence: &low},
			want: `{"value":"technical","confidence":0.41,` +
				`"fallback":"low_confidence","decision":"human"}`,
		},
		{
			name: "should keep the model's answer above minimum confidence",
			question: plan.Question{
				ID: "team", Shape: plan.Pick,
				Policy: plan.Policy{
					MinConfidence: &minConfidence,
					Fallback:      &plan.Fallback{Text: "human"},
				},
			},
			in:   &answer.Answer{Value: "technical", Confidence: &high},
			want: `{"value":"technical","confidence":0.81,"decision":"technical"}`,
		},
		{
			name: "should keep the model's answer exactly at minimum confidence",
			question: plan.Question{
				ID: "team", Shape: plan.Pick,
				Policy: plan.Policy{
					MinConfidence: &minConfidence,
					Fallback:      &plan.Fallback{Text: "human"},
				},
			},
			in:   &answer.Answer{Value: "technical", Confidence: &exact},
			want: `{"value":"technical","confidence":0.7,"decision":"technical"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answer.Apply(tc.question, tc.in)

			encoded, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}

			if string(encoded) != tc.want {
				t.Errorf("got  %s\nwant %s", encoded, tc.want)
			}
		})
	}
}

func TestFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question plan.Question
		want     string
		wantNil  bool
	}{
		{
			name:     "should emit nothing for a question with no fallback",
			question: plan.Question{ID: "team", Shape: plan.Pick},
			wantNil:  true,
		},
		{
			name: "should emit the fallback with no value on failure",
			question: plan.Question{
				ID: "team", Shape: plan.Pick,
				Policy: plan.Policy{Fallback: &plan.Fallback{Text: "refuse"}},
			},
			want: `{"fallback":"error","decision":"refuse"}`,
		},
		{
			name: "should keep a false yes/no decision boolean on failure",
			question: plan.Question{
				ID: "urgent", Shape: plan.Noul,
				Policy: plan.Policy{Fallback: &plan.Fallback{Text: "false", Boolean: false}},
			},
			want: `{"fallback":"error","decision":false}`,
		},
		{
			name: "should keep a true yes/no decision boolean on failure",
			question: plan.Question{
				ID: "urgent", Shape: plan.Noul,
				Policy: plan.Policy{Fallback: &plan.Fallback{Text: "true", Boolean: true}},
			},
			want: `{"fallback":"error","decision":true}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := answer.Failed(tc.question)

			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}

				return
			}

			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}

			if string(encoded) != tc.want {
				t.Errorf("got  %s\nwant %s", encoded, tc.want)
			}
		})
	}
}
