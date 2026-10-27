package creds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
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

	var misfit *misfitError

	switch {
	case errors.As(err, &misfit):
		return Profiles{}, false, invalidFile(path, profilesKind, "is invalid", misfit.Error())
	case err != nil:
		return Profiles{}, false, &InvalidProfilesError{
			Path: path, Kind: profilesKind, Reason: "is not a JSON object of profiles",
		}
	}

	if ruleErr := profiles.Validate(); ruleErr != nil {
		return Profiles{}, false, invalidFile(path, profilesKind, "is invalid", ruleErr.Error())
	}

	return profiles, true, nil
}

func invalidFile(path, kind, verdict, rule string) *InvalidProfilesError {
	return &InvalidProfilesError{Path: path, Kind: kind, Reason: verdict + ". " + upperFirst(rule)}
}

func upperFirst(sentence string) string {
	if sentence == "" {
		return sentence
	}

	return strings.ToUpper(sentence[:1]) + sentence[1:]
}

func decodeProfiles(data []byte) (Profiles, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return Profiles{}, errors.New("not a JSON object")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))

	var first json.RawMessage
	if err := decoder.Decode(&first); err != nil {
		return Profiles{}, err
	}

	var top profilesFile
	if err := decodeMembers(first, &top, "the file"); err != nil {
		return Profiles{}, err
	}

	if err := decoder.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return Profiles{}, errors.New("more than one JSON value")
	}

	profiles := Profiles{Default: top.Default, Profiles: make(map[string]Profile, len(top.Profiles))}

	// One profile at a time, so a misfit names the profile it is in.
	for _, name := range slices.Sorted(maps.Keys(top.Profiles)) {
		var profile Profile
		if err := decodeMembers(top.Profiles[name], &profile, "profile "+QuotedName(name)); err != nil {
			return Profiles{}, err
		}

		profiles.Profiles[name] = profile
	}

	return profiles, nil
}

type profilesFile struct {
	Default  string                     `json:"default,omitempty"`
	Profiles map[string]json.RawMessage `json:"profiles"`
}

func decodeMembers(data []byte, into any, holder string) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return misfit(err, holder)
	}

	for _, name := range slices.Sorted(maps.Keys(members)) {
		if !knownMember(into, name) {
			return &misfitError{fmt.Sprintf("%s has an unknown member %s", holder, QuotedName(name))}
		}
	}

	if err := json.Unmarshal(data, into); err != nil {
		return misfit(err, holder)
	}

	return nil
}

func knownMember(into any, name string) bool {
	fields := reflect.TypeOf(into).Elem()

	for i := range fields.NumField() {
		member, _, _ := strings.Cut(fields.Field(i).Tag.Get("json"), ",")

		// encoding/json matches a member to a field whatever its case, so a member it would read is
		// a known one.
		if strings.EqualFold(member, name) {
			return true
		}
	}

	return false
}

func misfit(err error, holder string) error {
	// A syntax error can quote a character of the file, which may be part of a key, so only a
	// member of the wrong type, which names the member and not its value, is passed on.
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		return err
	}

	if typeErr.Field == "" {
		return &misfitError{fmt.Sprintf("%s is %s, not %s", holder, jsonKind(typeErr.Value), goKind(typeErr.Type))}
	}

	return &misfitError{fmt.Sprintf("%s has a %s that is %s, not %s",
		holder, typeErr.Field, jsonKind(typeErr.Value), goKind(typeErr.Type))}
}

type misfitError struct {
	rule string
}

func (e *misfitError) Error() string {
	return e.rule
}

func jsonKind(value string) string {
	switch value {
	case "array", "object":
		return "an " + value
	}

	return "a " + value
}

func goKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Map, reflect.Struct:
		return "an object"
	}

	return "a " + t.String()
}

// SaveProfiles validates the profiles and writes them atomically through a renamed temporary file,
// replacing a symlink at path. A non nil warning means the file was written but its mode could not be set.
func (s Store) SaveProfiles(path string, profiles Profiles) (*ModeWarning, error) {
	if err := profiles.Validate(); err != nil {
		return nil, invalidFile(path, profilesKind, "would be invalid", err.Error())
	}

	if profiles.Profiles == nil {
		profiles.Profiles = map[string]Profile{}
	}

	// json.Marshal cannot fail on plain strings. A future field of another type would make it
	// reachable, and its error embeds the offending value, so such a field must keep the key out.
	data, err := json.Marshal(profiles)
	if err != nil {
		return nil, fmt.Errorf("encoding the profiles file: %w", err)
	}

	return s.write(path, data, ".profiles-*")
}

// Validate reports the first rule the profiles break, checking profiles in name order.
func (p Profiles) Validate() error {
	return p.validateAs(func(name string) string { return "profile " + QuotedName(name) })
}

func (p Profiles) validateAs(subject func(name string) string) error {
	for _, name := range slices.Sorted(maps.Keys(p.Profiles)) {
		if err := ValidName(name); err != nil {
			return err
		}

		if err := p.Profiles[name].validate(subject(name)); err != nil {
			return err
		}
	}

	if _, ok := p.Profiles[p.Default]; p.Default != "" && !ok {
		return fmt.Errorf("default names profile %s, which does not exist", QuotedName(p.Default))
	}

	return nil
}

func (p Profile) validate(subject string) error {
	if p.Provider == "" {
		return fmt.Errorf("%s names no provider", subject)
	}

	provider, err := onesie.ProviderNamed(p.Provider)
	if err != nil || provider.Name() != p.Provider {
		return fmt.Errorf(
			"%s names provider %s, which is not typesafe, openrouter, berget, levanto or cloudflare",
			subject, QuotedName(p.Provider))
	}

	inFile := strings.TrimSpace(p.APIKey) != "" && p.Store == "" && p.Account == ""
	inKeychain := p.APIKey == "" && p.Store == StoreKeychain && p.Account != ""

	if !inFile && !inKeychain {
		return fmt.Errorf(
			"%s needs its key in exactly one place, a non empty api_key or store keychain with an account",
			subject)
	}

	if len(p.KeyEnd) > keyEndLength {
		return fmt.Errorf("%s has a key_end longer than %d characters", subject, keyEndLength)
	}

	if p.AccountID == "" {
		return nil
	}

	if provider.EnvAccountID() == "" {
		return fmt.Errorf("%s has account_id, which only cloudflare takes", subject)
	}

	if !ValidAccountID(p.AccountID) {
		return fmt.Errorf("%s has an account_id that is not 32 hexadecimal characters", subject)
	}

	if p.BaseURL != "" {
		return fmt.Errorf("%s has both base_url and account_id. Keep one", subject)
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
		"%s is not a profile name. A name is 1 to 64 lowercase letters, digits, - or _, does not start with -, and is not none",
		QuotedName(name))
}

// ValidAccountID reports whether id is a Cloudflare account id, 32 hexadecimal characters.
func ValidAccountID(id string) bool {
	return accountIDPattern.MatchString(id)
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
	return e.Kind + " " + e.Path + " " + e.Reason
}

// QuotedName quotes a name in a message, such as a profile, a provider or a member of profiles.json.
func QuotedName(name string) string {
	return "'" + name + "'"
}
