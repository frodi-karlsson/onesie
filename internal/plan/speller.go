package plan

import "strings"

// Speller names a setting in a message the way the reader of that message writes it. The zero value
// is Flags.
type Speller int

const (
	// Flags spells a setting as its command line flag, such as --min-confidence.
	Flags Speller = iota
	// Keys spells a setting as its quoted question file key, such as 'min_confidence'.
	Keys
	// Arguments spells a setting as a tool argument, such as min_confidence.
	Arguments
)

// Shape spells a question's shape setting the way its origin wrote it. A body has only its one
// criteria key, so a message about its options or levels names that.
func (s Speller) Shape(q *Question, flag string) string {
	if q.Origin == OriginBody {
		return "'criteria'"
	}

	return s.For(q.Origin).Spell(flag)
}

// For is the speller for a question's own settings. A file question's settings are always its keys,
// whoever reads the message.
func (s Speller) For(origin Origin) Speller {
	if origin == OriginFile {
		return Keys
	}

	return s
}

// Spell names a setting, given as its command line flag.
func (s Speller) Spell(flag string) string {
	switch s {
	case Keys:
		return "'" + argumentName(flag) + "'"
	case Arguments:
		return argumentName(flag)
	default:
		return flag
	}
}

func argumentName(flag string) string {
	switch flag {
	case "-o":
		return "output"
	case "-i":
		return "input"
	default:
		return strings.ReplaceAll(strings.TrimLeft(flag, "-"), "-", "_")
	}
}
