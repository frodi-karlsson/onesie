package settings

import (
	"errors"
	"fmt"
	"strings"

	"github.com/frodi-karlsson/onesie-go"

	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	fileName     Name = "file"
	profileName  Name = "profile"
	providerName Name = "provider"
)

// Read reads every source of rows into one layer each, the highest first. A file, variable or profile
// value whose row has no effect in the run's mode is left out, and a typed flag is kept.
func Read(rows []Row, src Sources) ([]Layer, error) {
	flags, err := readFlags(rows, src.Given, src.Facts.Command)
	if err != nil {
		return nil, err
	}

	defaults := readDefaults(rows, src.Facts.Command)
	mode := runMode(src.Facts.Command, flags, defaults)

	choice, err := choose(rows, src, flags, mode)
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

	env, err := readEnv(rows, src.LookupEnv, runProvider(choice, src.Profile), mode)
	if err != nil {
		return nil, err
	}

	var layers []Layer

	for _, layer := range []Layer{flags, profile, file, env, defaults} {
		if len(layer.Values) > 0 || len(layer.Questions) > 0 {
			layers = append(layers, layer)
		}
	}

	return layers, nil
}

// Choose resolves the rows that pick a source rather than set a value: the question file, the
// profile and the provider. The caller loads what it names before calling Read.
func Choose(rows []Row, src Sources) (Choice, error) {
	flags, err := readFlags(rows, src.Given, src.Facts.Command)
	if err != nil {
		return Choice{}, err
	}

	return choose(rows, src, flags, runMode(src.Facts.Command, flags, readDefaults(rows, src.Facts.Command)))
}

// Sources is everything Read reads, each filled by the caller. DefaultProfile is the default member
// of profiles.json, read without converting anything, and empty when it cannot be read.
type Sources struct {
	Given          *Given
	File           *FileValues
	LookupEnv      func(string) (string, bool)
	Profile        *ProfileValues
	DefaultProfile string
	Facts          Facts
}

// FileValues is what a question file or a request body gives, keyed as the file spells each key.
// TopLevel holds only the keys the file holds.
type FileValues struct {
	Body      bool
	TopLevel  map[string]any
	Questions []QuestionValues
}

// QuestionValues is what one question of a file gives, keyed as the file spells each key.
type QuestionValues struct {
	ID     string
	Shape  plan.Shape
	Values map[string]any
}

// ProfileValues is the members of the profile Choose named, keyed as profiles.json spells them.
type ProfileValues struct {
	Members map[string]string
}

// Layer is what one source gives: a value per row, how the source spells each row, and per question
// values keyed by question id. Body marks a file layer read from a request body.
type Layer struct {
	Source    Source
	Values    map[Name]any
	Spelled   map[Name]string
	Questions map[string]map[Name]any
	Body      bool
}

// Choice is the question file, profile and provider a run uses, and the source that chose each.
// DefaultProfile is the profile a run falls back to, kept under a provider choice for its notice.
type Choice struct {
	File, Profile, Provider string
	ProfileBy, ProviderBy   Source
	DefaultProfile          string
}

func readFlags(rows []Row, given *Given, command Command) (Layer, error) {
	layer := Layer{Source: SourceFlag}
	if given == nil {
		return layer, nil
	}

	for _, row := range rows {
		bound, registered := row.FlagOn(command)
		if !registered || !parsedPerRun(row, command) {
			continue
		}

		flag := given.byName(bound.Long)
		if flag == nil || !flag.Changed {
			continue
		}

		value, err := parseFlag(row, bound, flag)
		if err != nil {
			return Layer{}, err
		}

		if text, isText := value.(string); isText && choosesSource(row.Name) {
			value = strings.TrimSpace(text)
		}

		layer.set(row.Name, value, "--"+bound.Long)
	}

	return layer, nil
}

func choosesSource(name Name) bool {
	return name == profileName || name == providerName
}

func (l *Layer) set(name Name, value any, spelled string) {
	if l.Values == nil {
		l.Values = map[Name]any{}
	}

	l.Values[name] = value

	if spelled == "" {
		return
	}

	if l.Spelled == nil {
		l.Spelled = map[Name]string{}
	}

	l.Spelled[name] = spelled
}

