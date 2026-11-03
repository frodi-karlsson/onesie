package resolve

import (
	"sync"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// NewResolver resolves the profile or provider of a run once, from the choice given and then from
// the environment, and opens the profiles file at most once.
func NewResolver(
	given func() Given,
	open func() (creds.Opened, error),
	lookupEnv func(string) (string, bool),
	speller plan.Speller,
) *Resolver {
	opened := sync.OnceValues(open)

	return &Resolver{
		opened: opened,
		resolved: sync.OnceValues(func() (Resolution, error) {
			return resolveRun(given, opened, lookupEnv, speller)
		}),
	}
}

// Opened is the profiles file, opened on first use.
func (r *Resolver) Opened() (creds.Opened, error) {
	return r.opened()
}

// Resolved is the resolution of the run, decided on first use. A refused provider comes back as a
// ProviderError, and the profiles file stays unopened.
func (r *Resolver) Resolved() (Resolution, error) {
	return r.resolved()
}

// Resolver decides the profile or provider of a run and where its key lives.
type Resolver struct {
	opened   func() (creds.Opened, error)
	resolved func() (Resolution, error)
}

func resolveRun(
	given func() Given,
	open func() (creds.Opened, error),
	lookupEnv func(string) (string, bool),
	speller plan.Speller,
) (Resolution, error) {
	chosen, err := Choose(given(), lookupEnv, speller)
	if err != nil {
		return Resolution{}, err
	}

	// A run that names its provider never opens the file, so CI that names one stays apart from any
	// file on the machine.
	if chosen.Provider != "" {
		bare, bareErr := ProviderRun(chosen.Provider, chosen.By, lookupEnv)
		if bareErr != nil {
			return Resolution{}, &ProviderError{Given: chosen, Err: bareErr}
		}

		return bare, nil
	}

	opened, err := open()
	if err != nil {
		return Resolution{}, err
	}

	return Profile(chosen, opened.Profiles, lookupEnv)
}
