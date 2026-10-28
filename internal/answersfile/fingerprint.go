package answersfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	fingerprintVersion = 3
	olderVersion       = 2
	partSumLength      = 8
)

// The reasons a StaleAnswersError gives for refusing a file.
const (
	NoFingerprint StaleReason = iota
	ForeignFingerprint
	OlderFingerprint
	NewerFingerprint
	OlderColumns
	ChangedRun
)

// ErrStaleAnswers matches every StaleAnswersError, so a caller tells a stale file apart without
// reading the message.
var ErrStaleAnswers = errors.New("stale answers")

// Fingerprint sums each part of a run that changes an answer, so a refusal can name the parts that
// changed. It also holds the single sum version 2 kept, which a version 2 sidecar still matches.
func Fingerprint(questions []plan.Question, wired json.Marshaler, inputs FingerprintInputs) (Print, error) {
	encoded, err := json.Marshal(printedRun(questions, wired, inputs))
	if err != nil {
		return Print{}, fmt.Errorf("fingerprinting the run: %w", err)
	}

	parts, err := partSums(encoded)
	if err != nil {
		return Print{}, fmt.Errorf("fingerprinting the run: %w", err)
	}

	fingerprint := Print{parts: parts, older: olderSum(encoded)}

	if delimitedOutput(inputs.Output) && hasFallback(questions) {
		without := inputs
		without.withoutFallbackColumns = true

		encoded, err = json.Marshal(printedRun(questions, wired, without))
		if err != nil {
			return Print{}, fmt.Errorf("fingerprinting the run: %w", err)
		}

		fingerprint.olderColumns = olderSum(encoded)
	}

	return fingerprint, nil
}

func printedRun(questions []plan.Question, wired json.Marshaler, inputs FingerprintInputs) runPrint {
	return runPrint{
		Questions: wired,
		Policies:  policiesOf(questions),
		Provider:  inputs.Provider,
		Model:     inputs.Model,
		Map:       inputs.MapSource,
		ID:        inputs.IDSource,
		Output:    inputs.Output,
		Input:     inputs.Input,
		MergeKey:  inputs.MergeKey,
		Assert:    inputs.Assert,
		AbstainIf: inputs.AbstainIf,
		SkipBlank: inputs.SkipBlank,
		Images:    inputs.Images,

		LongContext: inputs.LongContext,

		FallbackColumns: !inputs.withoutFallbackColumns && delimitedOutput(inputs.Output) && hasFallback(questions),
		Mock:            inputs.Mock,
	}
}

type runPrint struct {
	// Each member is encoded as version 2 encoded it, so a version 2 sum still matches.
	Questions   json.Marshaler `json:"questions"`
	Policies    []policyPrint  `json:"policies"`
	Provider    string         `json:"provider"`
	Model       string         `json:"model"`
	Map         string         `json:"map"`
	ID          string         `json:"id"`
	Output      string         `json:"output"`
	Input       string         `json:"input"`
	MergeKey    string         `json:"merge_key"`
	Assert      string         `json:"assert"`
	AbstainIf   string         `json:"abstain_if"`
	SkipBlank   bool           `json:"skip_blank,omitempty"`
	Images      []string       `json:"images,omitempty"`
	LongContext bool           `json:"long_context,omitempty"`
	// A csv or tsv file without the fallback columns has shorter rows than the run writes, so
	// a resume into it is refused.
	FallbackColumns bool `json:"fallback_columns,omitempty"`
	// A mock run and a real run never resume each other's file.
	Mock bool `json:"mock,omitempty"`
}

func partSums(encoded []byte) ([]partSum, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}

	var parts []partSum

	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}

		name, isName := token.(string)
		if !isName {
			return nil, fmt.Errorf("a member name of %v", token)
		}

		var member json.RawMessage
		if err := decoder.Decode(&member); err != nil {
			return nil, err
		}

		sum := sha256.Sum256(member)
		parts = append(parts, partSum{name: name, sum: hex.EncodeToString(sum[:partSumLength])})
	}

	return parts, nil
}

