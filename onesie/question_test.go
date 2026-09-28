package onesie_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/onesie"
)

func TestQuestionMarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question onesie.Question
		want     string
	}{
		{
			name:     "should marshal a bare noul without a criteria key",
			question: onesie.Noul{Instructions: "Does this convey urgency?"},
			want:     `{"type":"noul","instructions":"Does this convey urgency?"}`,
		},
		{
			name: "should marshal noul criteria",
			question: onesie.Noul{
				Instructions: "Urgent?",
				Criteria:     &onesie.NoulCriteria{True: "Time sensitive", False: "Not urgent"},
			},
			want: `{"type":"noul","instructions":"Urgent?","criteria":{"true":"Time sensitive","false":"Not urgent"}}`,
		},
		{
			name: "should marshal a choice with its criteria map",
			question: onesie.Choice{
				Instructions: "Which team?",
				Criteria: onesie.Criteria{
					{Name: "billing", Desc: "Payments"},
					{Name: "technical"},
				},
			},
			want: `{"type":"choice","instructions":"Which team?","criteria":{"billing":"Payments","technical":null}}`,
		},
		{
			name: "should marshal a score with ordered levels",
			question: onesie.Score{
				Instructions: "How severe?",
				Criteria:     onesie.Levels("Cosmetic", "Degraded", "Blocking"),
			},
			want: `{"type":"score","instructions":"How severe?","criteria":["Cosmetic","Degraded","Blocking"]}`,
		},
		{
			name:     "should marshal structured instructions",
			question: onesie.Noul{Instructions: map[string]any{"question": "Same person?"}},
			want:     `{"type":"noul","instructions":{"question":"Same person?"}}`,
		},
		{
			name:     "should marshal nil instructions as null",
			question: onesie.Noul{},
			want:     `{"type":"noul","instructions":null}`,
		},
		{
			name:     "should marshal through a pointer",
			question: &onesie.Noul{Instructions: "Urgent?"},
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

func TestQuestionsMarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions onesie.Questions
		want      string
		wantErr   string
	}{
		{
			name: "should marshal to an object in slice order",
			questions: onesie.Questions{
				{ID: "zebra", Question: onesie.Noul{Instructions: "z"}},
				{ID: "alpha", Question: onesie.Noul{Instructions: "a"}},
			},
			want: `{"zebra":{"type":"noul","instructions":"z"},` +
				`"alpha":{"type":"noul","instructions":"a"}}`,
		},
		{
			name:      "should marshal an empty set to an empty object",
			questions: onesie.Questions{},
			want:      `{}`,
		},
		{
			name:      "should marshal a nil set to an empty object",
			questions: nil,
			want:      `{}`,
		},
		{
			name: "should escape a key that carries a quote",
			questions: onesie.Questions{
				{ID: `a"b`, Question: onesie.Noul{Instructions: "q"}},
			},
			want: `{"a\"b":{"type":"noul","instructions":"q"}}`,
		},
		{
			name: "should fail when a question cannot be marshalled",
			questions: onesie.Questions{
				{ID: "a", Question: onesie.Noul{Instructions: "fine"}},
				{ID: "b", Question: onesie.Choice{
					Criteria: onesie.Criteria{{Name: "c", Desc: make(chan int)}},
				}},
			},
			wantErr: "unsupported type: chan int",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tc.questions)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error, got %s", got)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("\n got: %s\nwant it to contain: %s", err, tc.wantErr)
				}

				return
			}

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
		questions onesie.Questions
		wantErr   string
	}{
		{
			name:      "should reject an empty question set",
			questions: onesie.Questions{},
			wantErr:   "onesie: at least one question is required",
		},
		{
			name:      "should reject a nil question set",
			questions: nil,
			wantErr:   "onesie: at least one question is required",
		},
		{
			name: "should reject a score with one level",
			questions: onesie.Questions{
				{ID: "severity", Question: onesie.Score{Criteria: onesie.Levels("Only one")}},
			},
			wantErr: `onesie: score question "severity" has 1 criteria, at least two are required`,
		},
		{
			name:      "should reject a score with no levels",
			questions: onesie.Questions{{ID: "severity", Question: onesie.Score{}}},
			wantErr:   `onesie: score question "severity" has 0 criteria, at least two are required`,
		},
		{
			name: "should reject a score behind a pointer",
			questions: onesie.Questions{
				{ID: "severity", Question: &onesie.Score{Criteria: onesie.Levels("Only one")}},
			},
			wantErr: `onesie: score question "severity" has 1 criteria, at least two are required`,
		},
		{
			name: "should name the first offender in slice order",
			questions: onesie.Questions{
				{ID: "zebra", Question: onesie.Score{Criteria: onesie.Levels("One")}},
				{ID: "alpha", Question: onesie.Score{Criteria: onesie.Levels("One")}},
			},
			wantErr: `onesie: score question "zebra" has 1 criteria, at least two are required`,
		},
		{
			name: "should reject a duplicate question id",
			questions: onesie.Questions{
				{ID: "a", Question: onesie.Noul{Instructions: "one"}},
				{ID: "a", Question: onesie.Noul{Instructions: "two"}},
			},
			wantErr: `onesie: duplicate question id "a"`,
		},
		{
			name:      "should reject a nil question",
			questions: onesie.Questions{{ID: "a", Question: nil}},
			wantErr:   `onesie: question "a" must not be nil`,
		},
		{
			name:      "should reject a typed nil question",
			questions: onesie.Questions{{ID: "a", Question: (*onesie.Score)(nil)}},
			wantErr:   `onesie: question "a" must not be nil`,
		},
		{
			name: "should accept a valid mixed set",
			questions: onesie.Questions{
				{ID: "urgent", Question: onesie.Noul{Instructions: "Urgent?"}},
				{
					ID: "team",
					Question: onesie.Choice{
						Instructions: "Which?",
						Criteria:     onesie.Criteria{{Name: "a"}, {Name: "b"}},
					},
				},
				{
					ID: "severity",
					Question: onesie.Score{
						Instructions: "How bad?",
						Criteria:     onesie.Levels("Low", "High"),
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := onesie.ValidateQuestions(tc.questions)

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

			if !errors.Is(err, onesie.ErrValidation) {
				t.Errorf("a validation failure should match ErrValidation")
			}
		})
	}

	t.Run("should name the offending question", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			questions onesie.Questions
			want      string
		}{
			{
				name: "should carry the offending question id when a score is short",
				questions: onesie.Questions{
					{ID: "severity", Question: onesie.Score{Criteria: onesie.Levels("One")}},
				},
				want: "severity",
			},
			{
				name: "should carry the offending question id when an id repeats",
				questions: onesie.Questions{
					{ID: "team", Question: onesie.Noul{Instructions: "one"}},
					{ID: "team", Question: onesie.Noul{Instructions: "two"}},
				},
				want: "team",
			},
			{
				name:      "should carry the offending question id when a question is nil",
				questions: onesie.Questions{{ID: "urgent", Question: nil}},
				want:      "urgent",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				err := onesie.ValidateQuestions(tc.questions)

				var invalid *onesie.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("expected a *ValidationError, got %T", err)
				}

				if invalid.Question != tc.want {
					t.Errorf("question got %q, want %q", invalid.Question, tc.want)
				}
			})
		}
	})
}

