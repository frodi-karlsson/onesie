package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
)

func records(
	ctx context.Context,
	settings rootSettings,
	inputMode input.Mode,
	flags *runFlags,
	namer *jq.Expr,
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
		seen:   map[[sha256.Size]byte]int{},
	}
}

type namedRecord struct {
	input.Record
	id    any
	idErr error
}

type naming struct {
	ctx    context.Context
	stop   context.CancelFunc
	source engine.Source[input.Record]
	namer  *jq.Expr
	seen   map[[sha256.Size]byte]int
}

func (n *naming) Next() (namedRecord, bool, error) {
	rec, ok, err := n.source.Next()
	if err != nil || !ok || n.namer == nil || rec.Err != nil {
		return namedRecord{Record: rec}, ok, err
	}

	// Named as the records are read rather than as they are evaluated, so which of two records
	// sharing an id counts as the repeat follows input order and not whichever request finished
	// first.
	return n.name(rec), true, nil
}

func (n *naming) name(rec input.Record) namedRecord {
	id, err := idOf(n.ctx, n.namer, rec)
	if err != nil {
		return namedRecord{Record: rec, idErr: err}
	}

	text := idText(id)

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
