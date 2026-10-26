package creds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
)

const (
	profilesKind = "profiles file"
	minKeyEnd    = 16
	keyEndLength = 4
)

var (
	namePattern      = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]{0,63}$`)
	accountIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

// LoadProfiles reads the profiles file, refusing one another user can reach or one that breaks a
// rule, and found is false when it does not exist.
func (s Store) LoadProfiles(path string) (profiles Profiles, found bool, err error) {
	data, found, err := s.read(path, profilesKind)
	if err != nil || !found {
		return Profiles{}, false, err
	}

	profiles, err = decodeProfiles(data)
	if err != nil {
		return Profiles{}, false, &InvalidProfilesError{
			Path: path, Kind: profilesKind, Reason: "is not a JSON object of profiles" + decodeDetail(err),
		}
	}

	if ruleErr := profiles.Validate(); ruleErr != nil {
		return Profiles{}, false, &InvalidProfilesError{Path: path, Kind: profilesKind, Reason: ruleErr.Error()}
	}

	return profiles, true, nil
}

func decodeDetail(err error) string {
	// A syntax error can quote a character of the file, which may be part of a key. A type error
	// names the field and not the value, so only it and an unknown member are passed on.
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) || strings.HasPrefix(err.Error(), "json: unknown field") {
		return ": " + err.Error()
	}

	return ""
}

func decodeProfiles(data []byte) (Profiles, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return Profiles{}, errors.New("not a JSON object")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	// A misspelled member is refused, not silently dropped.
	decoder.DisallowUnknownFields()

	var profiles Profiles
	if err := decoder.Decode(&profiles); err != nil {
		return Profiles{}, err
	}

	if err := decoder.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return Profiles{}, errors.New("more than one JSON value")
	}

	if profiles.Profiles == nil {
		profiles.Profiles = map[string]Profile{}
	}

	return profiles, nil
}

// SaveProfiles validates the profiles and writes them atomically, as Save writes the credential
// file. A non nil warning means the file was written but its mode could not be set.
func (s Store) SaveProfiles(path string, profiles Profiles) (*ModeWarning, error) {
	if err := profiles.Validate(); err != nil {
		return nil, &InvalidProfilesError{Path: path, Kind: profilesKind, Reason: err.Error()}
	}

	if profiles.Profiles == nil {
		profiles.Profiles = map[string]Profile{}
	}

	// As in Save, json.Marshal cannot fail on plain strings, and its error would embed the value.
	data, err := json.Marshal(profiles)
	if err != nil {
		return nil, fmt.Errorf("onesie: encoding the profiles file: %w", err)
	}

	return s.write(path, data, ".profiles-*")
}

// Validate reports the first rule the profiles break, checking profiles in name order.
func (p Profiles) Validate() error {
	for _, name := range slices.Sorted(maps.Keys(p.Profiles)) {
		if err := ValidName(name); err != nil {
			return err
		}

		if err := p.Profiles[name].validate(name); err != nil {
			return err
		}
	}

	if _, ok := p.Profiles[p.Default]; p.Default != "" && !ok {
		return fmt.Errorf("default names profile '%s', which does not exist", p.Default)
	}

	return nil
}

func (p Profile) validate(name string) error {
	if p.Provider == "" {
		return fmt.Errorf("profile '%s' names no provider", name)
	}

	provider, err := onesie.ProviderNamed(p.Provider)
	if err != nil || provider.Name != p.Provider {
		return fmt.Errorf(
			"profile '%s' names provider '%s', which is not typesafe, openrouter, berget, levanto or cloudflare",
			name, p.Provider)
	}

	inFile := strings.TrimSpace(p.APIKey) != "" && p.Store == "" && p.Account == ""
	inKeychain := p.APIKey == "" && p.Store == StoreKeychain && p.Account != ""

	if !inFile && !inKeychain {
		return fmt.Errorf(
			"profile '%s' needs its key in exactly one place, a non empty api_key or store keychain with an account",
			name)
	}

	if len(p.KeyEnd) > keyEndLength {
		return fmt.Errorf("profile '%s' has a key_end longer than %d characters", name, keyEndLength)
	}

	if p.AccountID == "" {
		return nil
	}

	if provider.EnvAccountID == "" {
		return fmt.Errorf("profile '%s' has account_id, which only cloudflare takes", name)
	}

	if !accountIDPattern.MatchString(p.AccountID) {
		return fmt.Errorf("profile '%s' has an account_id that is not 32 hexadecimal characters", name)
	}

	if p.BaseURL != "" {
		return fmt.Errorf("profile '%s' has both base_url and account_id. Keep one", name)
	}

	return nil
}

// ValidName refuses a profile name that is not 1 to 64 lowercase ASCII letters, digits, dashes or
// underscores, one that starts with a dash, and none.
func ValidName(name string) error {
	if namePattern.MatchString(name) && name != "none" {
		return nil
	}

	return fmt.Errorf(
		"'%s' is not a profile name. A name is 1 to 64 lowercase letters, digits, - or _, does not start with -, and is not none",
		name)
}

// KeyEnd is the last four characters of a key of 16 characters or more, so two keys can be told
// apart without showing either. A shorter key gets none.
func KeyEnd(key string) string {
	if len(key) < minKeyEnd {
		return ""
	}

	return key[len(key)-keyEndLength:]
}

// Profiles is the contents of a profiles file. Default names the profile a run uses when nothing
// else chooses one.
type Profiles struct {
	Default  string             `json:"default,omitempty"`
	Profiles map[string]Profile `json:"profiles"`
}

// Profile is one named connection. Its key is APIKey, or the keychain item Account when Store is
// StoreKeychain.
type Profile struct {
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key,omitempty"`
	Store     string `json:"store,omitempty"`
	Account   string `json:"account,omitempty"`
	KeyEnd    string `json:"key_end,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	Model     string `json:"model,omitempty"`
}

// InvalidProfilesError means a profiles file, or a credential file being converted, does not parse
// or breaks a rule. Kind names the file. It names the rule, never a key.
type InvalidProfilesError struct {
	Path   string
	Kind   string
	Reason string
}

// Error names the file and the rule.
func (e *InvalidProfilesError) Error() string {
	return "onesie: " + e.Kind + " " + e.Path + " " + e.Reason
}
