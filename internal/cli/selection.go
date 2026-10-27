package cli

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

const (
	envProfile  = "ONESIE_PROFILE"
	envProvider = "ONESIE_PROVIDER"

	flagProfile  = "profile"
	flagProvider = "provider"

	byDefault = "the default"
	byNothing = "nothing"

	sourceEnv      = "env"
	sourceFile     = "file"
	sourceKeychain = "keychain"
	sourceNone     = "none"
)

func checkChoice(flags *runFlags, lookupEnv func(string) (string, bool)) error {
	chosen, err := choose(flags, lookupEnv)
	if err != nil || chosen.provider == "" {
		return err
	}

	_, err = onesie.ProviderNamed(chosen.provider)

	return err
}

func choose(flags *runFlags, lookupEnv func(string) (string, bool)) (choice, error) {
	if flags.profileSet && flags.providerSet {
		return choice{}, errors.New("--profile and --provider both choose what the run uses. Pass one")
	}

	switch {
	case flags.profileSet:
		return choice{profile: strings.TrimSpace(flags.profile), by: "--" + flagProfile}, nil
	case flags.providerSet:
		return choice{provider: strings.TrimSpace(flags.provider), by: "--" + flagProvider}, nil
	}

	profile, provider := setVariable(lookupEnv, envProfile), setVariable(lookupEnv, envProvider)

	switch {
	case profile != "" && provider != "":
		return choice{}, fmt.Errorf(
			"%s and %s are both set, and each chooses what the run uses. Unset one", envProfile, envProvider)
	case profile != "":
		return choice{profile: profile, by: envProfile}, nil
	case provider != "":
		return choice{provider: provider, by: envProvider}, nil
	}

	return choice{}, nil
}

type choice struct {
	profile  string
	provider string
	by       string
}

func resolveProfile(
	chosen choice, profiles creds.Profiles, lookupEnv func(string) (string, bool),
) (resolution, error) {
	name, by := chosen.profile, chosen.by
	if name == "" {
		name, by = profiles.Default, byDefault
	}

	if name == "" && len(profiles.Profiles) > 0 {
		return resolution{}, errors.New("no profile is the default and nothing chose one. " +
			"Run onesie profile use NAME, or pass --profile NAME or --provider NAME")
	}

	if name == "" {
		return providerRun(onesie.TypeSafe().Name(), byNothing, lookupEnv)
	}

	profile, found := profiles.Profiles[name]
	if !found {
		return resolution{}, fmt.Errorf("%s names profile %s, which does not exist. %s",
			by, creds.QuotedName(name), knownProfiles(profiles))
	}

	return profileRun(name, profile, by)
}

