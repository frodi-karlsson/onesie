package cli

import (
	"context"
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
) engine.Source[namedRecord] {
	stream := input.NewStream(settings.stdin, inputMode, flags.skipBlank)

	return &naming{
		ctx:    ctx,
		source: engine.Skip[input.Record](stream, flags.resumeSkip),
		namer:  namer,
		seen:   map[string]int{},
	}
}

type namedRecord struct {
	input.Record
	id    any
	idErr error
}

type naming struct {
	ctx    context.Context
	source engine.Source[input.Record]
	namer  *jq.Expr
	seen   map[string]int
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

	key := idText(id)
	if first, taken := n.seen[key]; taken {
		return namedRecord{
			Record: rec,
			idErr:  fmt.Errorf("--id: '%s' is also the id of line %d", key, first),
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
