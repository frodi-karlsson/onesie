package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	setting "github.com/frodi-karlsson/onesie/internal/settings"

	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func records(
	ctx context.Context,
	settings rootSettings,
	inputMode input.Mode,
	flags runConfig,
	namer *jq.Expr,
	book *answersfile.Ledger,
	resume answersfile.Resume,
	outputMode output.Mode,
) *naming {
	stream := input.NewStream(settings.stdin, inputMode, input.StreamOptions{
		SkipBlank: setting.Get(flags.merged, setting.SkipBlank), Delimiter: flags.parsedDelimiter,
	})

	// A context of its own, since the read ahead goroutine outlives an engine that stops early, and
	// a runaway --id on it would otherwise keep running until the command's context ends.
	ctx, stop := context.WithCancel(ctx)

	return &naming{
		ctx:  ctx,
		stop: stop,
		source: &resumed{
			source: stream, left: resume.Skip, stored: resume.Stored,
			haltOnAssert: setting.Get(flags.merged, setting.StopOnAssert), haltOnError: setting.Get(flags.merged, setting.StopOnError), table: tableName(outputMode, flags),
		},
		stream:   stream,
		namer:    namer,
		halt:     setting.Get(flags.merged, setting.StopOnAssert),
		book:     book,
		mode:     outputMode,
		input:    inputMode,
		appended: appendedTo(flags, resume, outputMode),
		seen:     map[[sha256.Size]byte]int{},
	}
}

func appendedTo(flags runConfig, resume answersfile.Resume, outputMode output.Mode) *appendedCSV {
	// Only a detected comma can differ from the one the file was written with, since a given one
	// also reads the file back.
	if !resume.Header || !flags.parsedDelimiter.Detect || outputMode != output.CSV {
		return nil
	}

	return &appendedCSV{path: setting.Get(flags.merged, setting.Out), comma: resume.Comma}
}

type appendedCSV struct {
	path  string
	comma rune
}

type resumed struct {
	mu              sync.Mutex
	source          engine.Source[input.Record]
	left            int
	stored          []answersfile.Verdict
	skips           answersfile.Tally
	haltOnAssert    bool
	haltOnError     bool
	halted          bool
	table           string
	stoppedBy       *output.Failure
	stoppedAtRecord int
}

func tableName(mode output.Mode, flags runConfig) string {
	if mode != output.CSV && mode != output.TSV {
		return ""
	}

	return setting.Get(flags.merged, setting.Output)
}

func (r *resumed) Next() (input.Record, bool, error) {
	for r.left > 0 && !r.halted {
		r.left--

		if rec, ok, err := r.source.Next(); err != nil || !ok {
			return rec, ok, err
		}

		// A fresh run under --stop-on-assert or --stop-on-error would have stopped at this record,
		// so nothing after it is read.
		judged := r.count()
		stopsOnError := judged.Failure != nil && r.haltOnError
		r.halted = (judged.Rejected && r.haltOnAssert) || stopsOnError

		if stopsOnError {
			r.stop(judged.Failure)
		}
	}

	if r.halted {
		return input.Record{}, false, nil
	}

	return r.source.Next()
}

func (r *resumed) count() answersfile.Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()

	var judged answersfile.Verdict
	if at := r.skips.Records; at < len(r.stored) {
		judged = r.stored[at]
	}

	r.skips.Count(judged)

	return judged
}

func (r *resumed) stop(failure *output.Failure) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.stoppedBy = failure
	r.stoppedAtRecord = r.skips.Records
}

func (r *resumed) idHint() string {
	return ""
}

func (r *resumed) freshRunAbort() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stoppedBy == nil {
		return nil
	}

	if r.table != "" {
		return fmt.Errorf("the stored failure for record %d is followed by more rows, so a run "+
			"without --stop-on-error wrote it, and a %s row keeps only its message, not the code to stop "+
			"with. Pass --id or drop --stop-on-error to carry on", r.stoppedAtRecord, r.table)
	}

	return &storedFailure{failure: *r.stoppedBy, record: r.stoppedAtRecord}
}

func (r *resumed) skipped() answersfile.Tally {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.skips
}

type naming struct {
	ctx      context.Context
	stop     context.CancelFunc
	source   *resumed
	stream   *input.Stream
	namer    *jq.Expr
	book     *answersfile.Ledger
	mode     output.Mode
	input    input.Mode
	appended *appendedCSV
	seen     map[[sha256.Size]byte]int
	halt     bool
	halted   bool
	misread  atomic.Pointer[[]string]
	otherID  atomic.Bool
}