func providerIsProfileHint(err error, chosen choice, profiles creds.Profiles) error {
	if _, isProfile := profiles.Profiles[chosen.provider]; !isProfile {
		return err
	}

	note := fmt.Sprintf("%s is a profile, so pass --profile %s in place of --provider",
		creds.QuotedName(chosen.provider), creds.QuotedName(chosen.provider))
	if chosen.by == envProvider {
		note = fmt.Sprintf("%s is a profile, so set %s=%s in place of %s",
			creds.QuotedName(chosen.provider), envProfile, creds.QuotedName(chosen.provider), envProvider)
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

func providerRun(name, by string, lookupEnv func(string) (string, bool)) (resolution, error) {
	provider, err := onesie.ProviderNamed(name)
	if err != nil {
		return resolution{}, err
	}

	bare := resolution{provider: provider, by: by, keyFrom: sourceNone}
	if setVariable(lookupEnv, provider.EnvAPIKey()) != "" {
		bare.keyFrom = sourceEnv
	}

	return bare, nil
}

func profileRun(name string, profile creds.Profile, by string) (resolution, error) {
	provider, err := onesie.ProviderNamed(profile.Provider)
	if err != nil {
		return resolution{}, err
	}

	kept := resolution{
		provider: provider, profile: name, by: by,
		keyFrom: sourceFile, key: profile.APIKey, keyEnd: creds.KeyEnd(strings.TrimSpace(profile.APIKey)),
		model: profile.Model, baseURL: profile.BaseURL, accountID: profile.AccountID,
	}

	if profile.Store == creds.StoreKeychain {
		kept.keyFrom, kept.key, kept.account, kept.keyEnd = sourceKeychain, "", profile.Account, profile.KeyEnd
	}

	return kept, nil
}

type resolution struct {
	provider  onesie.Provider
	profile   string
	by        string
	keyFrom   string
	key       string
	account   string
	keyEnd    string
	model     string
	baseURL   string
	accountID string
}

func (r resolution) options(
	baseURLFlag string, key func() (string, error), lookupEnv func(string) (string, bool),
) []onesie.Option {
	if r.profile == "" {
		return nil
	}

	opts := []onesie.Option{onesie.WithAPIKeyFunc(key), onesie.WithDefaultModel(r.modelFor("", lookupEnv))}

	// The factory adds --base-url ahead of these, and a later WithBaseURL would replace it.
	if strings.TrimSpace(baseURLFlag) == "" && r.baseURL != "" {
		opts = append(opts, onesie.WithBaseURL(r.baseURL))
	}

	if r.accountID != "" {
		opts = append(opts, onesie.WithAccountID(r.accountID))
	}

	return opts
}

func (r resolution) modelFor(explicit string, lookupEnv func(string) (string, bool)) string {
	if strings.TrimSpace(explicit) == "" {
		explicit = r.model
	}

	return r.provider.ResolveModel(explicit, lookupEnv)
}

func (r resolution) GoString() string {
	// The %#v verb prints every field, and key holds a file profile's key in plain text.
	return r.String()
}

func (r resolution) String() string {
	// Here so a stray %v prints where the key lives, never the key.
	return "profile " + cmp.Or(r.profile, sourceNone) + ", provider " + r.provider.Name() + ", key " + r.keyLine()
}

func (r resolution) keyLine() string {
	switch r.keyFrom {
	case sourceKeychain, sourceFile:
		if r.keyEnd == "" {
			return r.keyFrom
		}

		return r.keyFrom + ", ends in " + r.keyEnd
	case sourceEnv:
		return sourceEnv + " " + r.provider.EnvAPIKey()
	}

	return sourceNone
}

func missingKeyHint(err error, bare resolution, profiles creds.Profiles) error {
	if bare.profile != "" || bare.keyFrom != sourceNone || !errors.Is(err, onesie.ErrNoAPIKey) {
		return err
	}

	return &advisedError{cause: err, message: text(err) + storeKeyHint(bare, profiles)}
}

func storeKeyHint(bare resolution, profiles creds.Profiles) string {
	name := firstProfileOf(bare.provider.Name(), profiles)
	if name == "" && bare.by == envProvider {
		// A profile created now would still go unused while the variable chooses the run.
		return fmt.Sprintf(". %s is set, so a stored profile is not used. To use one instead, unset %s "+
			"and run onesie profile create NAME --provider %s", envProvider, envProvider, bare.provider.Name())
	}

	if name == "" {
		return ", or store one with onesie profile create NAME --provider " + bare.provider.Name()
	}

	if bare.by == envProvider {
		return fmt.Sprintf(", or unset %s and run onesie profile use %s, or run with --profile %s",
			envProvider, creds.QuotedName(name), creds.QuotedName(name))
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

func accountNote(err error, chosen resolution) error {
	if !errors.Is(err, onesie.ErrNoAccountID) {
		return err
	}

	missing := fmt.Sprintf("no %s account id. Set %s", chosen.provider.Name(), chosen.provider.EnvAccountID())
	if chosen.profile == "" {
		return &advisedError{cause: err, message: fmt.Sprintf(
			"%s, or store it with onesie profile create NAME --provider %s --%s ACCOUNT_ID",
			missing, chosen.provider.Name(), flagAccountID)}
	}

	return &advisedError{cause: err, message: fmt.Sprintf("%s, or run onesie profile update %s --%s ACCOUNT_ID",
		missing, creds.QuotedName(chosen.profile), flagAccountID)}
}

func (r resolution) accountSource(lookupEnv func(string) (string, bool)) string {
	switch {
	case r.provider.EnvAccountID() == "":
		return ""
	case r.accountID != "":
		return "account_id"
	case r.baseURL != "":
		return "base_url"
	case setVariable(lookupEnv, r.provider.EnvAccountID()) != "":
		return r.provider.EnvAccountID()
	}

	return sourceNone
}

func setVariable(lookupEnv func(string) (string, bool), name string) string {
	value, _ := lookupEnv(name)

	return strings.TrimSpace(value)
}
