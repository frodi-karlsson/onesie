package settings

// Origin is where a value came from, as a notice names it. Spelled is the flag, the variable, the
// file key or the profiles.json member, and Body marks a file that is a request body.
type Origin struct {
	Source  Source
	Spelled string
	Body    bool
}

// String names the origin the way a notice or an error reads it, such as the profile's model.
func (o Origin) String() string {
	switch o.Source {
	case SourceFile:
		if o.Body {
			return "the request body's " + o.Spelled
		}

		return "the question file's " + o.Spelled
	case SourceChosenProfile:
		return "the profile's " + o.Spelled
	case SourceDefaultProfile:
		return "the default profile's " + o.Spelled
	case SourceDefault:
		return "the default"
	default:
		return o.Spelled
	}
}
