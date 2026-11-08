package output

import "fmt"

// The verdicts a record ends in, in the words onesie writes for people.
const (
	// Answered is a record answered by a run with no gate.
	Answered Verdict = iota
	// Pass is a record whose gate held.
	Pass
	// Fail is a record whose gate said no, from a false assertion or from -q alone.
	Fail
	// Unsure is a record whose assertion was false while its abstain expression held.
	Unsure
	// Error is a record with no usable answer.
	Error
)

// Verdict is how a record ended.
type Verdict uint8

// String names the verdict as onesie writes it.
func (v Verdict) String() string {
	switch v {
	case Answered:
		return "answered"
	case Pass:
		return "pass"
	case Fail:
		return "fail"
	case Unsure:
		return "unsure"
	case Error:
		return "error"
	default:
		return fmt.Sprintf("Verdict(%d)", uint8(v))
	}
}

// Tally counts records by verdict, indexed by the verdict.
type Tally [Error + 1]int

// Verdict is how the record ended: Error when it carries a Failure, its Gate otherwise.
func (r Record) Verdict() Verdict {
	if r.Failure != nil {
		return Error
	}

	return r.Gate
}
