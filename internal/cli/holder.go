package cli

import (
	"io"
	"path/filepath"
	"sync"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func newHolder(settings rootSettings, flags *runFlags, stderr func() io.Writer) *holder {
	opened := sync.OnceValues(func() (creds.Opened, error) {
		return openProfiles(settings, stderr())
	})

	chosen := sync.OnceValues(func() (setting.Choice, error) {
		return setting.Choose(settingrow.Table(), setting.Sources{
			Given: flags.given, LookupEnv: settings.lookupEnv, Command: settingrow.CommandRoot,
		})
	})

	loaded := sync.OnceValues(func() (loadedRun, error) {
		choice, err := chosen()
		if err != nil {
			return loadedRun{}, err
		}
		return loadRun(choice, opened, func() string { return defaultName(settings) })
	})

	return &holder{
		stderr: stderr,
		chosen: chosen,
		opened: opened,
		loaded: loaded,
		resolved: sync.OnceValues(func() (resolution, error) {
			run, err := loaded()
			if err != nil {
				return resolution{}, err
			}

			return resolveRun(run, settings.lookupEnv, opened)
		}),
	}
}

type holder struct {
	chosen   func() (setting.Choice, error)
	stderr   func() io.Writer
	opened   func() (creds.Opened, error)
	loaded   func() (loadedRun, error)
	resolved func() (resolution, error)
}

func loadRun(
	chosen setting.Choice, open func() (creds.Opened, error),
	fallback func() string,
) (loadedRun, error) {
	// A run that names its provider never opens the file, so CI that names one stays apart from any
	// file on the machine. It only peeks at the default it runs without, for the notice.
	if chosen.Provider != "" {
		return loadedRun{chosen: chosen, fallback: fallback()}, nil
	}

	opened, err := open()
	if err != nil {
		return loadedRun{}, err
	}

	return profileOf(chosen, opened.Profiles)
}

func resolveRun(
	run loadedRun, lookupEnv func(string) (string, bool), open func() (creds.Opened, error),
) (resolution, error) {
	if run.chosen.Provider == "" {
		if run.name == "" {
			return providerRun(onesie.TypeSafe().Name(), byNothing, lookupEnv)
		}

		return profileRun(run.name, run.profile, run.by)
	}

	bare, err := providerRun(run.chosen.Provider, choiceBy(run.chosen.ProviderBy, setting.Provider.Name()), lookupEnv)
	if err == nil {
		return bare, nil
	}

	// Only a failure reads the file, for the hint.
	opened, openErr := open()
	if openErr != nil {
		return resolution{}, err
	}

	return resolution{}, providerIsProfileHint(err, run.chosen, opened.Profiles)
}

type loadedRun struct {
	chosen   setting.Choice
	name     string
	by       string
	profile  creds.Profile
	fallback string
}

func (r loadedRun) values() *setting.ProfileValues {
	if r.name == "" {
		return nil
	}

	return &setting.ProfileValues{
		Members: map[string]string{
			"provider": r.profile.Provider, "model": r.profile.Model,
			"base_url": r.profile.BaseURL, "account_id": r.profile.AccountID,
		},
		KeyHeld: creds.HoldsKey(r.profile),
		Key: setting.HeldKey{
			Key: r.profile.APIKey, Keychain: r.profile.Store == creds.StoreKeychain,
			Account: r.profile.Account, KeyEnd: r.profile.KeyEnd,
		},
	}
}

func defaultName(settings rootSettings) string {
	if settings.profilesPath == nil {
		return ""
	}

	path, err := settings.profilesPath()
	if err != nil {
		return ""
	}

	profiles, found, err := settings.credStore.LoadProfiles(path)
	if err != nil || !found {
		return ""
	}

	return profiles.Default
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

		if setVariable(lookupEnv, settingEnv(setting.Provider.Name())) != "" {
			lines = append(lines, notice{text: settingEnv(setting.Provider.Name()) + " is set, so a run uses no profile and reads only " +
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
