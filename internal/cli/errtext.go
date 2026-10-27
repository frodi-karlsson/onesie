package cli

func alsoFailed(first, second error) error {
	switch {
	case second == nil:
		return first
	case first == nil:
		return second
	}

	return &bothFailed{first: first, second: second}
}

type bothFailed struct {
	first  error
	second error
}

func (e *bothFailed) Error() string {
	// Execute prefixes only the first line, so the second carries its own.
	return text(e.first) + "\nonesie: " + text(e.second)
}

func (e *bothFailed) Unwrap() []error {
	return []error{e.first, e.second}
}

func embed(context string, err error) error {
	return &embeddedError{context: context, cause: err}
}

type embeddedError struct {
	context string
	cause   error
}

func (e *embeddedError) Error() string {
	if e.context == "" {
		return text(e.cause)
	}

	return e.context + ": " + text(e.cause)
}

func (e *embeddedError) Unwrap() error {
	return e.cause
}

func text(err error) string {
	if described, isReasoned := err.(reasoned); isReasoned {
		return described.Reason()
	}

	return err.Error()
}

type reasoned interface {
	Reason() string
}