func (n *naming) Next() (namedRecord, bool, error) {
	for !n.halted {
		rec, ok, err := n.source.Next()
		if err != nil || !ok {
			if err == nil && n.book != nil {
				n.book.End()
			}

			return namedRecord{Record: rec}, ok, err
		}

		if err := n.matchComma(); err != nil {
			return namedRecord{Record: rec}, false, err
		}

		named := n.named(rec)

		// An interrupt can end the naming context before the engine's, so an --id it cut short ends
		// the source and does not become a failed record.
		if err := n.ctx.Err(); err != nil {
			return namedRecord{Record: rec}, false, err
		}

		if n.book == nil {
			return named, true, nil
		}

		// Skipped here, as the record is read, so an answered record is never evaluated or sent.
		slot, judged, answered := n.book.Admit(named.id)
		named.slot = slot

		if !answered {
			return named, true, nil
		}

		// The ledger is left unended, so a stopped run is not compacted, as a fresh one is not.
		n.halted = judged.Rejected && n.halt
	}

	return namedRecord{}, false, nil
}

func (n *naming) matchComma() error {
	appended := n.appended
	if appended == nil {
		return nil
	}

	// Checked once, at the first record read, which fixes the detected comma.
	n.appended = nil

	if comma := n.stream.Comma(); comma != appended.comma {
		return fmt.Errorf("--delimiter auto read '%s' from the input, and %s uses '%s'. Drop --resume to start over",
			escaped(string(comma)), appended.path, escaped(string(appended.comma)))
	}

	return nil
}

type namedRecord struct {
	input.Record
	id    any
	idErr error
	slot  int
}

func (n *naming) skipped() answersfile.Tally {
	if n.book != nil {
		return n.book.Skipped()
	}

	return n.source.skipped()
}

func (n *naming) freshRunAbort() error {
	return n.source.freshRunAbort()
}

func (n *naming) idHint() string {
	// Atomic, since the goroutine that reads ahead may still be naming a record after the run.
	header := n.misread.Load()
	if header == nil || n.otherID.Load() {
		return ""
	}

	return "every record's --id was null. " + delimiterHint(n.input, n.stream.Comma(), *header)
}

func (n *naming) comma() rune {
	return n.stream.Comma()
}

func delimiterHint(mode input.Mode, comma rune, header []string) string {
	switch {
	case mode == input.CSV && semicolonHeader(header):
		return fmt.Sprintf("-i csv splits each line on %s, so if the file is semicolon separated, "+
			"pass --delimiter ';' or --delimiter auto", separatorName(comma))
	case mode == input.CSV:
		return fmt.Sprintf("-i csv splits each line on %s, so if the file is tab separated, pass -i tsv",
			separatorName(comma))
	case mode == input.TSV:
		return "-i tsv splits each line on tabs, so if the file is comma separated, pass -i csv"
	default:
		return ""
	}
}

func separatorName(comma rune) string {
	switch comma {
	case 0, ',':
		return "commas"
	case ';':
		return "semicolons"
	case '\t':
		return "tabs"
	default:
		return "'" + escaped(string(comma)) + "'"
	}
}

func semicolonHeader(header []string) bool {
	return len(header) == 1 && strings.Contains(header[0], ";") && !strings.Contains(header[0], "\t")
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
	if errors.Is(err, jq.ErrNullID) {
		if misreadDelimiter(n.input, rec.Header) {
			n.misread.Store(&rec.Header)
		}
	} else {
		n.otherID.Store(true)
	}

	if err != nil {
		return namedRecord{Record: rec, idErr: err}
	}

	text := jq.IDText(id)

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

func misreadDelimiter(mode input.Mode, header []string) bool {
	if len(header) != 1 {
		return false
	}

	switch mode {
	case input.CSV:
		return strings.ContainsAny(header[0], "\t;")
	case input.TSV:
		return strings.Contains(header[0], ",")
	default:
		return false
	}
}

func unwritableID(mode output.Mode, text string) error {
	switch {
	case mode == output.TSV && strings.ContainsAny(text, "\t\r\n"):
		return fmt.Errorf("--id: '%s' holds a tab, carriage return or newline, which -o tsv cannot write", escaped(text))
	case mode == output.CSV && strings.ContainsRune(text, '\r'):
		return fmt.Errorf("--id: '%s' holds a carriage return, which -o csv cannot read back", escaped(text))
	default:
		return nil
	}
}

func escaped(text string) string {
	// The escapes show the tab or the line break the message is about, inside the single quotes every
	// other message uses. Go quotes with double quotes, so its escaped double quote is swapped for an
	// escaped single quote.
	quoted := strconv.Quote(text)

	return strings.NewReplacer(`\"`, `"`, `'`, `\'`).Replace(quoted[1 : len(quoted)-1])
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
