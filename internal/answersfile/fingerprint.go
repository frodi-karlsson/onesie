package answersfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const fingerprintVersion = 2

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

// Fingerprint sums the questions, the form they are sent in and the inputs that change an answer.
func Fingerprint(questions []plan.Question, wired json.Marshaler, inputs FingerprintInputs) (string, error) {
	encoded, err := json.Marshal(struct {
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
		// A csv or tsv file written before the fallback columns existed has fewer columns, so a
		// resume into it is refused rather than appending longer rows.
		FallbackColumns bool `json:"fallback_columns,omitempty"`
		// A mock run and a real run never resume each other's file.
		Mock bool `json:"mock,omitempty"`
	}{
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
	})
	if err != nil {
		return "", fmt.Errorf("fingerprinting the run: %w", err)
	}

	sum := sha256.Sum256(encoded)

	return fmt.Sprintf("v%d:%s", fingerprintVersion, hex.EncodeToString(sum[:])), nil
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

func staleReason(stored string, found bool, fingerprint, olderColumns string) (StaleReason, bool) {
	if !found {
		return NoFingerprint, true
	}

	version, ok := fingerprintVersionOf(stored)

	switch {
	case !ok:
		return ForeignFingerprint, true
	case version < fingerprintVersion:
		return OlderFingerprint, true
	case version > fingerprintVersion:
		return NewerFingerprint, true
	case olderColumns != "" && stored == olderColumns:
		return OlderColumns, true
	case stored != fingerprint:
		return ChangedRun, true
	}

	return 0, false
}

// StaleAnswersError refuses a file whose fingerprint does not match the run. Sidecar is the file the
// fingerprint is kept in.
type StaleAnswersError struct {
	Path, Sidecar string
	Reason        StaleReason
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
		return "the questions, flags or gate changed since " + e.Path + " was written"
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
		return "the questions, model or flags changed since it was written"
	}
}

// Is matches ErrStaleAnswers.
func (e *StaleAnswersError) Is(target error) bool {
	return target == ErrStaleAnswers
}

func fingerprintVersionOf(stored string) (version int, ok bool) {
	tag, sum, found := strings.Cut(stored, ":")
	digits, tagged := strings.CutPrefix(tag, "v")

	if !found || !tagged || digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}

	version, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}

	if version != fingerprintVersion {
		return version, true
	}

	decoded, err := hex.DecodeString(sum)
	if err != nil || len(decoded) != sha256.Size {
		return 0, false
	}

	return version, true
}
