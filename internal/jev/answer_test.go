package jev_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

const sampleResult = `{
  "model": "jev-1.13.0",
  "answers": {
    "is_urgent": {"type": "noul", "noul": 0.95},
    "department": {
      "type": "choice",
      "choice": "billing",
      "probabilities": {"billing": 0.88, "technical": 0.12, "sales": 0.0},
      "confidence": 0.81
    },
    "frustration": {
      "type": "score",
      "score": 1.05,
      "legend": {"0": "Calm", "1": "Frustrated", "2": "Very angry"},
      "probabilities": {"0": 0.0, "1": 0.95, "2": 0.05},
      "confidence": 0.92
    }
  },
  "usage": {"input_tokens": 296, "output_tokens": 20}
}`

func loadSample(t *testing.T) *jev.Result {
	t.Helper()

	var result jev.Result
	if err := json.Unmarshal([]byte(sampleResult), &result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return &result
}

func TestResultUnmarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("should decode every answer type in one response", func(t *testing.T) {
		t.Parallel()

		result := loadSample(t)

		if result.Model != "jev-1.13.0" {
			t.Errorf("model got %q, want %q", result.Model, "jev-1.13.0")
		}

		if result.Usage.InputTokens != 296 || result.Usage.OutputTokens != 20 {
			t.Errorf("usage got %+v", result.Usage)
		}

		if len(result.Answers) != 3 {
			t.Fatalf("answer count got %d, want 3", len(result.Answers))
		}

		for name, want := range map[string]string{
			"is_urgent": "noul", "department": "choice", "frustration": "score",
		} {
			if got := result.Answers[name].Kind(); got != want {
				t.Errorf("%s kind got %q, want %q", name, got, want)
			}
		}
	})

	t.Run("should reject an unknown answer type", func(t *testing.T) {
		t.Parallel()

		body := `{"model":"m","answers":{"x":{"type":"tarot"}},"usage":{}}`

		var result jev.Result
		if err := json.Unmarshal([]byte(body), &result); err == nil {
			t.Fatalf("expected an error, got none")
		}
	})

	t.Run("should round trip back to the wire shape", func(t *testing.T) {
		t.Parallel()

		encoded, err := json.Marshal(loadSample(t))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var again jev.Result
		if decodeErr := json.Unmarshal(encoded, &again); decodeErr != nil {
			t.Fatalf("re-decoding failed: %v", decodeErr)
		}

		noul, err := again.Noul("is_urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if noul.Noul != 0.95 {
			t.Errorf("noul got %v, want 0.95", noul.Noul)
		}
	})
}

func TestResultAccessors(t *testing.T) {
	t.Parallel()

	t.Run("should return the noul answer", func(t *testing.T) {
		t.Parallel()

		got, err := loadSample(t).Noul("is_urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got.Noul != 0.95 {
			t.Errorf("got %v, want 0.95", got.Noul)
		}
	})

	t.Run("should return the choice answer with a summing distribution", func(t *testing.T) {
		t.Parallel()

		got, err := loadSample(t).Choice("department")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got.Choice != "billing" || got.Confidence != 0.81 {
			t.Errorf("got %+v", got)
		}

		if p, ok := got.Probability("technical"); !ok || p != 0.12 {
			t.Errorf("probability got %v %v, want 0.12 true", p, ok)
		}
	})

	t.Run("should report an unknown option rather than returning zero", func(t *testing.T) {
		t.Parallel()

		got, err := loadSample(t).Choice("department")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, ok := got.Probability("typo"); ok {
			t.Errorf("an unknown option was reported as present")
		}
	})

	t.Run("should return the score answer with its legend", func(t *testing.T) {
		t.Parallel()

		got, err := loadSample(t).Score("frustration")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got.Score != 1.05 {
			t.Errorf("score got %v, want 1.05", got.Score)
		}

		if got.Legend["1"] != "Frustrated" {
			t.Errorf("legend got %+v", got.Legend)
		}
	})

	t.Run("should report a missing answer", func(t *testing.T) {
		t.Parallel()

		_, err := loadSample(t).Noul("absent")

		var answerErr *jev.AnswerError
		if !errors.As(err, &answerErr) {
			t.Fatalf("expected an *AnswerError, got %T", err)
		}

		if !answerErr.Missing {
			t.Errorf("Missing should be set for an absent answer")
		}
	})

	t.Run("should report a type mismatch", func(t *testing.T) {
		t.Parallel()

		_, err := loadSample(t).Noul("department")
		if err == nil {
			t.Fatalf("expected an error, got none")
		}

		want := `jev: answer "department" is a choice, not a noul`
		if err.Error() != want {
			t.Errorf("\n got: %s\nwant: %s", err.Error(), want)
		}
	})
}
