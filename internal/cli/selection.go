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
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	envProfile  = "ONESIE_PROFILE"
	envProvider = "ONESIE_PROVIDER"

	flagProfile  = "profile"
	flagProvider = "provider"

	byDefault = "the default"
	byNothing = "nothing"
)

// Where the key of a run lives, as Resolution.KeyFrom names it.
const (
	KeyFromEnv      = "env"
	KeyFromFile     = "file"
	KeyFromKeychain = "keychain"
	KeyFromNone     = "none"
)

func checkChoice(flags *runFlags, lookupEnv func(string) (string, bool)) error {
	chosen, err := Choose(givenOf(flags), lookupEnv, plan.Flags)
	if err != nil || chosen.Provider == "" {
		return err
	}

	_, err = onesie.ProviderNamed(chosen.Provider)

	return err
}

func givenOf(flags *runFlags) Given {
	return Given{
		Profile: flags.profile, Provider: flags.provider, ProfileSet: flags.profileSet, ProviderSet: flags.providerSet,
	}
}

// Choose decides the profile or provider a run uses, from what was given and then from the
// environment. speller names the given settings.
func Choose(given Given, lookupEnv func(string) (string, bool), speller plan.Speller) (Chosen, error) {
	profileSetting, providerSetting := speller.Spell("--profile"), speller.Spell("--provider")

	if given.ProfileSet && given.ProviderSet {
		return Chosen{}, fmt.Errorf("%s and %s both choose what the run uses. Pass one",
			profileSetting, providerSetting)
	}

	switch {
	case given.ProfileSet:
		return Chosen{Profile: strings.TrimSpace(given.Profile), By: profileSetting}, nil
	case given.ProviderSet:
		return Chosen{Provider: strings.TrimSpace(given.Provider), By: providerSetting}, nil
	}

	profile, provider := setVariable(lookupEnv, envProfile), setVariable(lookupEnv, envProvider)

	switch {
	case profile != "" && provider != "":
		return Chosen{}, fmt.Errorf(
			"%s and %s are both set, and each chooses what the run uses. Unset one", envProfile, envProvider)
	case profile != "":
		return Chosen{Profile: profile, By: envProfile}, nil
	case provider != "":
		return Chosen{Provider: provider, By: envProvider}, nil
	}

	return Chosen{}, nil
}

// Given is the profile or provider a caller chose, each with whether it was given at all.
type Given struct {
	Profile, Provider       string
	ProfileSet, ProviderSet bool
}

// Chosen is the profile or provider a run uses, and By what chose it. By is empty when nothing did.
type Chosen struct {
	Profile  string
	Provider string
	By       string
}

// Profile resolves the profile chosen, else the default one, else the default provider when there is
// no profile at all.
func Profile(
	chosen Chosen, profiles creds.Profiles, lookupEnv func(string) (string, bool),
) (Resolution, error) {
	name, by := chosen.Profile, chosen.By
	if name == "" {
		name, by = profiles.Default, byDefault
	}

	if name == "" && len(profiles.Profiles) > 0 {
		return Resolution{}, ErrNoDefaultProfile
	}

	if name == "" {
		return ProviderRun(onesie.TypeSafe().Name(), byNothing, lookupEnv)
	}

	profile, found := profiles.Profiles[name]
	if !found {
		return Resolution{}, &ProfileNotFoundError{By: by, Name: name, Profiles: profiles}
	}

	return ProfileRun(name, profile, by)
}

// ErrNoDefaultProfile refuses a run that chose no profile, when profiles exist and none is the default.
var ErrNoDefaultProfile = errors.New("no profile is the default and nothing chose one")

// Error names the profile and what named it.
func (e *ProfileNotFoundError) Error() string {
	return fmt.Sprintf("%s names profile %s, which does not exist", e.By, creds.QuotedName(e.Name))
}

// ProfileNotFoundError refuses a profile name the profiles file does not hold. Profiles is what it holds.
type ProfileNotFoundError struct {
	By, Name string
	Profiles creds.Profiles
}

