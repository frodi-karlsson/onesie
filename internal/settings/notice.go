package settings

import "strconv"

// Notice says one source of a setting won over another. Values holds the two values, which print only
// for a setting typed twice, and the names a choice of provider or profile prints. Times counts a
// setting typed more than twice.
type Notice struct {
	Setting  Name
	Winner   Origin
	Loser    Origin
	Question string
	Verb     Verb
	Values   [2]string
	Times    int
}

// String words the notice, without the onesie: prefix output.Notice adds.
func (n Notice) String() string {
	var forQuestion string
	if n.Question != "" {
		forQuestion = " for '" + n.Question + "'"
	}

	switch n.Verb {
	case VerbSets:
		return n.winner() + " sets " + n.Loser.String() + forQuestion
	case VerbGivenTwice:
		return n.Winner.Spelled + " is given " + n.times() + forQuestion + ", " + n.Values[0] + " overrides " + n.Values[1]
	case VerbRunsWithout:
		return n.winner() + " runs without the default profile '" + n.Values[1] + "'"
	case VerbPairs:
		return n.winner() + " overrides " + n.Loser.String() + forQuestion + ", since a key goes only with its own endpoint"
	case VerbKeeps:
		return n.winner() + " keeps " + n.Loser.String() + forQuestion
	default:
		return n.winner() + " overrides " + n.Loser.String() + forQuestion
	}
}

func (n Notice) times() string {
	if n.Times > 2 {
		return strconv.Itoa(n.Times) + " times"
	}

	return "twice"
}

func (n Notice) winner() string {
	if !choosesSource(n.Setting) || n.Values[0] == "" {
		return n.Winner.String()
	}

	if n.Winner.Source == SourceEnv {
		return n.Winner.Spelled + "=" + n.Values[0]
	}

	return n.Winner.String() + " " + n.Values[0]
}

// Verb is how a notice's winner stands to its loser.
type Verb int

const (
	// VerbOverrides is a winner that replaced the loser's value.
	VerbOverrides Verb = iota
	// VerbSets is a flag that gave a file question a setting it lacked.
	VerbSets
	// VerbGivenTwice is a setting typed twice, whose last value wins.
	VerbGivenTwice
	// VerbRunsWithout is a provider choice that leaves the default profile out.
	VerbRunsWithout
	// VerbKeeps is a change that keeps a value the loser holds.
	VerbKeeps
	// VerbPairs is an endpoint that sets aside a key from another source, since a key goes only with
	// its own endpoint.
	VerbPairs
)

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
