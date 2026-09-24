package cli

import (
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func wireAll(questions []plan.Question) jev.Questions {
	wired := make(jev.Questions, 0, len(questions))
	for _, question := range questions {
		wired = append(wired, jev.NamedQuestion{ID: question.ID, Question: wire(question)})
	}

	return wired
}

func wire(q plan.Question) jev.Question {
	switch q.Shape {
	case plan.Pick:
		criteria := make(jev.Criteria, 0, len(q.Options))
		for _, option := range q.Options {
			criteria = append(criteria, jev.NamedCriterion{Name: option.Name, Desc: option.Desc})
		}

		return jev.Choice{Instructions: q.Instructions, Criteria: criteria}
	case plan.Rate:
		criteria := make([]any, 0, len(q.Levels))

		for _, level := range q.Levels {
			// An undescribed rubric sends the labels themselves, which is the documented
			// behaviour for a bare --rate. A body question has no labels, so substituting an
			// empty string would rewrite its criteria on the way out.
			if level.Desc == nil && q.Labelled {
				criteria = append(criteria, level.Label)

				continue
			}

			criteria = append(criteria, level.Desc)
		}

		return jev.Score{Instructions: q.Instructions, Criteria: criteria}
	default:
		if q.Criteria == nil {
			return jev.Noul{Instructions: q.Instructions}
		}

		return jev.Noul{
			Instructions: q.Instructions,
			Criteria:     &jev.NoulCriteria{True: orEmpty(q.Criteria.Yes), False: orEmpty(q.Criteria.No)},
		}
	}
}

func orEmpty(desc any) any {
	if desc == nil {
		return ""
	}

	return desc
}
