// Package argv records group local flags in the order pflag encounters them, so the question
// groups in a command line can be reconstructed after parsing.
package argv

import "github.com/spf13/pflag"

// New returns an empty recorder.
func New() *Recorder {
	return &Recorder{}
}

// Recorder collects flag occurrences in argv order. pflag calls Set left to right, which is the
// only reason the ordering survives parsing at all.
type Recorder struct {
	events []Event
}

// Flag returns a pflag value that appends to the log under this name.
func (r *Recorder) Flag(name string) pflag.Value {
	return &recorded{recorder: r, name: name}
}

// Events returns the log in argv order. The caller must not modify it.
func (r *Recorder) Events() []Event {
	return r.events
}

// Event is one flag occurrence, with the text exactly as the user wrote it.
type Event struct {
	Name  string
	Value string
}

type recorded struct {
	recorder *Recorder
	name     string
}

func (v *recorded) Set(value string) error {
	v.recorder.events = append(v.recorder.events, Event{Name: v.name, Value: value})

	return nil
}

func (v *recorded) String() string {
	// pflag prints this as the default, and a repeatable flag has no single default.
	return ""
}

func (v *recorded) Type() string {
	return "string"
}
