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
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

const (
	envProfile  = "ONESIE_PROFILE"
	envProvider = "ONESIE_PROVIDER"
	envBaseURL  = "ONESIE_BASE_URL"

	flagProfile  = "profile"
	flagProvider = "provider"

	byDefault = "the default"
	byNothing = "nothing"

	sourceEnv      = "env"
	sourceFile     = "file"
	sourceKeychain = "keychain"
	sourceNone     = "none"

	profileAccountID = "the profile's account_id"
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
	chosen, err := setting.Choose(setting.Table(), setting.Sources{
		Given: flags.given, LookupEnv: lookupEnv, Command: setting.CommandRoot,
	})

	switch {
	case err != nil:
		return choice{}, err
	case chosen.Profile != "":
		return choice{profile: chosen.Profile, by: chooser(chosen.ProfileBy, flagProfile, envProfile)}, nil
	case chosen.Provider != "":
		return choice{provider: chosen.Provider, by: chooser(chosen.ProviderBy, flagProvider, envProvider)}, nil
	default:
		return choice{}, nil
	}
}

func chooser(by setting.Source, flag, variable string) string {
	if by == setting.SourceFlag {
		return "--" + flag
	}

	return variable
}

type choice struct {
	profile  string
	provider string
	by       string
}

func profileOf(chosen choice, profiles creds.Profiles) (loadedRun, error) {
	name, by := chosen.profile, chosen.by
	if name == "" {
		name, by = profiles.Default, byDefault
	}

	if name == "" && len(profiles.Profiles) > 0 {
		return loadedRun{}, errors.New("no profile is the default and nothing chose one. " +
			"Run onesie profile use NAME, or pass --profile NAME or --provider NAME")
	}

	if name == "" {
		return loadedRun{chosen: chosen}, nil
	}

	profile, found := profiles.Profiles[name]
	if !found {
		return loadedRun{}, providerNameHint(fmt.Errorf("%s names profile %s, which does not exist. %s",
			by, creds.QuotedName(name), knownProfiles(profiles)), name, by)
	}

	return loadedRun{chosen: chosen, name: name, by: by, profile: profile, fallback: profiles.Default}, nil
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
	case envProfile:
		note = fmt.Sprintf("so set %s=%s in place of %s", envProvider, name, envProfile)
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

	switch {
	case profile.Store == creds.StoreKeychain:
		kept.keyFrom, kept.key, kept.account, kept.keyEnd = sourceKeychain, "", profile.Account, profile.KeyEnd
	case !creds.HoldsKey(profile):
		kept.keyFrom = sourceNone
	}

	return kept, nil
}

type resolution struct {
	provider     onesie.Provider
	profile      string
	by           string
	keyFrom      string
	key          string
	account      string
	keyEnd       string
	model        string
	baseURL      string
	accountID    string
	endpointFrom string
	modelFrom    string
	keyAside     bool
}

func (r resolution) rankedProfile() string {
	switch {
	case r.profile == "":
		return noProfile
	case r.chosenProfile():
		return r.profile + ", a chosen profile"
	default:
		return r.profile + ", the default profile"
	}
}

func (r resolution) chosenProfile() bool {
	return r.profile != "" && r.by != byDefault
}

func (r resolution) merged(m *setting.Merged) resolution {
	endpoint := setting.Get(m, setting.APIEndpoint)
	r.baseURL, r.accountID = endpoint.BaseURL, endpoint.AccountID
	r.endpointFrom = endpointName(m.Origin(setting.APIEndpoint.Name()), endpoint)
	r.model = setting.Get(m, setting.Model)
	r.modelFrom = modelSource(m.Origin(setting.Model.Name()))

	switch m.Value(setting.APIKey.Name()).(type) {
	case setting.HeldKey:
	case string:
		r.keyFrom, r.key, r.account, r.keyEnd = sourceEnv, setting.Get(m, setting.APIKey), "", ""
	default:
		aside, set := m.SetAside(setting.APIKey.Name())
		r.keyAside = set && r.profile != "" &&
			(aside.Source == setting.SourceChosenProfile || aside.Source == setting.SourceDefaultProfile)
		r.keyFrom, r.key, r.account, r.keyEnd = sourceNone, "", "", ""
	}

	return r
}

