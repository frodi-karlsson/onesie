package settings

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/input"
)

const (
	fileName     settingrow.Name = "file"
	profileName  settingrow.Name = "profile"
	providerName settingrow.Name = "provider"
	apiKeyName   settingrow.Name = "api_key"
	endpointName settingrow.Name = "endpoint"
	pickName     settingrow.Name = "pick"
	rateName     settingrow.Name = "rate"
	descName     settingrow.Name = "desc"
)

// Read reads every source of rows into one layer each, the highest first. A file, variable or profile
// value whose row has no effect in the run's mode is left out, and a typed flag is kept.
func Read(rows []settingrow.Row, src Sources) ([]Layer, error) {
	flags, err := readFlags(rows, src.Given, src.Command, src.Parsed)
	if err != nil {
		return nil, err
	}

	defaults := readDefaults(rows, src.Command)
	mode := runMode(src.Command, flags, defaults)

	choice, err := sourceChoice(rows, src, flags, mode)
	if err != nil {
		return nil, err
	}

	profile, err := readProfile(rows, src.Profile, choice, mode)
	if err != nil {
		return nil, err
	}

	file, err := readFile(rows, src.File, mode)
	if err != nil {
		return nil, err
	}

	env, err := readEnv(rows, src.LookupEnv, runProvider(choice, src.Profile), mode, src.Command,
		keyless(choice, src.Profile))
	if err != nil {
		return nil, err
	}

	var layers []Layer

	for _, layer := range []Layer{flags, profile, file, env, defaults} {
		if len(layer.Values) > 0 {
			layers = append(layers, layer)
		}
	}

	return highestFirst(layers), nil
}

// Choose resolves the rows that pick a source rather than set a value: the question file, the
// profile and the provider. The caller loads what it names before calling Read.
func Choose(rows []settingrow.Row, src Sources) (Choice, error) {
	flags, err := readFlags(rows, src.Given, src.Command, src.Parsed)
	if err != nil {
		return Choice{}, err
	}

	return choose(rows, src, flags, runMode(src.Command, flags, readDefaults(rows, src.Command)))
}

// Sources holds the inputs to Read. Choice reuses a resolved selection, and Parsed reuses ParseRun.
// DefaultProfile is the default member of profiles.json, empty when it cannot be read.
type Sources struct {
	Choice         *Choice
	Given          *Given
	Parsed         *Merged
	File           *FileValues
	LookupEnv      func(string) (string, bool)
	Profile        *ProfileValues
	DefaultProfile string
	Command        settingrow.Command
}

// FileValues is what a question file or a request body gives, keyed as the file spells each key.
// TopLevel holds only the keys the file holds.
type FileValues struct {
	Body     bool
	TopLevel map[string]any
}

// ProfileValues is the members of the profile Choose named, keyed as profiles.json spells them.
// KeyHeld says the profile holds a key, with its credential metadata in Key.
type ProfileValues struct {
	Members map[string]string
	KeyHeld bool
	Key     HeldKey
}

// HeldKey carries the credential metadata of a profile without opening the keychain.
type HeldKey struct {
	Key      string
	Keychain bool
	Account  string
	KeyEnd   string
}

// Layer holds each value and its spelling from one source. Body marks a request body.
type Layer struct {
	Source  Source
	Values  map[settingrow.Name]any
	Spelled map[settingrow.Name]string
	Body    bool
}

// Choice is the question file, profile and provider a run uses, and the source that chose each.
// DefaultProfile is the profile a run falls back to, kept under a provider choice for its notice.
type Choice struct {
	File, Profile, Provider string
	ProfileBy, ProviderBy   Source
	DefaultProfile          string
}

func readFlags(rows []settingrow.Row, given *Given, command settingrow.Command, parsed *Merged) (Layer, error) {
	layer := Layer{Source: SourceFlag}
	if given == nil {
		return layer, nil
	}

	for _, row := range rows {
		bound, registered := boundOn(row, command)
		if !registered || !readPerRun(row) || offTheChoice(row, command) {
			continue
		}

		flag := given.byName(bound.Long)
		if flag == nil || !flag.Changed {
			continue
		}

		var value any
		var err error
		if parsed != nil && parsed.Source(row.Name) == SourceFlag {
			value = parsed.Value(row.Name)
		} else {
			value, err = flagValue(row, bound, flag, command)
			if err != nil {
				return Layer{}, err
			}
		}

		if text, isText := value.(string); isText && choosesSource(row.Name) {
			value = strings.TrimSpace(text)
		}

		layer.set(row.Name, value, "--"+bound.Long)
	}

	return layer, nil
}

