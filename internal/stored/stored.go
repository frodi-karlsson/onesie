// Package stored reads a line of an answers file back into the record it was written from.
package stored

import (
	"encoding/json"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Record reads a stored answers line back for the questions of built. Each field is decoded on its
// own, so one that does not decode leaves only itself out, and a line that is not an object is empty.
func Record(built *plan.Plan, text []byte) output.Record {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(text, &fields); err != nil {
		return output.Record{}
	}

	var record output.Record

	if json.Unmarshal(fields["model"], &record.Model) != nil {
		record.Model = ""
	}

	var usage onesie.Usage
	if json.Unmarshal(fields["usage"], &usage) == nil {
		record.Usage = &usage
	}

	for _, question := range built.Questions {
		var stored struct {
			Value      any      `json:"value"`
			Confidence *float64 `json:"confidence"`
		}

		raw, found := fields[question.ID]
		if !found || json.Unmarshal(raw, &stored) != nil {
			continue
		}

		record.Answers = append(record.Answers, output.Named{
			ID: question.ID, Answer: &answer.Answer{Value: stored.Value, Confidence: stored.Confidence},
		})
	}

	return record
}