func modelSource(origin setting.Origin) string {
	switch origin.Source {
	case setting.SourceChosenProfile, setting.SourceDefaultProfile:
		return "profile"
	case setting.SourceEnv:
		return sourceEnv + " " + origin.Spelled
	case setting.SourceDefault:
		return providersOwn
	default:
		return origin.String()
	}
}

func endpointName(origin setting.Origin, endpoint setting.EndpointValue) string {
	switch origin.Source {
	case setting.SourceChosenProfile, setting.SourceDefaultProfile:
		if endpoint.AccountID != "" {
			return profileAccountID
		}

		return profileBaseURL
	case setting.SourceDefault:
		return ""
	default:
		return origin.Spelled
	}
}

func (r resolution) options(key func() (string, error)) []onesie.Option {
	opts := []onesie.Option{onesie.WithDefaultModel(r.model)}

	if r.baseURL != "" {
		opts = append(opts, onesie.WithBaseURL(r.baseURL))
	}

	if r.accountID != "" {
		opts = append(opts, onesie.WithAccountID(r.accountID))
	}

	switch r.keyFrom {
	case sourceFile, sourceKeychain:
		opts = append(opts, onesie.WithAPIKeyFunc(key))
	case sourceEnv:
		opts = append(opts, onesie.WithAPIKey(r.key))
	}

	return opts
}

func (r resolution) modelFor(model string) string {
	return r.provider.ResolveModel(model, noEnvironment)
}

func noEnvironment(string) (string, bool) {
	return "", false
}

func (r resolution) GoString() string {
	// The %#v verb prints every field, and key holds a file profile's key in plain text.
	return r.String()
}

func (r resolution) String() string {
	// Here so a stray %v prints where the key lives, never the key.
	return "profile " + cmp.Or(r.profile, sourceNone) + ", provider " + r.provider.Name() + ", key " + r.keyLine(false)
}

func (r resolution) keyLine(custom bool) string {
	switch r.keyFrom {
	case sourceKeychain, sourceFile:
		if r.keyEnd == "" {
			return r.keyFrom
		}

		return r.keyFrom + ", ends in " + r.keyEnd
	case sourceEnv:
		return sourceEnv + " " + r.provider.EnvAPIKey()
	}

	if custom {
		return sourceNone + ", not needed for a custom base URL"
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
	missing := fmt.Sprintf("no %s account id. Set %s", chosen.provider.Name(), chosen.provider.EnvAccountID())
	if chosen.profile == "" {
		return &advisedError{cause: err, message: fmt.Sprintf(
			"%s, or store it with onesie profile create NAME --provider %s --%s ACCOUNT_ID",
			missing, chosen.provider.Name(), flagAccountID)}
	}

	return &advisedError{cause: err, message: fmt.Sprintf("%s, or run onesie profile update %s --%s ACCOUNT_ID",
		missing, creds.QuotedName(chosen.profile), flagAccountID)}
}

func (r resolution) accountSource(source onesie.BaseURLSource, resolveErr error) string {
	switch {
	case r.provider.EnvAccountID() == "":
		return ""
	case errors.Is(resolveErr, onesie.ErrNoAccountID):
		return sourceNone
	case source != onesie.BaseURLFromAccount:
		return "base_url"
	case r.endpointFrom == profileAccountID:
		return "account_id"
	}

	return r.provider.EnvAccountID()
}

func setVariable(lookupEnv func(string) (string, bool), name string) string {
	value, _ := lookupEnv(name)

	return strings.TrimSpace(value)
}
