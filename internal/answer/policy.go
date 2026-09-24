package answer

import "github.com/frodi-karlsson/onesie/internal/plan"

// Apply sets decision and fallback from the question's policy. A low confidence fallback is a
// successful request, so nothing here reports an error.
func Apply(q plan.Question, a *Answer) {
	policy := q.Policy

	if policy.Threshold != nil {
		probability, ok := a.Value.(float64)
		if !ok {
			return
		}

		a.Decision = probability >= *policy.Threshold
		a.Decided = true

		return
	}

	if policy.MinConfidence == nil || policy.Fallback == nil || a.Confidence == nil {
		return
	}

	a.Decided = true

	if *a.Confidence < *policy.MinConfidence {
		a.Decision = policy.Fallback.Text
		a.Fallback = "low_confidence"

		return
	}

	a.Decision = a.Value
}

// Failed builds the answer a question emits when the request failed after retries. It returns nil
// for a question with no fallback, which is absent from the record entirely.
func Failed(q plan.Question) *Answer {
	if q.Policy.Fallback == nil {
		return nil
	}

	out := &Answer{Fallback: "error", Decided: true}

	// A yes/no decision stays boolean whether it came from a threshold or from a fallback, so a
	// consumer can branch on the JSON type without knowing which produced it.
	if q.Shape == plan.Noul {
		out.Decision = q.Policy.Fallback.Boolean

		return out
	}

	out.Decision = q.Policy.Fallback.Text

	return out
}
