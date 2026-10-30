package cli

import (
	"context"

	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/stored"
)

func resumeLabelled(
	ctx context.Context, out *answersfile.File, flags runConfig, namer *jq.Expr, built *plan.Plan, set labelledSet,
) (resumedSet, error) {
	resumed := resumedSet{pending: set.records, answered: make([]output.Record, len(set.records))}

	book, err := answersfile.ResumeLedger(ctx, out, answersfile.Format{
		Mode: output.JSON, Merge: merging(flags), MergeKey: mergeKey(flags), Namer: namer,
	})
	if err != nil || book == nil {
		return resumed, err
	}

	var found []storedLine

	for i, rec := range set.records {
		if at, ok := book.StoredAt(rec.id); ok {
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
			book.Forget(rec.id)

			continue
		}

		resumed.answered[stored.index] = stored.record
	}

	resumed.book = book
	resumed.pending = nil

	for _, entry := range set.inputs {
		if entry.record < 0 {
			book.Keep(entry.id)

			continue
		}

		rec := &set.records[entry.record]
		slot, _, known := book.Admit(rec.id)
		rec.slot = slot

		if known {
			resumed.stored++
		} else {
			resumed.pending = append(resumed.pending, *rec)
		}
	}

	book.End()

	return resumed, nil
}

type resumedSet struct {
	book     *answersfile.Ledger
	pending  []labelledRecord
	answered []output.Record
	stored   int
}

type storedLine struct {
	index  int
	at     answersfile.Span
	record output.Record
}

func storedAnswers(ctx context.Context, out *answersfile.File, lines []storedLine, built *plan.Plan) error {
	spans := make([]answersfile.Span, len(lines))
	for i, stored := range lines {
		spans[i] = stored.at
	}

	return out.ReadSpans(ctx, spans, func(i int, text []byte) {
		lines[i].record = stored.Record(built, text)
	})
}
