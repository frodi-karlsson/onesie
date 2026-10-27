package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

func newHolder(settings rootSettings, flags *runFlags, stderr func() io.Writer) *holder {
	opened := sync.OnceValues(func() (creds.Opened, error) {
		return openProfiles(settings, stderr())
	})

	return &holder{
		stderr: stderr,
		opened: opened,
		resolved: sync.OnceValues(func() (resolution, error) {
			return resolveRun(flags, settings.lookupEnv, opened)
		}),
	}
}

type holder struct {
	stderr   func() io.Writer
	opened   func() (creds.Opened, error)
	resolved func() (resolution, error)
}

func resolveRun(
	flags *runFlags, lookupEnv func(string) (string, bool), open func() (creds.Opened, error),
) (resolution, error) {
	chosen, err := choose(flags, lookupEnv)
	if err != nil {
		return resolution{}, err
	}

	// A run that names its provider and succeeds never opens the file, so CI that names one stays
	// apart from any file on the machine. Only a failure reads it, for the hint.
	if chosen.provider != "" {
		bare, bareErr := providerRun(chosen.provider, chosen.by, lookupEnv)
		if bareErr == nil {
			return bare, nil
		}

		opened, openErr := open()
		if openErr != nil {
			return resolution{}, bareErr
		}

		return resolution{}, providerIsProfileHint(bareErr, chosen, opened.Profiles)
	}

	opened, err := open()
	if err != nil {
		return resolution{}, err
	}

	return resolveProfile(chosen, opened.Profiles, lookupEnv)
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

	for _, line := range conversionLines(opened, settings.lookupEnv) {
		if _, printErr := fmt.Fprintln(stderr, line); printErr != nil {
			return creds.Opened{}, printErr
		}
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

func conversionLines(opened creds.Opened, lookupEnv func(string) (string, bool)) []string {
	var lines []string

	if len(opened.Converted) > 0 {
		where := "into profiles.json"
		if opened.InMemory {
			where = "for this run"
		}

		lines = append(lines, "onesie: converted credentials.json "+where+", with "+
			namedProfiles(opened.Converted)+". "+defaultSentence(opened.Profiles.Default))

		if setVariable(lookupEnv, envProvider) != "" {
			lines = append(lines, "onesie: "+envProvider+" is set, so a run uses no profile and reads only the "+
				"provider's key variable. To use a stored key, unset it and run onesie profile use NAME, "+
				"or pass --profile NAME")
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