func flagValue(row settingrow.Row, bound settingrow.BoundFlag, flag *pflag.Flag, command settingrow.Command) (any, error) {
	if parsedPerRun(row, command) {
		return parseFlag(row, bound, flag)
	}

	// A command that gives a flag a kind of its own keeps the flag as typed, and its rules word the
	// refusal.
	return flag.Value.String(), nil
}

func boundOn(row settingrow.Row, command settingrow.Command) (settingrow.BoundFlag, bool) {
	if bound, registered := row.FlagOn(command); registered || command == settingrow.CommandRoot {
		return bound, registered
	}

	bound, registered := row.FlagOn(settingrow.CommandRoot)

	return bound, registered && bound.Persistent
}

func offTheChoice(row settingrow.Row, command settingrow.Command) bool {
	return choosesSource(row.Name) && profileCommand(command)
}

func profileCommand(command settingrow.Command) bool {
	return command == settingrow.CommandProfileCreate || command == settingrow.CommandProfileUpdate
}

func choosesSource(name settingrow.Name) bool {
	return name == profileName || name == providerName
}

func (l *Layer) set(name settingrow.Name, value any, spelled string) {
	if l.Values == nil {
		l.Values = map[settingrow.Name]any{}
	}

	l.Values[name] = value

	if spelled == "" {
		return
	}

	if l.Spelled == nil {
		l.Spelled = map[settingrow.Name]string{}
	}

	l.Spelled[name] = spelled
}

func readDefaults(rows []settingrow.Row, command settingrow.Command) Layer {
	layer := Layer{Source: SourceDefault}

	for _, row := range rows {
		if row.Scope != settingrow.ScopeRun || offTheChoice(row, command) {
			continue
		}

		if bound, registered := boundOn(row, command); registered {
			if readPerRun(row) {
				layer.set(row.Name, untyped(row, bound), "")
			}

			continue
		}

		if row.Default != nil {
			layer.set(row.Name, row.Default, "")
		}
	}

	return layer
}

func readPerRun(row settingrow.Row) bool {
	return row.Scope == settingrow.ScopeRun && !row.Flag.Group
}

func runMode(command settingrow.Command, flags, defaults Layer) settingrow.Mode {
	switch command {
	case settingrow.CommandCalibrate:
		return settingrow.ModeCalibrate
	case settingrow.CommandProfileCreate, settingrow.CommandProfileUpdate:
		return settingrow.ModeProfile
	case settingrow.CommandRoot:
	}

	given := func(name settingrow.Name) any {
		if value, found := flags.Values[name]; found {
			return value
		}

		return defaults.Values[name]
	}

	for _, selector := range []struct {
		key  Key[bool]
		mode settingrow.Mode
	}{
		{PrintSchema, settingrow.ModePrintSchema},
		{ListModels, settingrow.ModeListModels},
		{PrintRequest, settingrow.ModePrintRequest},
		{PrintQuestions, settingrow.ModePrintQuestions},
	} {
		if on, isSwitch := given(selector.key.Name()).(bool); isSwitch && on {
			return selector.mode
		}
	}

	name, isText := given(Input.Name()).(string)
	if !isText {
		return settingrow.ModeAsk
	}

	switch mode, err := input.ParseMode(name); {
	case err != nil:
		return settingrow.ModeAsk
	case mode == input.Request:
		return settingrow.ModeRequest
	case mode.Streaming():
		return settingrow.ModeStream
	default:
		return settingrow.ModeAsk
	}
}

func highestFirst(layers []Layer) []Layer {
	return slices.SortedStableFunc(slices.Values(layers), func(a, b Layer) int {
		return cmp.Compare(b.Source, a.Source)
	})
}

func sourceChoice(rows []settingrow.Row, src Sources, flags Layer, mode settingrow.Mode) (Choice, error) {
	if src.Choice != nil {
		return *src.Choice, nil
	}
	return choose(rows, src, flags, mode)
}

