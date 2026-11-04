package runner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// NewNaming reads the records of a run, skips the ones a resume already answered and names each
// by its id. Stop ends the context its ids are computed under.
func NewNaming(ctx context.Context, config RecordsConfig) *Naming {
	stream := input.NewStream(config.Input, config.InputMode, input.StreamOptions{
		SkipBlank: config.SkipBlank, Speller: config.Speller, Delimiter: config.Delimiter,
	})

	// A context of its own, since the read ahead goroutine outlives an engine that stops early, and
	// a runaway --id on it would otherwise keep running until the command's context ends.
	ctx, cancel := context.WithCancel(ctx)

	return &Naming{
		ctx:      ctx,
		cancel:   cancel,
		source:   NewResumed(stream, config.ResumedConfig),
		stream:   stream,
		namer:    config.Namer,
		halt:     config.StopOnAssert,
		book:     config.Book,
		mode:     config.OutputMode,
		input:    config.InputMode,
		speller:  config.Speller,
		appended: config.Appended,
		seen:     map[[sha256.Size]byte]int{},
	}
}

// RecordsConfig is what NewNaming reads its records from and how it names and skips them.
type RecordsConfig struct {
	ResumedConfig

	Input     io.Reader
	InputMode input.Mode
	SkipBlank bool
	Delimiter input.Delimiter
	Namer     *jq.Expr
	Book      *answersfile.Ledger
	// Appended is the csv answers file whose rows a run appends to with the comma it detects in the
	// input. Nil means the run appends to none.
	Appended *AppendedCSV
}

// AppendedCSV is a resumed csv answers file and the comma its rows use.
type AppendedCSV struct {
	Path  string
	Comma rune
}

// ResumedConfig is the resume a source skips and the stop flags a fresh run would have honoured.
type ResumedConfig struct {
	Resume answersfile.Resume
	// OutputMode names the table a stored failure is refused against, for csv and tsv only.
	OutputMode   output.Mode
	StopOnAssert bool
	StopOnError  bool
	Speller      plan.Speller
}

// NewResumed skips the records a resume already answered, and stops where a fresh run under its
// stop flags would have stopped.
func NewResumed(source engine.Source[input.Record], config ResumedConfig) *Resumed {
	return &Resumed{
		source:       source,
		left:         config.Resume.Skip,
		stored:       config.Resume.Stored,
		haltOnAssert: config.StopOnAssert,
		haltOnError:  config.StopOnError,
		table:        tableName(config.OutputMode),
		speller:      config.Speller,
	}
}

func tableName(mode output.Mode) string {
	if mode != output.CSV && mode != output.TSV {
		return ""
	}

	return mode.String()
}

// Next reads the next record past the skipped ones.
func (r *Resumed) Next() (input.Record, bool, error) {
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

func (r *Resumed) count() answersfile.Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()

	var judged answersfile.Verdict
	if at := r.skips.Records; at < len(r.stored) {
		judged = r.stored[at]
	}

	r.skips.Count(judged)

	return judged
}

func (r *Resumed) stop(failure *output.Failure) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.stoppedBy = failure
	r.stoppedAtRecord = r.skips.Records
}

// IDHint is empty, since a source that names nothing has no id to hint at.
func (r *Resumed) IDHint() string {
	return ""
}

// FreshRunAbort is the stored failure a fresh run would have stopped at, or nil.
func (r *Resumed) FreshRunAbort() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stoppedBy == nil {
		return nil
	}

	if r.table != "" {
		return fmt.Errorf("the stored failure for record %d is followed by more rows, so a run "+
			"without %s wrote it, and a %s row keeps only its message, not the code to stop "+
			"with. Pass %s or drop %s to carry on", r.stoppedAtRecord, r.speller.Spell("--stop-on-error"),
			r.table, r.speller.Spell("--id"), r.speller.Spell("--stop-on-error"))
	}

	return &StoredFailure{Failure: *r.stoppedBy, Record: r.stoppedAtRecord, speller: r.speller}
}

// Skipped counts the records the resume skipped.
func (r *Resumed) Skipped() answersfile.Tally {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.skips
}