func readDefaults(rows []Row, command Command) Layer {
	layer := Layer{Source: SourceDefault}

	for _, row := range rows {
		if row.Scope != ScopeRun {
			continue
		}

		if bound, registered := row.FlagOn(command); registered {
			if parsedPerRun(row, command) {
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

func runMode(command Command, flags, defaults Layer) Mode {
	switch command {
	case CommandCalibrate:
		return ModeCalibrate
	case CommandProfileCreate, CommandProfileUpdate:
		return ModeProfile
	case CommandRoot:
	}

	given := func(name Name) any {
		if value, found := flags.Values[name]; found {
			return value
		}

		return defaults.Values[name]
	}

	for _, selector := range []struct {
		key  Key[bool]
		mode Mode
	}{
		{Version, ModeVersion},
		{PrintSchema, ModePrintSchema},
		{ListModels, ModeListModels},
		{PrintRequest, ModePrintRequest},
		{PrintQuestions, ModePrintQuestions},
	} {
		if on, isSwitch := given(selector.key.Name()).(bool); isSwitch && on {
			return selector.mode
		}
	}

	name, isText := given(Input.Name()).(string)
	if !isText {
		return ModeAsk
	}

	switch mode, err := input.ParseMode(name); {
	case err != nil:
		return ModeAsk
	case mode == input.Request:
		return ModeRequest
	case mode.Streaming():
		return ModeStream
	default:
		return ModeAsk
	}
}

func choose(rows []Row, src Sources, flags Layer, mode Mode) (Choice, error) {
	chosen := Choice{File: textIn(flags, fileName), DefaultProfile: src.DefaultProfile}
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

func textIn(layer Layer, name Name) string {
	if text, isText := layer.Values[name].(string); isText {
		return text
	}

	return ""
}

func ambientText(rows []Row, name Name, lookupEnv func(string) (string, bool), mode Mode) (value, variable string) {
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

func readProfile(rows []Row, profile *ProfileValues, chosen Choice, mode Mode) (Layer, error) {
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

	if profile == nil {
		return layer, nil
	}

	for _, row := range rows {
		if !row.Modes.Has(mode) {
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

func parseHalf(row Row, raw string, half EndpointHalf) (any, error) {
	if endpoint, isEndpoint := row.Kind.(EndpointKind); isEndpoint {
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
	case profile != nil && strings.TrimSpace(profile.Members["provider"]) != "":
		return strings.TrimSpace(profile.Members["provider"])
	default:
		return onesie.TypeSafe().Name()
	}
}

func readFile(rows []Row, file *FileValues, mode Mode) (Layer, error) {
	layer := Layer{Source: SourceFile}
	if file == nil {
		return layer, nil
	}

	layer.Body = file.Body

	for _, row := range rows {
		if row.Scope != ScopeRun || row.FileKey == "" || row.InBody != file.Body || !row.Modes.Has(mode) {
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

	for _, question := range file.Questions {
		values, err := readQuestion(rows, question, file.Body, mode)
		if err != nil {
			return Layer{}, err
		}

		if layer.Questions == nil {
			layer.Questions = map[string]map[Name]any{}
		}

		layer.Questions[question.ID] = values
	}

	return layer, nil
}

func readQuestion(rows []Row, question QuestionValues, body bool, mode Mode) (map[Name]any, error) {
	values := map[Name]any{}

	for _, row := range rows {
		if row.Scope != ScopeQuestion || row.FileKey == "" || !row.Modes.Has(mode) {
			continue
		}

		raw, found := question.Values[row.FileKey]
		if !found {
			continue
		}

		value, err := row.Kind.FromFile(raw)
		if err != nil {
			return nil, fmt.Errorf("%s for '%s' %w",
				Origin{Source: SourceFile, Spelled: row.FileKey, Body: body}, question.ID, err)
		}

		values[row.Name] = value
	}

	return values, nil
}

func readEnv(rows []Row, lookupEnv func(string) (string, bool), provider string, mode Mode) (Layer, error) {
	layer := Layer{Source: SourceEnv}
	if lookupEnv == nil {
		return layer, nil
	}

	for _, row := range rows {
		if !row.Modes.Has(mode) {
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

			value, err := parseHalf(row, raw, variable.Half)
			if err != nil {
				return Layer{}, envError(row, variable.Name, raw, err)
			}

			layer.set(row.Name, value, variable.Name)

			break
		}
	}

	return layer, nil
}

func envError(row Row, variable, raw string, err error) error {
	switch row.Kind.(type) {
	case boolKind:
		return fmt.Errorf("%s=%s is not a value --%s takes. Use 1 or 0", variable, raw, row.Flag.Long)
	case durationKind:
		return fmt.Errorf("%s=%s is not a duration of zero or more, such as 90m or 72h", variable, raw)
	default:
		return fmt.Errorf("%s %w", variable, err)
	}
}