func choose(rows []settingrow.Row, src Sources, flags Layer, mode settingrow.Mode) (Choice, error) {
	chosen := Choice{File: textIn(flags, fileName), DefaultProfile: src.DefaultProfile}
	if profileCommand(src.Command) {
		return chosen, nil
	}

	profileFlag, providerFlag := textIn(flags, profileName), textIn(flags, providerName)
	profileEnv, profileVariable := ambientText(rows, profileName, src.LookupEnv, mode)
	providerEnv, providerVariable := ambientText(rows, providerName, src.LookupEnv, mode)

	switch {
	case profileFlag != "" && providerFlag != "":
		return Choice{}, errors.New("--profile and --provider both choose what the run uses. Pass one")
	case profileFlag != "":
		chosen.Profile, chosen.ProfileBy = profileFlag, SourceFlag
	case providerFlag != "" && profileEnv != "":
		return Choice{}, fmt.Errorf("--provider and %s both choose what the run uses. Drop --provider or unset %s",
			profileVariable, profileVariable)
	case providerFlag != "":
		chosen.Provider, chosen.ProviderBy = providerFlag, SourceFlag
	case profileEnv != "" && providerEnv != "":
		return Choice{}, fmt.Errorf("%s and %s are both set, and each chooses what the run uses. Unset one",
			profileVariable, providerVariable)
	case profileEnv != "":
		chosen.Profile, chosen.ProfileBy = profileEnv, SourceEnv
	case providerEnv != "":
		chosen.Provider, chosen.ProviderBy = providerEnv, SourceEnv
	case src.DefaultProfile != "":
		chosen.Profile, chosen.ProfileBy = src.DefaultProfile, SourceDefaultProfile
	}

	return chosen, nil
}

func textIn(layer Layer, name settingrow.Name) string {
	if text, isText := layer.Values[name].(string); isText {
		return text
	}

	return ""
}

func ambientText(rows []settingrow.Row, name settingrow.Name, lookupEnv func(string) (string, bool), mode settingrow.Mode) (value, variable string) {
	for _, row := range rows {
		if row.Name != name || len(row.Env) == 0 {
			continue
		}

		variable = row.Env[0].Name
		if lookupEnv != nil && row.Modes.Has(mode) {
			value = lookedUp(lookupEnv, variable)
		}
	}

	return value, variable
}

func lookedUp(lookupEnv func(string) (string, bool), name string) string {
	value, _ := lookupEnv(name)

	return strings.TrimSpace(value)
}

func readProfile(rows []settingrow.Row, profile *ProfileValues, chosen Choice, mode settingrow.Mode) (Layer, error) {
	if chosen.Provider != "" {
		layer := Layer{Source: SourceDefaultProfile}
		if chosen.DefaultProfile != "" {
			layer.set(profileName, chosen.DefaultProfile, "")
		}

		return layer, nil
	}

	layer := Layer{Source: SourceDefaultProfile}
	if chosen.ProfileBy == SourceFlag || chosen.ProfileBy == SourceEnv {
		layer.Source = SourceChosenProfile
	}

	if layer.Source == SourceDefaultProfile && chosen.Profile != "" {
		layer.set(profileName, chosen.Profile, "")
	}

	if profile == nil || chosen.Profile == "" {
		return layer, nil
	}

	for _, row := range rows {
		if !row.Modes.Has(mode) {
			continue
		}

		if row.Name == apiKeyName {
			if profile.KeyHeld {
				layer.set(apiKeyName, profile.Key, string(apiKeyName))
			}

			continue
		}

		for _, member := range row.Profile {
			raw := strings.TrimSpace(profile.Members[member.Key])
			if raw == "" {
				continue
			}

			value, err := parseHalf(row, raw, member.Half)
			if err != nil {
				return Layer{}, fmt.Errorf("%s %w", Origin{Source: layer.Source, Spelled: member.Key}, err)
			}

			layer.set(row.Name, value, member.Key)

			break
		}
	}

	return layer, nil
}

func parseHalf(row settingrow.Row, raw string, half settingrow.EndpointHalf) (any, error) {
	if endpoint, isEndpoint := row.Kind.(settingrow.EndpointKind); isEndpoint {
		return endpoint.ParseHalf(raw, half)
	}

	value, err := row.Kind.Parse(raw)
	if err == nil && row.Check != nil {
		err = row.Check(value)
	}

	return value, err
}