// Resumed is a record source past the records a resume already answered.
type Resumed struct {
	mu              sync.Mutex
	source          engine.Source[input.Record]
	left            int
	stored          []answersfile.Verdict
	skips           answersfile.Tally
	haltOnAssert    bool
	haltOnError     bool
	halted          bool
	table           string
	speller         plan.Speller
	stoppedBy       *output.Failure
	stoppedAtRecord int
}

// Resumable is a record source a resume skipped through, read once the run ends.
type Resumable interface {
	Skipped() answersfile.Tally
	FreshRunAbort() error
	IDHint() string
}

// Error names the stored failure and the flags that carry a resume past it.
func (e *StoredFailure) Error() string {
	return fmt.Sprintf("%s. The stored failure for record %d is followed by more lines, so a run without "+
		"%s wrote it. Pass %s or drop %s to carry on",
		strings.TrimSuffix(e.Failure.Message, "."), e.Record, e.speller.Spell("--stop-on-error"),
		e.speller.Spell("--id"), e.speller.Spell("--stop-on-error"))
}

// StoredFailure is the stored failure a resume stopped at, where a fresh run would have stopped.
type StoredFailure struct {
	Failure output.Failure
	Record  int
	speller plan.Speller
}

// Next reads the next named record the resume did not answer.
func (n *Naming) Next() (NamedRecord, bool, error) {
	for !n.halted {
		rec, ok, err := n.source.Next()
		if err != nil || !ok {
			if err == nil && n.book != nil {
				n.book.End()
			}

			return NamedRecord{Record: rec}, ok, err
		}

		if err := n.matchComma(); err != nil {
			return NamedRecord{Record: rec}, false, err
		}

		named := n.named(rec)

		// An interrupt can end the naming context before the engine's, so an --id it cut short ends
		// the source and does not become a failed record.
		if err := n.ctx.Err(); err != nil {
			return NamedRecord{Record: rec}, false, err
		}

		if n.book == nil {
			return named, true, nil
		}

		// Skipped here, as the record is read, so an answered record is never evaluated or sent.
		slot, judged, answered := n.book.Admit(named.ID)
		named.Slot = slot

		if !answered {
			return named, true, nil
		}

		// The ledger is left unended, so a stopped run is not compacted, as a fresh one is not.
		n.halted = judged.Rejected && n.halt
	}

	return NamedRecord{}, false, nil
}

func (n *Naming) matchComma() error {
	appended := n.appended
	if appended == nil {
		return nil
	}

	// Checked once, at the first record read, which fixes the detected comma.
	n.appended = nil

	if comma := n.stream.Comma(); comma != appended.Comma {
		return fmt.Errorf("%s auto read '%s' from the input, and %s uses '%s'. Drop %s to start over",
			n.speller.Spell("--delimiter"), escaped(string(comma)), appended.Path, escaped(string(appended.Comma)),
			n.speller.Spell("--resume"))
	}

	return nil
}

// Stop ends the context the ids are computed under.
func (n *Naming) Stop() {
	n.cancel()
}

// NamedRecord is a record with its id, the error naming it gave, and its slot in the answers file.
type NamedRecord struct {
	input.Record

	ID    any
	IDErr error
	Slot  int
}

// Skipped counts the records the resume skipped.
func (n *Naming) Skipped() answersfile.Tally {
	if n.book != nil {
		return n.book.Skipped()
	}

	return n.source.Skipped()
}

// FreshRunAbort is the stored failure a fresh run would have stopped at, or nil.
func (n *Naming) FreshRunAbort() error {
	return n.source.FreshRunAbort()
}

// IDHint is the hint for a run whose every id was null, which a misread delimiter explains.
func (n *Naming) IDHint() string {
	// Atomic, since the goroutine that reads ahead may still be naming a record after the run.
	header := n.misread.Load()
	if header == nil || n.otherID.Load() {
		return ""
	}

	return "every record's " + n.speller.Spell("--id") + " was null. " + DelimiterHint(n.input, n.stream.Comma(), *header, n.speller)
}

// Comma is the separator the input splits a csv row on, final once the first record is read.
func (n *Naming) Comma() rune {
	return n.stream.Comma()
}

