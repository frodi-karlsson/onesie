package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func records(
	ctx context.Context,
	settings rootSettings,
	inputMode input.Mode,
	flags *runFlags,
	namer *jq.Expr,
	book *ledger,
	outputMode output.Mode,
) *naming {
	stream := input.NewStream(settings.stdin, inputMode, flags.skipBlank)

	// A context of its own, since the read ahead goroutine outlives an engine that stops early, and
	// a runaway --id on it would otherwise keep running until the command's context ends.
	ctx, stop := context.WithCancel(ctx)

	return &naming{
		ctx:    ctx,
		stop:   stop,
		source: engine.Skip[input.Record](stream, flags.resumeSkip),
		namer:  namer,
		book:   book,
		mode:   outputMode,
		seen:   map[[sha256.Size]byte]int{},
	}
}

type namedRecord struct {
	input.Record
	id    any
	idErr error
	slot  int
}

type naming struct {
	ctx    context.Context
	stop   context.CancelFunc
	source engine.Source[input.Record]
	namer  *jq.Expr
	book   *ledger
	mode   output.Mode
	seen   map[[sha256.Size]byte]int
}

func (n *naming) Next() (namedRecord, bool, error) {
	for {
		rec, ok, err := n.source.Next()
		if err != nil || !ok {
			if err == nil && n.book != nil {
				n.book.end()
			}

			return namedRecord{Record: rec}, ok, err
		}

		named := n.named(rec)

		// Skipped here, as the record is read, so an answered record is never evaluated or sent.
		if n.book != nil && n.book.admit(&named) {
			continue
		}

		return named, true, nil
	}
}

func (n *naming) named(rec input.Record) namedRecord {
	if n.namer == nil || rec.Err != nil {
		return namedRecord{Record: rec}
	}

	// Named as the records are read rather than as they are evaluated, so which of two records
	// sharing an id counts as the repeat follows input order and not whichever request finished
	// first.
	return n.name(rec)
}

func (n *naming) name(rec input.Record) namedRecord {
	id, err := idOf(n.ctx, n.namer, rec)
	if err != nil {
		return namedRecord{Record: rec, idErr: err}
	}

	text := idText(id)

	if unwritable := unwritableID(n.mode, text); unwritable != nil {
		return namedRecord{Record: rec, idErr: unwritable}
	}

	key := sha256.Sum256([]byte(text))
	if first, taken := n.seen[key]; taken {
		return namedRecord{
			Record: rec,
			idErr:  fmt.Errorf("--id: '%s' is also the id of line %d", text, first),
		}
	}

	n.seen[key] = rec.Line

	return namedRecord{Record: rec, id: id}
}

func unwritableID(mode output.Mode, text string) error {
	switch {
	case mode == output.TSV && strings.ContainsAny(text, "\t\r\n"):
		return fmt.Errorf("--id: %q holds a tab, carriage return or newline, which -o tsv cannot write", text)
	case mode == output.CSV && strings.ContainsRune(text, '\r'):
		return fmt.Errorf("--id: %q holds a carriage return, which -o csv cannot read back", text)
	default:
		return nil
	}
}

func idOf(ctx context.Context, namer *jq.Expr, rec input.Record) (any, error) {
	value, err := namer.One(ctx, jqValue(rec.State, rec.Wire))
	if err != nil {
		return nil, fmt.Errorf("--id %w", err)
	}

	id, err := jq.ID(value)
	if err != nil {
		return nil, fmt.Errorf("--id: %w", err)
	}

	return id, nil
}

func idText(id any) string {
	// By text rather than by type, since a csv file holds both as the same cell and a resume has
	// to tell them apart by that cell alone.
	switch typed := id.(type) {
	case json.Number:
		return typed.String()
	case string:
		return typed
	default:
		return ""
	}
}
