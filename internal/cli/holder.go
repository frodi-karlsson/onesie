package cli

import (
	"io"
	"path/filepath"
	"sync"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func newHolder(settings rootSettings, flags *runFlags, stderr func() io.Writer) *holder {
	resolver := NewResolver(
		func() (Given, error) { return givenOf(flags), nil },
		func() (creds.Opened, error) { return openProfiles(settings, stderr()) },
		settings.lookupEnv,
		plan.Flags,
	)

	return &holder{
		stderr: stderr,
		opened: resolver.Opened,
		resolved: sync.OnceValues(func() (Resolution, error) {
			chosen, err := resolver.Resolved()

			return chosen, withProfileHints(err, resolver.Opened)
		}),
	}
}

type holder struct {
	stderr   func() io.Writer
	opened   func() (creds.Opened, error)
	resolved func() (Resolution, error)
}

// NewResolver resolves the profile or provider of a run once, from the choice given and then from
// the environment, and opens the profiles file at most once.
func NewResolver(
	given func() (Given, error),
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
	given func() (Given, error),
	open func() (creds.Opened, error),
	lookupEnv func(string) (string, bool),
	speller plan.Speller,
) (Resolution, error) {
	choice, err := given()
	if err != nil {
		return Resolution{}, err
	}

	chosen, err := Choose(choice, lookupEnv, speller)
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

func openProfiles(settings rootSettings, stderr io.Writer) (creds.Opened, error) {
	dir, resolved := profilesDir(settings)
	if !resolved {
		// A config dir that cannot be resolved holds no file, as -f treats it, so a run on a machine
		// with no home still runs on its environment key.
		return creds.Opened{}, nil
	}

	opened, err := settings.credStore.OpenProfiles(dir)
	if err != nil {
		return creds.Opened{}, err
	}

	if printErr := notify(stderr, conversionLines(opened, settings.lookupEnv)...); printErr != nil {
		return creds.Opened{}, printErr
	}

	return opened, nil
}

func profilesDir(settings rootSettings) (string, bool) {
	if settings.profilesPath == nil {
		return "", false
	}

	path, err := settings.profilesPath()

	return filepath.Dir(path), err == nil
}

func conversionLines(opened creds.Opened, lookupEnv func(string) (string, bool)) []notice {
	var lines []notice

	if len(opened.Converted) > 0 {
		where := "into profiles.json"
		if opened.InMemory {
			where = "for this run"
		}

		lines = append(lines, notice{text: "converted credentials.json " + where + ", with " +
			namedProfiles(opened.Converted) + ". " + defaultSentence(opened.Profiles.Default)})

		if setVariable(lookupEnv, envProvider) != "" {
			lines = append(lines, notice{text: envProvider + " is set, so a run uses no profile and reads only " +
				"the provider's key variable. To use a stored key, unset it and run onesie profile use NAME, " +
				"or pass --profile NAME"})
		}
	}

	return append(lines, warningLines(opened.Warnings)...)
}

func namedProfiles(names []string) string {
	if len(names) == 1 {
		return "profile " + creds.QuotedName(names[0])
	}

	return "profiles " + quotedNames(names[:len(names)-1], ", ") + " and " + creds.QuotedName(names[len(names)-1])
}

func defaultSentence(name string) string {
	if name == "" {
		return "No profile is the default, so run onesie profile use NAME to choose one"
	}

	return creds.QuotedName(name) + " is the default"
}