// Error is the text of the provider's refusal.
func (e *ProviderError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the provider's refusal.
func (e *ProviderError) Unwrap() error {
	return e.Err
}

// ProviderError refuses the provider a run chose, and carries the choice that named it.
type ProviderError struct {
	Given Chosen
	Err   error
}

func withProfileHints(err error, opened func() (creds.Opened, error)) error {
	var (
		notFound *ProfileNotFoundError
		provider *ProviderError
	)

	switch {
	case errors.Is(err, ErrNoDefaultProfile):
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

func providerIsProfileHint(err error, chosen Chosen, profiles creds.Profiles) error {
	if _, isProfile := profiles.Profiles[chosen.Provider]; !isProfile {
		return err
	}

	note := fmt.Sprintf("%s is a profile, so pass --profile %s in place of --provider",
		creds.QuotedName(chosen.Provider), creds.QuotedName(chosen.Provider))
	if chosen.By == envProvider {
		note = fmt.Sprintf("%s is a profile, so set %s=%s in place of %s",
			creds.QuotedName(chosen.Provider), envProfile, creds.QuotedName(chosen.Provider), envProvider)
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

// ProviderRun resolves a run on a provider alone, keyed by the provider's key variable if it is set.
func ProviderRun(name, by string, lookupEnv func(string) (string, bool)) (Resolution, error) {
	provider, err := onesie.ProviderNamed(name)
	if err != nil {
		return Resolution{}, err
	}

	bare := Resolution{Provider: provider, By: by, KeyFrom: KeyFromNone}
	if setVariable(lookupEnv, provider.EnvAPIKey()) != "" {
		bare.KeyFrom = KeyFromEnv
	}

	return bare, nil
}

// ProfileRun resolves a run on a stored profile.
func ProfileRun(name string, profile creds.Profile, by string) (Resolution, error) {
	provider, err := onesie.ProviderNamed(profile.Provider)
	if err != nil {
		return Resolution{}, err
	}

	kept := Resolution{
		Provider: provider, Profile: name, By: by,
		KeyFrom: KeyFromFile, key: profile.APIKey, KeyEnd: creds.KeyEnd(strings.TrimSpace(profile.APIKey)),
		Model: profile.Model, BaseURL: profile.BaseURL, AccountID: profile.AccountID,
	}

	if profile.Store == creds.StoreKeychain {
		kept.KeyFrom, kept.key, kept.Account, kept.KeyEnd = KeyFromKeychain, "", profile.Account, profile.KeyEnd
	}

	return kept, nil
}

// Resolution is the provider a run uses, the profile it came from, what chose it and where its key
// lives. A file profile's key stays unexported, so no print of the value shows it.
type Resolution struct {
	Provider  onesie.Provider
	Profile   string
	By        string
	KeyFrom   string
	key       string
	Account   string
	KeyEnd    string
	Model     string
	BaseURL   string
	AccountID string
}

// Options are the client options the resolution sets, reading a profile's key through key.
func (r Resolution) Options(
	baseURLFlag string, key func() (string, error), lookupEnv func(string) (string, bool),
) []onesie.Option {
	var opts []onesie.Option

	if _, baseURL := r.GivenBaseURL(baseURLFlag); baseURL != "" {
		opts = append(opts, onesie.WithBaseURL(baseURL))
	}

	if r.Profile == "" {
		return opts
	}

	opts = append(opts, onesie.WithAPIKeyFunc(key), onesie.WithDefaultModel(r.ModelFor("", lookupEnv)))

	if r.AccountID != "" {
		opts = append(opts, onesie.WithAccountID(r.AccountID))
	}

	return opts
}

// GivenBaseURL is the base URL given, from the flag or else the profile, and the name of where it
// came from.
func (r Resolution) GivenBaseURL(baseURLFlag string) (name, given string) {
	if flag := strings.TrimSpace(baseURLFlag); flag != "" {
		return "--" + flagBaseURL, flag
	}

	return profileBaseURL, r.BaseURL
}

// ModelFor is the model a run asks, explicit first, then the profile's, then the provider's.
func (r Resolution) ModelFor(explicit string, lookupEnv func(string) (string, bool)) string {
	if strings.TrimSpace(explicit) == "" {
		explicit = r.Model
	}

	return r.Provider.ResolveModel(explicit, lookupEnv)
}

// GoString is String, so a Go syntax print shows where the key lives and never the key.
func (r Resolution) GoString() string {
	// The %#v verb prints every field, and key holds a file profile's key in plain text.
	return r.String()
}

// String names the profile, the provider and where the key lives.
func (r Resolution) String() string {
	// Here so a stray %v prints where the key lives, never the key.
	return "profile " + cmp.Or(r.Profile, KeyFromNone) + ", provider " + r.Provider.Name() + ", key " + r.KeyLine()
}

// KeyLine says where the key lives, and how a stored key ends.
func (r Resolution) KeyLine() string {
	switch r.KeyFrom {
	case KeyFromKeychain, KeyFromFile:
		if r.KeyEnd == "" {
			return r.KeyFrom
		}

		return r.KeyFrom + ", ends in " + r.KeyEnd
	case KeyFromEnv:
		return KeyFromEnv + " " + r.Provider.EnvAPIKey()
	}

	return KeyFromNone
}

func missingKeyHint(err error, bare Resolution, profiles creds.Profiles) error {
	if bare.Profile != "" || bare.KeyFrom != KeyFromNone || !errors.Is(err, onesie.ErrNoAPIKey) {
		return err
	}

	return &advisedError{cause: err, message: text(err) + storeKeyHint(bare, profiles)}
}

func storeKeyHint(bare Resolution, profiles creds.Profiles) string {
	name := firstProfileOf(bare.Provider.Name(), profiles)
	if name == "" && bare.By == envProvider {
		// A profile created now would still go unused while the variable chooses the run.
		return fmt.Sprintf(". %s is set, so a stored profile is not used. To use one instead, unset %s "+
			"and run onesie profile create NAME --provider %s", envProvider, envProvider, bare.Provider.Name())
	}

	if name == "" {
		return ", or store one with onesie profile create NAME --provider " + bare.Provider.Name()
	}

	if bare.By == envProvider {
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

func accountNote(err error, chosen Resolution) error {
	missing := fmt.Sprintf("no %s account id. Set %s", chosen.Provider.Name(), chosen.Provider.EnvAccountID())
	if chosen.Profile == "" {
		return &advisedError{cause: err, message: fmt.Sprintf(
			"%s, or store it with onesie profile create NAME --provider %s --%s ACCOUNT_ID",
			missing, chosen.Provider.Name(), flagAccountID)}
	}

	return &advisedError{cause: err, message: fmt.Sprintf("%s, or run onesie profile update %s --%s ACCOUNT_ID",
		missing, creds.QuotedName(chosen.Profile), flagAccountID)}
}

// AccountSource names where a provider's account id came from, empty for a provider that needs none.
func (r Resolution) AccountSource(source onesie.BaseURLSource, resolveErr error) string {
	switch {
	case r.Provider.EnvAccountID() == "":
		return ""
	case errors.Is(resolveErr, onesie.ErrNoAccountID):
		return KeyFromNone
	case source != onesie.BaseURLFromAccount:
		return "base_url"
	case r.AccountID != "":
		return "account_id"
	}

	return r.Provider.EnvAccountID()
}

func setVariable(lookupEnv func(string) (string, bool), name string) string {
	value, _ := lookupEnv(name)

	return strings.TrimSpace(value)
}
