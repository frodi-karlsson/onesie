package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func resumeLabelled(
	ctx context.Context, out *outFile, flags *runFlags, namer *jq.Expr, built *plan.Plan, set labelledSet,
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
		named := namedRecord{id: rec.id}
		_, known := book.admit(&named)
		rec.slot = named.slot

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

func (l *ledger) storedAt(id any) (span, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id == nil {
		return span{}, false
	}

	stored, found := l.answered[keyOf(idText(id))]

	return stored.at, found
}

func (l *ledger) keep(id any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id == nil {
		return
	}

	key := keyOf(idText(id))

	// Kept in its place and nothing more. The record is not asked, so it is neither pending nor
	// skipped.
	if stored, found := l.answered[key]; found {
		delete(l.answered, key)
		l.lines = append(l.lines, stored.at)
	}
}

func (l *ledger) forget(id any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.answered, keyOf(idText(id)))
}

func storedAnswers(ctx context.Context, out *outFile, lines []storedLine, built *plan.Plan) (err error) {
	if len(lines) == 0 {
		return nil
	}

	file, err := out.open(out.path, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("onesie: reading %s to resume: %w", out.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	// In file order, so the reads move forward through the file.
	order := make([]*storedLine, len(lines))
	for i := range lines {
		order[i] = &lines[i]
	}

	slices.SortFunc(order, func(a, b *storedLine) int {
		return cmp.Compare(a.at.start, b.at.start)
	})

	var text []byte

	for _, stored := range order {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		text = slices.Grow(text[:0], int(stored.at.end-stored.at.start))[:stored.at.end-stored.at.start]
		if _, readErr := file.ReadAt(text, stored.at.start); readErr != nil {
			return fmt.Errorf("onesie: reading %s to resume: %w", out.path, readErr)
		}

		stored.record = storedRecord(built, text)
	}

	return nil
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

	var usage jev.Usage
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
