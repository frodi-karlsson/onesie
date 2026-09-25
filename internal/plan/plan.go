// Package plan turns a recorded command line into a validated invocation, ready to run.
package plan

import "github.com/frodi-karlsson/onesie/internal/argv"

// The two spellings of a mock answers file, which a refusal names.
const (
	// MockFlag is the flag that names a mock answers file.
	MockFlag = "--mock"
	// MockVariable is the environment variable that names one.
	MockVariable = "ONESIE_MOCK"
)

// Plan is a validated invocation. Questions are in the order they were defined, which is the order
// the normalized output requires.
type Plan struct {
	Questions []Question
	Model     string

	// Orphans are shape or policy flags that had no --ask to bind to. They are kept off Question
	// so nothing iterating Questions has to know to skip a pseudo entry.
	Orphans []argv.Event
}

// Question carries everything the API request body cannot: labels, policy and output position.
type Question struct {
	ID           string
	Shape        Shape
	Instructions any
	Options      []Option
	Levels       []Level
	Policy       Policy

	// Criteria is set only for a Noul question, from --desc yes= and --desc no=.
	Criteria *YesNoCriteria

	// Origin is where the question came from. A body carries no labels and no policy of its own,
	// and a message spells the offending thing the way the origin does.
	Origin Origin

	// Labelled is meaningful only for Rate, since only a score question from a request body can
	// arrive unlabelled. It selects which normalization applies.
	Labelled bool

	// DescOrder is the order --desc keys appeared, so validation can report them as written.
	DescOrder []string

	// UnknownDesc holds --desc keys that matched no option, level, yes or no. Validation reports
	// them against the question's own vocabulary.
	UnknownDesc map[string]any
}

// Shape is the question type, which the shape flags determine.
type Shape int

const (
	// Noul is a yes or no question, answered with a probability.
	Noul Shape = iota
	// Pick is a choice between named options.
	Pick
	// Rate is a score against an ordered rubric.
	Rate
)

// String names the shape as the API's type field spells it.
func (s Shape) String() string {
	switch s {
	case Pick:
		return "pick"
	case Rate:
		return "rate"
	default:
		return "noul"
	}
}

// Origin is where a question came from, which decides how a message spells the thing that is
// wrong. A file says pick where the command line says --pick.
type Origin int

const (
	// OriginPositional is the bare QUESTION argument, keyed under the reserved positional id.
	OriginPositional Origin = iota
	// OriginFlag is a question opened with --ask.
	OriginFlag
	// OriginFile is a question read from a question file.
	OriginFile
	// OriginBody is a question read from a raw API request body.
	OriginBody
)

// Chosen reports whether the user picked the question's id, which is every origin but the
// positional one.
func (o Origin) Chosen() bool {
	return o != OriginPositional
}

// Option is one choice, with the description sent as criteria. A nil Desc sends null, which the
// API permits.
type Option struct {
	Name string
	Desc any
}

// Level is one rung of a score rubric. Label is onesie local and never reaches the API.
type Level struct {
	Label string
	Desc  any
}

// Policy is the per question decision rule. A nil field means the flag was not given.
type Policy struct {
	Threshold     *float64
	MinConfidence *float64
	Fallback      *Fallback
}

// Fallback is the value substituted on low confidence or on request failure.
type Fallback struct {
	// Text is the flag value as written, used for a pick or rate question.
	Text string
	// Boolean is the parsed value for a yes/no question, where decision stays boolean.
	Boolean bool
}

// YesNoCriteria holds what a yes and a no mean, from --desc yes= and --desc no=.
type YesNoCriteria struct {
	Yes any
	No  any
}
