// Package resolve decides the provider and the profile a run uses, where its key lives, and builds the
// client it asks through.
package resolve

import (
	"cmp"
	"errors"
	"fmt"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

const (
	byDefault = "the default"
	byNothing = "nothing"

	flagBaseURL    = "base-url"
	profileBaseURL = "the profile's base_url"
)

// ErrNoDefaultProfile refuses a run that chose no profile, when profiles exist and none is the default.
var ErrNoDefaultProfile = errors.New("no profile is the default and nothing chose one")

// Where the key of a run lives, as Resolution.KeyFrom names it.
const (
	KeyFromEnv      = "env"
	KeyFromFile     = "file"
	KeyFromKeychain = "keychain"
	KeyFromNone     = "none"
)

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
