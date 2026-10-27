package cli

import (
	"context"
	"encoding/json"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func resumeLabelled(
	ctx context.Context, out *outFile, flags runConfig, namer *jq.Expr, built *plan.Plan, set labelledSet,
) (resumedSet, error) {
	resumed := resumedSet{pending: set.records, answered: make([]output.Record, len(set.records))}

	book, err := resumeLedger(ctx, out, flags, namer, output.JSON, false)
	if err != nil || book == nil {
		return resumed, err
	}

	var found []storedLine

	for i, rec := range set.records {
		if at, ok := book.storedAt(rec.id); ok {
			found = append(found, storedLine{index: i, at: at})
		}
	}

	if readErr := storedAnswers(ctx, out, found, built); readErr != nil {
		return resumedSet{}, readErr
	}

	for _, stored := range found {
		rec := set.records[stored.index]
		stored.record.ID = rec.id

		// A line the scorers cannot use answers nothing, so its record is asked again, as a stored
		// error line is, and the report and the exit code count only the new outcome.
		if _, caseErr := casesOf(built, rec, stored.record); caseErr != nil {
			book.forget(rec.id)

			continue
		}

		resumed.answered[stored.index] = stored.record
	}

	resumed.book = book
	resumed.pending = nil

	for _, entry := range set.inputs {
		if entry.record < 0 {
			book.keep(entry.id)

			continue
		}

		rec := &set.records[entry.record]
		slot, _, known := book.admit(rec.id)
		rec.slot = slot

		if known {
			resumed.stored++
		} else {
			resumed.pending = append(resumed.pending, *rec)
		}
	}

	book.end()

	return resumed, nil
}

type resumedSet struct {
	book     *ledger
	pending  []labelledRecord
	answered []output.Record
	stored   int
}

type storedLine struct {
	index  int
	at     span
	record output.Record
}

func storedAnswers(ctx context.Context, out *outFile, lines []storedLine, built *plan.Plan) error {
	spans := make([]span, len(lines))
	for i, stored := range lines {
		spans[i] = stored.at
	}

	return out.readSpans(ctx, spans, func(i int, text []byte) {
		lines[i].record = storedRecord(built, text)
	})
}

func storedRecord(built *plan.Plan, text []byte) output.Record {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(text, &fields); err != nil {
		return output.Record{}
	}

	var record output.Record

	// Each field is decoded on its own, so one that does not decode leaves only itself out. An answer
	// left out fails the answers check, and the record is asked again.
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