func TestLevels(t *testing.T) {
	t.Parallel()

	t.Run("should widen strings into score criteria", func(t *testing.T) {
		t.Parallel()

		got := onesie.Levels("Low", "High")

		if len(got) != 2 || got[0] != "Low" || got[1] != "High" {
			t.Errorf("got %#v", got)
		}
	})

	t.Run("should return an empty slice for no levels", func(t *testing.T) {
		t.Parallel()

		if got := onesie.Levels(); len(got) != 0 {
			t.Errorf("got %#v, want empty", got)
		}
	})
}

func TestChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		choice onesie.Choice
		want   string
	}{
		{
			name: "should marshal criteria in slice order",
			choice: onesie.Choice{
				Instructions: "pick one",
				Criteria: onesie.Criteria{
					{Name: "zebra", Desc: "last alphabetically"},
					{Name: "alpha", Desc: "first alphabetically"},
				},
			},
			want: `{"type":"choice","instructions":"pick one","criteria":` +
				`{"zebra":"last alphabetically","alpha":"first alphabetically"}}`,
		},
		{
			name:   "should marshal absent criteria as null",
			choice: onesie.Choice{Instructions: "q"},
			want:   `{"type":"choice","instructions":"q","criteria":null}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tc.choice)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("Marshal = %s, want %s", got, tc.want)
			}
		})
	}
}
