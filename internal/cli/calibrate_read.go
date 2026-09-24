package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func calibrateRequests(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, inputMode input.Mode, inv *invocation,
	labels []questionLabel,
) error {
	model, err := resolveModel(settings, flags, inv.plan.Model)
	if err != nil {
		return err
	}

	set, err := readLabelled(cmd.Context(), settings, inputMode, flags, inv.mapper, inv.namer, labels)
	if err != nil {
		return err
	}

	questions := wireAll(inv.plan.Questions)

	for _, rec := range set.records {
		if ctxErr := cmd.Context().Err(); ctxErr != nil {
			return ctxErr
		}

		body, bodyErr := requestLine(rec.line, jev.Request{State: rec.sent, Model: model, Questions: questions})
		if bodyErr != nil {
			return bodyErr
		}

		if _, printErr := fmt.Fprintln(cmd.OutOrStdout(), string(body)); printErr != nil {
			return written(printErr)
		}
	}

	return nil
}

func readLabelled(
	ctx context.Context, settings rootSettings, inputMode input.Mode, flags *runFlags,
	mapper, namer *jq.Expr, labels []questionLabel,
) (labelledSet, error) {
	// Ended on every return, so the goroutine reading stdin stops waiting to hand over a record.
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	next := readAhead(ctx, input.NewStream(settings.stdin, inputMode, flags.skipBlank))
	reader := &labelReader{mapper: mapper, namer: namer, labels: labels, seen: map[[sha256.Size]byte]int{}}

	var set labelledSet

	for {
		var pulled pulledRecord

		select {
		case pulled = <-next:
		case <-ctx.Done():
			return labelledSet{}, ctx.Err()
		}

		rec, ok, err := pulled.rec, pulled.ok, pulled.err
		if err != nil {
			return labelledSet{}, err
		}

		// An interrupt can land as the input ends, and the set read so far is not the whole input.
		if !ok {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return labelledSet{}, ctxErr
			}

			return set, nil
		}

		set.total++
		if set.total > limits.MaxCalibrateRecords {
			return labelledSet{}, fmt.Errorf(
				"onesie: calibrate reads at most %d records, and -V lists the cap", limits.MaxCalibrateRecords)
		}

		labelled, found, err := reader.read(ctx, rec)
		if err != nil {
			// An interrupt reaches the expressions as their own failure, and it has to exit 130
			// rather than as the bad record it would otherwise read as.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return labelledSet{}, ctxErr
			}

			return labelledSet{}, err
		}

		if !found {
			set.unlabelled++

			continue
		}

		labelled.index = len(set.records)
		set.records = append(set.records, labelled)
	}
}

func readAhead(ctx context.Context, stream *input.Stream) <-chan pulledRecord {
	next := make(chan pulledRecord)

	// Read on its own goroutine so an interrupt ends a read with no deadline, such as a terminal. A
	// read under way cannot be interrupted, so this goroutine outlives the read until it returns.
	go func() {
		for {
			rec, ok, err := stream.Next()

			select {
			case next <- pulledRecord{rec: rec, ok: ok, err: err}:
			case <-ctx.Done():
				return
			}

			if !ok || err != nil {
				return
			}
		}
	}()

	return next
}

func (r *labelReader) read(ctx context.Context, rec input.Record) (labelledRecord, bool, error) {
	if rec.Err != nil {
		return labelledRecord{}, false, rec.Err
	}

	labelled := labelledRecord{line: rec.Line}

	if r.namer != nil {
		id, err := r.uniqueID(ctx, rec)
		if err != nil {
			return labelledRecord{}, false, &input.LineError{Line: rec.Line, Err: err}
		}

		labelled.id = id
	}

	value := jqValue(rec.State, rec.Wire)
	labelled.labels = make([]*calibrate.Label, len(r.labels))
	found := false

	for i, label := range r.labels {
		parsed, err := labelOf(ctx, label, value)
		if err != nil {
			return labelledRecord{}, false, fmt.Errorf("onesie: --label %s: %s: %w", label.id, labelled.name(), err)
		}

		labelled.labels[i] = parsed
		found = found || parsed != nil
	}

	sent, err := mapped(ctx, r.mapper, rec.State, rec.Wire)
	if err != nil {
		return labelledRecord{}, false, &input.LineError{Line: rec.Line, Err: err}
	}

	raw, isRaw := sent.(json.RawMessage)
	if !isRaw {
		return labelledRecord{}, false, errors.New("onesie: calibrate needs --map")
	}

	// Cloned, so the record holds its own bytes and not the slack of the buffer they were encoded in.
	labelled.sent = bytes.Clone(raw)

	return labelled, found, nil
}

func (r *labelReader) uniqueID(ctx context.Context, rec input.Record) (any, error) {
	id, err := idOf(ctx, r.namer, rec)
	if err != nil {
		return nil, err
	}

	text := idText(id)

	if unwritable := unwritableID(output.JSON, text); unwritable != nil {
		return nil, unwritable
	}

	key := sha256.Sum256([]byte(text))
	if first, taken := r.seen[key]; taken {
		return nil, fmt.Errorf("--id: '%s' is also the id of line %d", text, first)
	}

	r.seen[key] = rec.Line

	return id, nil
}

func labelOf(ctx context.Context, label questionLabel, value any) (*calibrate.Label, error) {
	result, err := label.expr.One(ctx, value)
	if errors.Is(err, jq.ErrNoValue) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	parsed, labelled, err := calibrate.ParseLabel(label.shape, label.names, result)
	if err != nil || !labelled {
		return nil, err
	}

	return &parsed, nil
}

type pulledRecord struct {
	rec input.Record
	ok  bool
	err error
}

type labelReader struct {
	mapper *jq.Expr
	namer  *jq.Expr
	labels []questionLabel
	seen   map[[sha256.Size]byte]int
}

type labelledSet struct {
	records    []labelledRecord
	unlabelled int
	total      int
}

type labelledRecord struct {
	index  int
	line   int
	id     any
	sent   json.RawMessage
	labels []*calibrate.Label
}

func (r labelledRecord) name() string {
	if r.id == nil {
		return fmt.Sprintf("line %d", r.line)
	}

	return "record " + idText(r.id)
}
