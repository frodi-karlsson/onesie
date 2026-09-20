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

func TestValidateQuestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions map[string]jev.Question
		wantErr   string
	}{
		{
			name:      "should reject an empty question set",
			questions: map[string]jev.Question{},
			wantErr:   "jev: at least one question is required",
		},
		{
			name:      "should reject a nil question set",
			questions: nil,
			wantErr:   "jev: at least one question is required",
		},
		{
			name:      "should reject a score with one level",
			questions: map[string]jev.Question{"severity": jev.Score{Criteria: jev.Levels("Only one")}},
			wantErr:   `jev: score question "severity" has 1 criteria, at least two are required`,
		},
		{
			name:      "should reject a score with no levels",
			questions: map[string]jev.Question{"severity": jev.Score{}},
			wantErr:   `jev: score question "severity" has 0 criteria, at least two are required`,
		},
		{
			name:      "should reject a score behind a pointer",
			questions: map[string]jev.Question{"severity": &jev.Score{Criteria: jev.Levels("Only one")}},
			wantErr:   `jev: score question "severity" has 1 criteria, at least two are required`,
		},
		{
			name: "should name the first offender in sorted order",
			questions: map[string]jev.Question{
				"zebra": jev.Score{Criteria: jev.Levels("One")},
				"alpha": jev.Score{Criteria: jev.Levels("One")},
			},
			wantErr: `jev: score question "alpha" has 1 criteria, at least two are required`,
		},
		{
			name: "should accept a valid mixed set",
			questions: map[string]jev.Question{
				"urgent":   jev.Noul{Instructions: "Urgent?"},
				"team":     jev.Choice{Instructions: "Which?", Criteria: map[string]any{"a": nil, "b": nil}},
				"severity": jev.Score{Instructions: "How bad?", Criteria: jev.Levels("Low", "High")},
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

		err := jev.ValidateQuestions(map[string]jev.Question{
			"severity": jev.Score{Criteria: jev.Levels("One")},
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
