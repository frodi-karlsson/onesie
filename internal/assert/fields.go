package assert

import (
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

var fields = []field{
	{
		name:      "value",
		available: func(plan.Question) bool { return true },
		typeOf: func(q plan.Question) valueType {
			if q.Shape == plan.Noul {
				return typeNumber
			}

			return typeString
		},
		read: func(a *answer.Answer, _ string) any { return a.Value },
	},
	{
		name:      "confidence",
		available: notNoul,
		typeOf:    constant(typeNumber),
		read:      func(a *answer.Answer, _ string) any { return unwrap(a.Confidence) },
	},
	{
		name:      "score",
		available: rated,
		typeOf:    constant(typeNumber),
		read:      func(a *answer.Answer, _ string) any { return unwrap(a.Score) },
	},
	{
		name:      "norm",
		available: rated,
		typeOf:    constant(typeNumber),
		read:      func(a *answer.Answer, _ string) any { return unwrap(a.Norm) },
	},
	{
		name:      "p",
		takesKey:  true,
		available: notNoul,
		typeOf:    constant(typeNumber),
		read:      func(a *answer.Answer, key string) any { return probability(a, key) },
	},
	{
		name:      "decision",
		available: decided,
		reason:    reasonDecision,
		typeOf: func(q plan.Question) valueType {
			if q.Shape == plan.Noul {
				return typeBoolean
			}

			return typeString
		},
		// Apply sets Decision only alongside Decided, so an undecided answer already reads as nil
		// and Decided adds nothing here.
		read: func(a *answer.Answer, _ string) any { return a.Decision },
	},
	{
		name:      "fallback",
		available: policed,
		reason:    reasonFallback,
		typeOf:    constant(typeString),
		// §17.3 reads an absent fallback as the empty string, which is what the field already
		// holds when no policy replaced the answer.
		read: func(a *answer.Answer, _ string) any { return a.Fallback },
	},
}

const (
	reasonShape unavailableReason = iota
	reasonDecision
	reasonFallback
)

func fieldsOf(q plan.Question) []string {
	available := make([]string, 0, len(fields))

	for _, f := range fields {
		if f.available(q) {
			available = append(available, f.name)
		}
	}

	return available
}

func lookup(name string) (field, bool) {
	for _, f := range fields {
		if f.name == name {
			return f, true
		}
	}

	return field{}, false
}

type field struct {
	name      string
	takesKey  bool
	available func(q plan.Question) bool
	typeOf    func(q plan.Question) valueType
	read      func(a *answer.Answer, key string) any
	reason    unavailableReason
}

type unavailableReason int

func notNoul(q plan.Question) bool { return q.Shape != plan.Noul }

func rated(q plan.Question) bool { return q.Shape == plan.Rate }

func decided(q plan.Question) bool {
	if q.Shape == plan.Noul {
		return q.Policy.Threshold != nil
	}

	// answer.Apply has nothing to substitute without a fallback and leaves decision unset, so the
	// path is only there when both flags are, which §11 already requires of the pair.
	return q.Policy.MinConfidence != nil && q.Policy.Fallback != nil
}

func policed(q plan.Question) bool {
	return q.Policy.Threshold != nil || q.Policy.MinConfidence != nil || q.Policy.Fallback != nil
}

func constant(t valueType) func(plan.Question) valueType {
	return func(plan.Question) valueType { return t }
}
