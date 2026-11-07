package settingrow

const (
	// Noul is a yes or no question, answered with a probability.
	Noul Shape = iota
	// Pick is a choice between named options.
	Pick
	// Rate is a score against an ordered rubric.
	Rate
	// Box finds where something is in an image.
	Box
)

// String names the shape as the API's type field spells it.
func (s Shape) String() string {
	switch s {
	case Pick:
		return "pick"
	case Rate:
		return "rate"
	case Box:
		return "box"
	default:
		return "noul"
	}
}

// Shape is the question type, which the shape flags determine.
type Shape int
