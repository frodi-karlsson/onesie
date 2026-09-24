//go:build integration

package jev_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

const (
	urgentState = "Help! My payouts have been failing for 3 days and I am losing customers."
	calmState   = "Thanks, that fixed it! Great service."
)

func liveClient(t *testing.T, opts ...jev.Option) *jev.Client {
	t.Helper()

	base := []jev.Option{jev.WithAPIKey(apiKey(t)), jev.WithUserAgent("onesie-integration")}

	client, err := jev.New(append(base, opts...)...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return client
}

func liveContext(t *testing.T, d time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)

	return ctx
}

func TestLiveSystemOne(t *testing.T) {
	t.Parallel()

	// One request carrying every question type. Questions are evaluated in parallel and in
	// isolation, so batching costs almost nothing over a single question.
	t.Run("should answer all three question types in one request", func(t *testing.T) {
		t.Parallel()

		client := liveClient(t)

		result, err := client.SystemOne(liveContext(t, time.Minute), jev.Request{
			State: urgentState,
			Questions: jev.Questions{
				{
					ID:       "is_urgent",
					Question: jev.Noul{Instructions: "Does this message convey urgency?"},
				},
				{
					ID: "department",
					Question: jev.Choice{
						Instructions: "Which team should handle this?",
						Criteria: jev.Criteria{
							{Name: "billing", Desc: "Payments, invoicing, payouts, refunds"},
							{Name: "technical", Desc: "Bugs, outages, integrations"},
							{Name: "sales", Desc: "Pricing, upgrades, new accounts"},
						},
					},
				},
				{
					ID: "frustration",
					Question: jev.Score{
						Instructions: "How frustrated is the customer?",
						Criteria:     jev.Levels("Calm", "Frustrated", "Very angry"),
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Model == jev.DefaultModel {
			t.Errorf("model should resolve to a versioned id, got the alias %q", result.Model)
		}

		if result.RequestID == "" {
			t.Errorf("no request id came back")
		}

		if result.Usage.InputTokens <= 0 {
			t.Errorf("input tokens got %d, want a positive count", result.Usage.InputTokens)
		}

		urgency, err := result.Noul("is_urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// A wide band. The point is the direction, not a calibration assertion.
		if urgency.Noul < 0.7 {
			t.Errorf("urgency got %v, want above 0.7 for an explicitly urgent message", urgency.Noul)
		}

		department, err := result.Choice("department")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(department.Probabilities) != 3 {
			t.Errorf("probability count got %d, want 3", len(department.Probabilities))
		}

		total := 0.0
		for _, p := range department.Probabilities {
			total += p
		}

		if total < 0.99 || total > 1.01 {
			t.Errorf("probabilities sum to %v, want 1", total)
		}

		if _, ok := department.Probability(department.Choice); !ok {
			t.Errorf("the winning option has no probability entry")
		}

		if department.Confidence < 0 || department.Confidence > 1 {
			t.Errorf("confidence got %v, want a value from 0 to 1", department.Confidence)
		}

		frustration, err := result.Score("frustration")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if frustration.Score < 0 || frustration.Score > 2 {
			t.Errorf("score got %v, want a value within the three level rubric", frustration.Score)
		}

		if frustration.Legend["0"] != "Calm" {
			t.Errorf("legend got %+v, want level 0 to echo the criteria", frustration.Legend)
		}

		if len(frustration.Probabilities) != 3 {
			t.Errorf("score probability count got %d, want 3", len(frustration.Probabilities))
		}
	})

	t.Run("should move the answer when the state changes", func(t *testing.T) {
		t.Parallel()

		client := liveClient(t)
		ctx := liveContext(t, time.Minute)

		question := jev.Questions{
			{ID: "is_urgent", Question: jev.Noul{Instructions: "Does this message convey urgency?"}},
		}

		hot, err := client.SystemOne(ctx, jev.Request{State: urgentState, Questions: question})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cold, err := client.SystemOne(ctx, jev.Request{State: calmState, Questions: question})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		hotValue, err := hot.Noul("is_urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		coldValue, err := cold.Noul("is_urgent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if coldValue.Noul >= hotValue.Noul {
			t.Errorf("a calm message scored %v against an urgent %v", coldValue.Noul, hotValue.Noul)
		}

		if coldValue.Noul > 0.3 {
			t.Errorf("calm urgency got %v, want below 0.3", coldValue.Noul)
		}
	})

	t.Run("should accept a structured state and a backtick field reference", func(t *testing.T) {
		t.Parallel()

		client := liveClient(t)

		result, err := client.SystemOne(liveContext(t, time.Minute), jev.Request{
			State: map[string]any{
				"ticket": map[string]any{
					"messages": []any{
						map[string]any{
							"from": "customer",
							"text": "I was charged twice for order A-104. Please refund the duplicate.",
						},
					},
				},
				"refund_policy": "Duplicate charges are eligible for a refund.",
			},
			Questions: jev.Questions{
				{
					ID: "refund_requested",
					Question: jev.Noul{
						Instructions: "Does `ticket.messages[0].text` request a refund?",
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		requested, err := result.Noul("refund_requested")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if requested.Noul < 0.7 {
			t.Errorf("refund requested got %v, want above 0.7", requested.Noul)
		}
	})

	t.Run("should accept a noul with criteria", func(t *testing.T) {
		t.Parallel()

		client := liveClient(t)

		result, err := client.SystemOne(liveContext(t, time.Minute), jev.Request{
			State: urgentState,
			Questions: jev.Questions{
				{
					ID: "time_sensitive",
					Question: jev.Noul{
						Instructions: "Is this time sensitive?",
						Criteria: &jev.NoulCriteria{
							True:  "The customer states or implies a deadline or ongoing loss",
							False: "No time pressure is expressed",
						},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		answer, err := result.Noul("time_sensitive")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if answer.Noul < 0 || answer.Noul > 1 {
			t.Errorf("noul got %v, want a value from 0 to 1", answer.Noul)
		}
	})
}

func TestLiveListModels(t *testing.T) {
	t.Parallel()

	t.Run("should list at least one named model", func(t *testing.T) {
		t.Parallel()

		client := liveClient(t)

		models, err := client.ListModels(liveContext(t, 30*time.Second))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(models) == 0 {
			t.Fatalf("no models came back")
		}

		for _, model := range models {
			if model.Name == "" {
				t.Errorf("a model came back with no name: %+v", model)
			}
		}
	})
}

func TestLiveErrors(t *testing.T) {
	t.Parallel()

	t.Run("should return ErrAuthentication for a bad key", func(t *testing.T) {
		t.Parallel()

		// Call apiKey first so this subtest skips alongside the others when no key is configured.
		_ = apiKey(t)

		client, err := jev.New(
			jev.WithAPIKey("sk-definitely-not-a-real-key"),
			jev.WithUserAgent("onesie-integration"),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = client.SystemOne(liveContext(t, 30*time.Second), jev.Request{
			State:     "x",
			Questions: jev.Questions{{ID: "q", Question: jev.Noul{Instructions: "Is this a test?"}}},
		})

		if !errors.Is(err, jev.ErrAuthentication) {
			t.Fatalf("error got %v, want ErrAuthentication", err)
		}

		var api *jev.APIError
		if !errors.As(err, &api) {
			t.Fatalf("expected an *APIError, got %T", err)
		}

		if api.Status != 401 {
			t.Errorf("status got %d, want 401", api.Status)
		}
	})

	t.Run("should return an APIError for an unknown model", func(t *testing.T) {
		t.Parallel()

		client := liveClient(t)

		_, err := client.SystemOne(liveContext(t, 30*time.Second), jev.Request{
			State:     "x",
			Model:     "onesie-does-not-exist",
			Questions: jev.Questions{{ID: "q", Question: jev.Noul{Instructions: "Is this a test?"}}},
		})

		var api *jev.APIError
		if !errors.As(err, &api) {
			t.Fatalf("expected an *APIError, got %v", err)
		}

		if api.Status < 400 || api.Status >= 500 {
			t.Errorf("status got %d, want a 4xx", api.Status)
		}

		if api.Error() == "" {
			t.Errorf("the error carried no message")
		}
	})
}
