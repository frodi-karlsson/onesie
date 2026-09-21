package jev_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestQuestionMarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question jev.Question
		want     string
	}{
		{
			name:     "should marshal a bare noul without a criteria key",
			question: jev.Noul{Instructions: "Does this convey urgency?"},
			want:     `{"type":"noul","instructions":"Does this convey urgency?"}`,
		},
		{
			name: "should marshal noul criteria",
			question: jev.Noul{
				Instructions: "Urgent?",
				Criteria:     &jev.NoulCriteria{True: "Time sensitive", False: "Not urgent"},
			},
			want: `{"type":"noul","instructions":"Urgent?","criteria":{"true":"Time sensitive","false":"Not urgent"}}`,
		},
		{
			name: "should marshal a choice with its criteria map",
			question: jev.Choice{
				Instructions: "Which team?",
				Criteria:     map[string]any{"billing": "Payments", "technical": nil},
			},
			want: `{"type":"choice","instructions":"Which team?","criteria":{"billing":"Payments","technical":null}}`,
		},
		{
			name: "should marshal a score with ordered levels",
			question: jev.Score{
				Instructions: "How severe?",
				Criteria:     jev.Levels("Cosmetic", "Degraded", "Blocking"),
			},
			want: `{"type":"score","instructions":"How severe?","criteria":["Cosmetic","Degraded","Blocking"]}`,
		},
		{
			name:     "should marshal structured instructions",
			question: jev.Noul{Instructions: map[string]any{"question": "Same person?"}},
			want:     `{"type":"noul","instructions":{"question":"Same person?"}}`,
		},
		{
			name:     "should marshal nil instructions as null",
			question: jev.Noul{},
			want:     `{"type":"noul","instructions":null}`,
		},
		{
			name:     "should marshal through a pointer",
			question: &jev.Noul{Instructions: "Urgent?"},
			want:     `{"type":"noul","instructions":"Urgent?"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tc.question)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestQuestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions jev.Questions
		want      string
	}{
		{
			name: "should marshal to an object in slice order",
			questions: jev.Questions{
				{ID: "zebra", Question: jev.Noul{Instructions: "z"}},
				{ID: "alpha", Question: jev.Noul{Instructions: "a"}},
			},
			want: `{"zebra":{"type":"noul","instructions":"z"},` +
				`"alpha":{"type":"noul","instructions":"a"}}`,
		},
		{
			name:      "should marshal an empty set to an empty object",
			questions: jev.Questions{},
			want:      `{}`,
		},
		{
			name: "should escape a key that carries a quote",
			questions: jev.Questions{
				{ID: `a"b`, Question: jev.Noul{Instructions: "q"}},
			},
			want: `{"a\"b":{"type":"noul","instructions":"q"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tc.questions)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("Marshal = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestValidateQuestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions jev.Questions
		wantErr   string
	}{
		{
			name:      "should reject an empty question set",
			questions: jev.Questions{},
			wantErr:   "jev: at least one question is required",
		},
		{
			name:      "should reject a nil question set",
			questions: nil,
			wantErr:   "jev: at least one question is required",
		},
		{
			name: "should reject a score with one level",
			questions: jev.Questions{
				{ID: "severity", Question: jev.Score{Criteria: jev.Levels("Only one")}},
			},
			wantErr: `jev: score question "severity" has 1 criteria, at least two are required`,
		},
		{
			name:      "should reject a score with no levels",
			questions: jev.Questions{{ID: "severity", Question: jev.Score{}}},
			wantErr:   `jev: score question "severity" has 0 criteria, at least two are required`,
		},
		{
			name: "should reject a score behind a pointer",
			questions: jev.Questions{
				{ID: "severity", Question: &jev.Score{Criteria: jev.Levels("Only one")}},
			},
			wantErr: `jev: score question "severity" has 1 criteria, at least two are required`,
		},
		{
			name: "should name the first offender in slice order",
			questions: jev.Questions{
				{ID: "alpha", Question: jev.Score{Criteria: jev.Levels("One")}},
				{ID: "zebra", Question: jev.Score{Criteria: jev.Levels("One")}},
			},
			wantErr: `jev: score question "alpha" has 1 criteria, at least two are required`,
		},
		{
			name: "should reject a duplicate question id",
			questions: jev.Questions{
				{ID: "a", Question: jev.Noul{Instructions: "one"}},
				{ID: "a", Question: jev.Noul{Instructions: "two"}},
			},
			wantErr: "jev: duplicate question name a",
		},
		{
			name:      "should reject a nil question",
			questions: jev.Questions{{ID: "a", Question: nil}},
			wantErr:   "jev: question a must not be nil",
		},
		{
			name: "should accept a valid mixed set",
			questions: jev.Questions{
				{ID: "urgent", Question: jev.Noul{Instructions: "Urgent?"}},
				{
					ID: "team",
					Question: jev.Choice{
						Instructions: "Which?",
						Criteria:     map[string]any{"a": nil, "b": nil},
					},
				},
				{
					ID: "severity",
					Question: jev.Score{
						Instructions: "How bad?",
						Criteria:     jev.Levels("Low", "High"),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := jev.ValidateQuestions(tc.questions)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("expected an error, got none")
			}

			if err.Error() != tc.wantErr {
				t.Errorf("\n got: %s\nwant: %s", err.Error(), tc.wantErr)
			}

			if !errors.Is(err, jev.ErrValidation) {
				t.Errorf("a validation failure should match ErrValidation")
			}
		})
	}
}

func TestValidateQuestionsNamesTheQuestion(t *testing.T) {
	t.Parallel()

	t.Run("should carry the offending question name as a field", func(t *testing.T) {
		t.Parallel()

		err := jev.ValidateQuestions(jev.Questions{
			{ID: "severity", Question: jev.Score{Criteria: jev.Levels("One")}},
		})

		var invalid *jev.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("expected a *ValidationError, got %T", err)
		}

		if invalid.Question != "severity" {
			t.Errorf("question got %q, want %q", invalid.Question, "severity")
		}
	})
}

func TestLevels(t *testing.T) {
	t.Parallel()

	t.Run("should widen strings into score criteria", func(t *testing.T) {
		t.Parallel()

		got := jev.Levels("Low", "High")

		if len(got) != 2 || got[0] != "Low" || got[1] != "High" {
			t.Errorf("got %#v", got)
		}
	})

	t.Run("should return an empty slice for no levels", func(t *testing.T) {
		t.Parallel()

		if got := jev.Levels(); len(got) != 0 {
			t.Errorf("got %#v, want empty", got)
		}
	})
}