type partSum struct {
	name string
	sum  string
}

func olderSum(encoded []byte) string {
	sum := sha256.Sum256(encoded)

	return fmt.Sprintf("v%d:%s", olderVersion, hex.EncodeToString(sum[:]))
}

// Print is a run's fingerprint. Its String is what the sidecar beside the answers file holds.
type Print struct {
	parts        []partSum
	older        string
	olderColumns string
}

// String writes each part's sum after the version, as v3:assert=SUM,id=SUM, in the order the run's
// parts come in.
func (p Print) String() string {
	written := make([]string, 0, len(p.parts))
	for _, part := range p.parts {
		written = append(written, part.name+"="+part.sum)
	}

	return fmt.Sprintf("v%d:%s", fingerprintVersion, strings.Join(written, ","))
}

// Matches reports whether stored is this run's fingerprint, written by this version or as the
// single sum version 2 kept.
func (p Print) Matches(stored string) bool {
	_, _, stale := staleReason(stored, true, p)

	return !stale
}

func (p Print) changedFrom(stored map[string]string) []string {
	var changed []string

	seen := make(map[string]bool, len(p.parts))

	for _, part := range p.parts {
		seen[part.name] = true

		if stored[part.name] != part.sum {
			changed = append(changed, partLabel(part.name))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(stored)) {
		if !seen[name] {
			changed = append(changed, partLabel(name))
		}
	}

	return changed
}

func partLabel(name string) string {
	switch name {
	case "questions":
		return "the questions"
	case "policies":
		return "the policy flags"
	case "provider":
		return "the provider"
	case "model":
		return "the model"
	case "map":
		return "--map"
	case "id":
		return "--id"
	case "output":
		return "-o"
	case "input":
		return "-i"
	case "merge_key":
		return "the merge key"
	case "assert":
		return "--assert"
	case "abstain_if":
		return "--abstain-if"
	case "skip_blank":
		return "--skip-blank"
	case "images":
		return "the images"
	case "long_context":
		return "--long-context"
	case "fallback_columns":
		return "the fallback columns"
	case "mock":
		return "--mock"
	default:
		return ""
	}
}

func delimitedOutput(name string) bool {
	return name == output.CSV.String() || name == output.TSV.String()
}

func hasFallback(questions []plan.Question) bool {
	return slices.ContainsFunc(questions, func(question plan.Question) bool {
		return question.Policy.Fallback != nil
	})
}

// FingerprintInputs are the choices beside the questions that Fingerprint covers.
type FingerprintInputs struct {
	Provider  string
	Model     string
	MapSource string
	IDSource  string
	Output    string
	Input     string
	MergeKey  string
	Assert    string
	AbstainIf string
	SkipBlank bool
	Images    []string

	LongContext bool
	Mock        bool

	withoutFallbackColumns bool
}

func policiesOf(questions []plan.Question) []policyPrint {
	policies := make([]policyPrint, 0, len(questions))

	for _, question := range questions {
		policy := policyPrint{
			ID:            question.ID,
			Threshold:     question.Policy.Threshold,
			MinConfidence: question.Policy.MinConfidence,
		}

		if question.Policy.Fallback != nil {
			policy.Fallback = &question.Policy.Fallback.Text
		}

		policies = append(policies, policy)
	}

	return policies
}

type policyPrint struct {
	ID            string   `json:"id"`
	Threshold     *float64 `json:"threshold"`
	MinConfidence *float64 `json:"min_confidence"`
	Fallback      *string  `json:"fallback"`
}

func staleReason(stored string, found bool, current Print) (StaleReason, []string, bool) {
	if !found {
		return NoFingerprint, nil, true
	}

	version, sums, ok := parsePrint(stored)

	switch {
	case !ok:
		return ForeignFingerprint, nil, true
	case version < olderVersion:
		return OlderFingerprint, nil, true
	case version > fingerprintVersion:
		return NewerFingerprint, nil, true
	case version == olderVersion && strings.EqualFold(stored, current.older):
		return 0, nil, false
	case version == olderVersion && current.olderColumns != "" && strings.EqualFold(stored, current.olderColumns):
		return OlderColumns, nil, true
	case version == olderVersion:
		return ChangedRun, nil, true
	}

	if changed := current.changedFrom(sums); len(changed) > 0 {
		return ChangedRun, changed, true
	}

	return 0, nil, false
}

// StaleAnswersError refuses a file whose fingerprint does not match the run. Sidecar is the file the
// fingerprint is kept in.
type StaleAnswersError struct {
	Path, Sidecar string
	Reason        StaleReason
	// Changed is empty when the sidecar holds a single sum, which cannot tell the parts apart.
	Changed []string
}

// StaleReason says why a file's fingerprint does not match the run.
type StaleReason int

// Error says why the file does not match, without the sentinel's own text.
func (e *StaleAnswersError) Error() string {
	switch e.Reason {
	case NoFingerprint:
		return e.Path + " has no fingerprint beside it, so onesie cannot tell which run wrote it"
	case ForeignFingerprint:
		return fmt.Sprintf("%s does not hold a fingerprint onesie wrote, so onesie cannot tell which run "+
			"wrote %s", e.Sidecar, e.Path)
	case OlderFingerprint:
		return fmt.Sprintf("an older onesie wrote %s, so this one cannot tell which run wrote %s", e.Sidecar, e.Path)
	case NewerFingerprint:
		return fmt.Sprintf("a newer onesie wrote %s, so this one cannot tell which run wrote %s", e.Sidecar, e.Path)
	case OlderColumns:
		return e.Path + " was written by an older onesie, without the fallback columns this run adds"
	default:
		return e.changed() + " changed since " + e.Path + " was written"
	}
}

// Cause says why the file does not match, in words that follow since.
func (e *StaleAnswersError) Cause() string {
	switch e.Reason {
	case NoFingerprint:
		return "it has no fingerprint beside it"
	case ForeignFingerprint:
		return "onesie did not write its fingerprint"
	case OlderFingerprint:
		return "an older onesie wrote its fingerprint"
	case NewerFingerprint:
		return "a newer onesie wrote its fingerprint"
	case OlderColumns:
		return "an older onesie wrote its columns"
	default:
		return e.changed() + " changed since it was written"
	}
}

func (e *StaleAnswersError) changed() string {
	switch len(e.Changed) {
	case 0:
		return "the questions, model or flags"
	case 1:
		return e.Changed[0]
	}

	return strings.Join(e.Changed[:len(e.Changed)-1], ", ") + " and " + e.Changed[len(e.Changed)-1]
}

// Is matches ErrStaleAnswers.
func (e *StaleAnswersError) Is(target error) bool {
	return target == ErrStaleAnswers
}

func parsePrint(stored string) (version int, sums map[string]string, ok bool) {
	tag, rest, found := strings.Cut(stored, ":")
	digits, tagged := strings.CutPrefix(tag, "v")

	if !found || !tagged || digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, nil, false
	}

	version, err := strconv.Atoi(digits)
	if err != nil {
		return 0, nil, false
	}

	switch version {
	case olderVersion:
		decoded, err := hex.DecodeString(rest)

		return version, nil, err == nil && len(decoded) == sha256.Size
	case fingerprintVersion:
		sums, ok := parseSums(rest)

		return version, sums, ok
	}

	return version, nil, true
}

func parseSums(written string) (map[string]string, bool) {
	sums := map[string]string{}

	for _, part := range strings.Split(written, ",") {
		name, sum, found := strings.Cut(part, "=")
		decoded, err := hex.DecodeString(sum)

		if !found || partLabel(name) == "" || err != nil || len(decoded) != partSumLength {
			return nil, false
		}

		if _, repeated := sums[name]; repeated {
			return nil, false
		}

		sums[name] = strings.ToLower(sum)
	}

	return sums, true
}
