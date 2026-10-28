package cli

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