// Naming reads records and names each by its id.
type Naming struct {
	ctx      context.Context
	cancel   context.CancelFunc
	source   *Resumed
	stream   *input.Stream
	namer    *jq.Expr
	book     *answersfile.Ledger
	mode     output.Mode
	input    input.Mode
	speller  plan.Speller
	appended *AppendedCSV
	seen     map[[sha256.Size]byte]int
	halt     bool
	halted   bool
	misread  atomic.Pointer[[]string]
	otherID  atomic.Bool
}

// DelimiterHint suggests the setting that splits on the delimiter a one column header holds. comma is
// the separator a csv input split on.
func DelimiterHint(mode input.Mode, comma rune, header []string, speller plan.Speller) string {
	inputFlag := speller.Spell("-i")

	switch {
	case mode == input.CSV && semicolonHeader(header):
		delimiter := speller.Spell("--delimiter")

		return fmt.Sprintf("%s csv splits each line on %s, so if the file is semicolon separated, "+
			"pass %s ';' or %s auto", inputFlag, separatorName(comma), delimiter, delimiter)
	case mode == input.CSV:
		return fmt.Sprintf("%s csv splits each line on %s, so if the file is tab separated, pass %s tsv",
			inputFlag, separatorName(comma), inputFlag)
	case mode == input.TSV:
		return fmt.Sprintf("%s tsv splits each line on tabs, so if the file is comma separated, pass %s csv",
			inputFlag, inputFlag)
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

func (n *Naming) named(rec input.Record) NamedRecord {
	if n.namer == nil || rec.Err != nil {
		return NamedRecord{Record: rec}
	}

	// Named as the records are read rather than as they are evaluated, so which of two records
	// sharing an id counts as the repeat follows input order and not whichever request finished
	// first.
	return n.name(rec)
}

func (n *Naming) name(rec input.Record) NamedRecord {
	id, err := IDOf(n.ctx, n.namer, rec, n.speller)
	if errors.Is(err, jq.ErrNullID) {
		if MisreadDelimiter(n.input, rec.Header) {
			n.misread.Store(&rec.Header)
		}
	} else {
		n.otherID.Store(true)
	}

	if err != nil {
		return NamedRecord{Record: rec, IDErr: err}
	}

	text := jq.IDText(id)

	if unwritable := UnwritableID(n.mode, text, n.speller); unwritable != nil {
		return NamedRecord{Record: rec, IDErr: unwritable}
	}

	key := sha256.Sum256([]byte(text))
	if first, taken := n.seen[key]; taken {
		return NamedRecord{
			Record: rec,
			IDErr:  fmt.Errorf("%s: '%s' is also the id of line %d", n.speller.Spell("--id"), text, first),
		}
	}

	n.seen[key] = rec.Line

	return NamedRecord{Record: rec, ID: id}
}

// MisreadDelimiter reports whether a one column header holds the other mode's delimiter.
func MisreadDelimiter(mode input.Mode, header []string) bool {
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

// UnwritableID refuses an id the output mode cannot write, or cannot read back on a resume.
func UnwritableID(mode output.Mode, text string, speller plan.Speller) error {
	id, out := speller.Spell("--id"), speller.Spell("-o")

	switch {
	case mode == output.TSV && strings.ContainsAny(text, "\t\r\n"):
		return fmt.Errorf("%s: '%s' holds a tab, carriage return or newline, which %s tsv cannot write",
			id, escaped(text), out)
	case mode == output.CSV && strings.ContainsRune(text, '\r'):
		return fmt.Errorf("%s: '%s' holds a carriage return, which %s csv cannot read back", id, escaped(text), out)
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

// IDOf computes a record's id with namer.
func IDOf(ctx context.Context, namer *jq.Expr, rec input.Record, speller plan.Speller) (any, error) {
	value, err := namer.One(ctx, JQValue(rec.State, rec.Wire))
	if err != nil {
		return nil, fmt.Errorf("%s %w", speller.Spell("--id"), err)
	}

	id, err := jq.ID(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", speller.Spell("--id"), err)
	}

	return id, nil
}