func runProvider(chosen Choice, profile *ProfileValues) string {
	switch {
	case chosen.Provider != "":
		return chosen.Provider
	case chosen.Profile != "" && profile != nil && strings.TrimSpace(profile.Members["provider"]) != "":
		return strings.TrimSpace(profile.Members["provider"])
	default:
		return onesie.TypeSafe().Name()
	}
}

func readFile(rows []settingrow.Row, file *FileValues, mode settingrow.Mode) (Layer, error) {
	layer := Layer{Source: SourceFile}
	if file == nil {
		return layer, nil
	}

	layer.Body = file.Body

	for _, row := range rows {
		if row.Scope != settingrow.ScopeRun || row.FileKey == "" || row.InBody != file.Body || !row.Modes.Has(mode) {
			continue
		}

		raw, found := file.TopLevel[row.FileKey]
		if !found {
			continue
		}

		value, err := row.Kind.FromFile(raw)
		if err != nil {
			return Layer{}, fmt.Errorf("%s %w", Origin{Source: SourceFile, Spelled: row.FileKey, Body: file.Body}, err)
		}

		// qfile keeps a body's long_context as a bool, so its false cannot be told from no key and
		// reads as unset.
		if on, isSwitch := value.(bool); isSwitch && !on && file.Body {
			continue
		}

		if text, isText := value.(string); isText && row.Flag.Repeat {
			value = []string{text}
		}

		layer.set(row.Name, value, row.FileKey)
	}

	return layer, nil
}

func keyless(chosen Choice, profile *ProfileValues) bool {
	return chosen.Provider == "" && chosen.Profile != "" && profile != nil && !profile.KeyHeld
}

func readEnv(
	rows []settingrow.Row, lookupEnv func(string) (string, bool), provider string, mode settingrow.Mode, command settingrow.Command, keyless bool,
) (Layer, error) {
	layer := Layer{Source: SourceEnv}
	if lookupEnv == nil {
		return layer, nil
	}

	for _, row := range rows {
		// A profile that holds no key runs keyless, as on a server of its own, so the provider's key
		// variable never reaches its base URL.
		if !row.Modes.Has(mode) || offTheChoice(row, command) || (keyless && row.Name == apiKeyName) {
			continue
		}

		for _, variable := range row.Env {
			if variable.Provider != "" && variable.Provider != provider {
				continue
			}

			raw := lookedUp(lookupEnv, variable.Name)
			if raw == "" {
				continue
			}

			if !utf8.ValidString(raw) {
				return Layer{}, &EnvironmentError{Name: variable.Name, Cause: fmt.Errorf("%s %w", variable.Name, settingrow.ErrNotUTF8)}
			}

			value, err := parseHalf(row, raw, variable.Half)
			if err != nil {
				return Layer{}, envError(row, variable, raw, err)
			}

			layer.set(row.Name, value, variable.Name)

			break
		}
	}

	return layer, nil
}

func envError(row settingrow.Row, variable settingrow.EnvSpec, raw string, err error) error {
	return &EnvironmentError{Name: variable.Name, Cause: envErrorText(row, variable, raw, err)}
}

// EnvironmentError identifies the refused variable without exposing its value.
type EnvironmentError struct {
	Name  string
	Cause error
}

// Error preserves the ordinary environment diagnostic.
func (e *EnvironmentError) Error() string { return e.Cause.Error() }

// Unwrap returns the underlying environment diagnostic.
func (e *EnvironmentError) Unwrap() error { return e.Cause }

func envErrorText(row settingrow.Row, variable settingrow.EnvSpec, raw string, err error) error {
	switch row.Kind.Zero().(type) {
	case bool:
		return fmt.Errorf("%s=%s is not a value --%s takes. Use 1 or 0", variable.Name, raw, row.Flag.Long)
	case time.Duration:
		return fmt.Errorf("%s=%s is not a duration of zero or more, such as 90m or 72h", variable.Name, raw)
	case settingrow.EndpointValue:
		if variable.Half == settingrow.HalfAccount {
			return fmt.Errorf("%s must be 32 hexadecimal characters", variable.Name)
		}

		return fmt.Errorf("%s %w", variable.Name, err)
	default:
		return fmt.Errorf("%s %w", variable.Name, err)
	}
}
