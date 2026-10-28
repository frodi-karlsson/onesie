package plan

import "github.com/frodi-karlsson/onesie-go"

// WireAll builds the questions a request sends, in the order given, for a request or a Result.Check.
func WireAll(questions []Question) onesie.Questions {
	wired := make(onesie.Questions, 0, len(questions))
	for _, question := range questions {
		wired = append(wired, onesie.NamedQuestion{ID: question.ID, Question: Wire(question)})
	}

	return wired
}

// Wire builds the question a request sends. An undescribed level of a labelled rate sends its label.
func Wire(q Question) onesie.Question {
	switch q.Shape {
	case Pick:
		criteria := make(onesie.Criteria, 0, len(q.Options))
		for _, option := range q.Options {
			criteria = append(criteria, onesie.NamedCriterion{Name: option.Name, Desc: option.Desc})
		}

		return onesie.Choice{Instructions: q.Instructions, Criteria: criteria}
	case Rate:
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

		return onesie.Score{Instructions: q.Instructions, Criteria: criteria}
	case Box:
		return onesie.Box{Instructions: q.Instructions}
	default:
		if q.Criteria == nil {
			return onesie.Noul{Instructions: q.Instructions}
		}

		return onesie.Noul{
			Instructions: q.Instructions,
			Criteria:     &onesie.NoulCriteria{True: orEmpty(q.Criteria.Yes), False: orEmpty(q.Criteria.No)},
		}
	}
}

func orEmpty(desc any) any {
	if desc == nil {
		return ""
	}

	return desc
}
