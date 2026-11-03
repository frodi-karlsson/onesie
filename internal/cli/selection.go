package cli

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/resolve"
)

const (
	flagProfile  = "profile"
	flagProvider = "provider"
)

func checkChoice(flags *runFlags, lookupEnv func(string) (string, bool)) error {
	chosen, err := resolve.Choose(givenOf(flags), lookupEnv, plan.Flags)
	if err != nil || chosen.Provider == "" {
		return err
	}

	_, err = onesie.ProviderNamed(chosen.Provider)

	return err
}

func givenOf(flags *runFlags) resolve.Given {
	return resolve.Given{
		Profile: flags.profile, Provider: flags.provider, ProfileSet: flags.profileSet, ProviderSet: flags.providerSet,
	}
}

func withProfileHints(err error, opened func() (creds.Opened, error)) error {
	var (
		notFound *resolve.ProfileNotFoundError
		provider *resolve.ProviderError
	)

	switch {
	case errors.Is(err, resolve.ErrNoDefaultProfile):
		return fmt.Errorf("%w. Run onesie profile use NAME, or pass --profile NAME or --provider NAME", err)
	case errors.As(err, &notFound):
		return providerNameHint(fmt.Errorf("%w. %s", err, knownProfiles(notFound.Profiles)), notFound.Name, notFound.By)
	case errors.As(err, &provider):
		// Read only when the provider is refused, so a run that names its provider and succeeds
		// never opens the file, and CI that names one stays apart from any file on the machine.
		profiles, openErr := opened()
		if openErr != nil {
			return provider.Err
		}

		return providerIsProfileHint(provider.Err, provider.Given, profiles.Profiles)
	default:
		return err
	}
}

func providerNameHint(err error, name, by string) error {
	isProvider := slices.ContainsFunc(onesie.Providers(), func(provider onesie.Provider) bool {
		return provider.Name() == name
	})
	if !isProvider {
		return err
	}

	var note string

	switch by {
	case "--" + flagProfile:
		note = fmt.Sprintf("so pass --%s %s in place of --%s", flagProvider, name, flagProfile)
	case resolve.EnvProfile:
		note = fmt.Sprintf("so set %s=%s in place of %s", resolve.EnvProvider, name, resolve.EnvProfile)
	case "show", "test":
		note = fmt.Sprintf("so run onesie profile %s --%s %s", by, flagProvider, name)
	case "use":
		note = fmt.Sprintf("which a run chooses with --%s %s, and profile use takes only a profile",
			flagProvider, name)
	default:
		return err
	}

	return &advisedError{
		cause: err, message: fmt.Sprintf("%s. %s is a provider, %s", text(err), creds.QuotedName(name), note),
	}
}

func providerIsProfileHint(err error, chosen resolve.Chosen, profiles creds.Profiles) error {
	if _, isProfile := profiles.Profiles[chosen.Provider]; !isProfile {
		return err
	}

	note := fmt.Sprintf("%s is a profile, so pass --profile %s in place of --provider",
		creds.QuotedName(chosen.Provider), creds.QuotedName(chosen.Provider))
	if chosen.By == resolve.EnvProvider {
		note = fmt.Sprintf("%s is a profile, so set %s=%s in place of %s",
			creds.QuotedName(chosen.Provider), resolve.EnvProfile, creds.QuotedName(chosen.Provider), resolve.EnvProvider)
	}

	return &advisedError{cause: err, message: text(err) + ". " + note}
}

func knownProfiles(profiles creds.Profiles) string {
	if len(profiles.Profiles) == 0 {
		return "There are no profiles. Run onesie profile create NAME --provider PROVIDER to make one"
	}

	return "The profiles are " + quotedNames(slices.Sorted(maps.Keys(profiles.Profiles)), ", ")
}

func quotedNames(names []string, sep string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, creds.QuotedName(name))
	}

	return strings.Join(quoted, sep)
}

func missingKeyHint(err error, bare resolve.Resolution, profiles creds.Profiles) error {
	if bare.Profile != "" || bare.KeyFrom != resolve.KeyFromNone || !errors.Is(err, onesie.ErrNoAPIKey) {
		return err
	}

	return &advisedError{cause: err, message: text(err) + storeKeyHint(bare, profiles)}
}

func storeKeyHint(bare resolve.Resolution, profiles creds.Profiles) string {
	name := firstProfileOf(bare.Provider.Name(), profiles)
	if name == "" && bare.By == resolve.EnvProvider {
		// A profile created now would still go unused while the variable chooses the run.
		return fmt.Sprintf(". %s is set, so a stored profile is not used. To use one instead, unset %s "+
			"and run onesie profile create NAME --provider %s", resolve.EnvProvider, resolve.EnvProvider, bare.Provider.Name())
	}

	if name == "" {
		return ", or store one with onesie profile create NAME --provider " + bare.Provider.Name()
	}

	if bare.By == resolve.EnvProvider {
		return fmt.Sprintf(", or unset %s and run onesie profile use %s, or run with --profile %s",
			resolve.EnvProvider, creds.QuotedName(name), creds.QuotedName(name))
	}

	return ", or drop --provider and pass --profile " + creds.QuotedName(name)
}

func firstProfileOf(provider string, profiles creds.Profiles) string {
	if preferred, ok := profiles.Profiles[profiles.Default]; ok && preferred.Provider == provider {
		return profiles.Default
	}

	for _, name := range slices.Sorted(maps.Keys(profiles.Profiles)) {
		if profiles.Profiles[name].Provider == provider {
			return name
		}
	}

	return ""
}

func accountNote(err error, chosen resolve.Resolution) error {
	missing := fmt.Sprintf("no %s account id. Set %s", chosen.Provider.Name(), chosen.Provider.EnvAccountID())
	if chosen.Profile == "" {
		return &advisedError{cause: err, message: fmt.Sprintf(
			"%s, or store it with onesie profile create NAME --provider %s --%s ACCOUNT_ID",
			missing, chosen.Provider.Name(), flagAccountID)}
	}

	return &advisedError{cause: err, message: fmt.Sprintf("%s, or run onesie profile update %s --%s ACCOUNT_ID",
		missing, creds.QuotedName(chosen.Profile), flagAccountID)}
}

func setVariable(lookupEnv func(string) (string, bool), name string) string {
	value, _ := lookupEnv(name)

	return strings.TrimSpace(value)
}
