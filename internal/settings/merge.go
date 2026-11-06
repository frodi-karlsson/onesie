package settings

import (
	"reflect"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie-go"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Merge takes each row's value, and each value of every question built asks, from the highest layer
// that gives one, with a notice for each value a higher source replaced. Derived works out the rest.
func Merge(rows []Row, layers []Layer, built *plan.Plan) (*Merged, []Notice) {
	ranked := highestFirst(layers)
	merged := &Merged{}

	var notices []Notice

	for _, row := range rows {
		switch {
		case row.Scope == ScopeQuestion, row.Name == profileName:
		case row.Name == providerName:
			notices = append(notices, mergeChoice(merged, ranked)...)
		default:
			notices = append(notices, mergeRow(merged, row, ranked)...)
		}
	}

	notices = pairKey(merged, ranked, notices)

	if built == nil {
		return merged, notices
	}

	for _, question := range built.Questions {
		for _, row := range rows {
			if row.Scope == ScopeQuestion {
				notices = append(notices, mergeQuestion(merged, row, question.ID, ranked)...)
			}
		}
	}

	return merged, append(notices, MergeQuestions(merged, built)...)
}

// MergeQuestions applies to each question from -f the flags its --for groups typed, and the top level
// flags when it is the one question asked, and records its settings in merged. It returns a sets or
// overrides notice for each setting a flag gave.
func MergeQuestions(merged *Merged, built *plan.Plan) []Notice {
	if built == nil {
		return nil
	}

	var notices []Notice

	for i := range built.Questions {
		question := &built.Questions[i]

		events := typedFor(built, question.ID)
		if !question.Origin.FromFile() || len(events) == 0 {
			continue
		}

		before := detached(*question)
		plan.Refine(question, events, PolicyNumbers())

		for _, row := range Table() {
			if row.Scope != ScopeQuestion || !slices.ContainsFunc(events, typedAs(row)) {
				continue
			}

			if row.Name == descName {
				notices = append(notices, describedNotices(row, before, *question, events)...)

				continue
			}

			was, now := questionSetting(before, row.Name), questionSetting(*question, row.Name)
			if now != nil && merged != nil && plan.PolicyFlag(row.Flag.Long) {
				merged.setFor(question.ID, row.Name, now)
			}

			if notice, changed := refinedNotice(row, *question, was, now); changed {
				notices = append(notices, notice)
			}
		}
	}

	return notices
}

func typedFor(built *plan.Plan, id string) []argv.Event {
	var events []argv.Event

	if len(built.Questions) == 1 {
		events = append(events, built.Orphans...)
	}

	for _, selection := range built.Selections {
		if selection.ID == id {
			events = append(events, selection.Events...)
		}
	}

	return events
}

func detached(question plan.Question) plan.Question {
	question.Options = slices.Clone(question.Options)
	question.Levels = slices.Clone(question.Levels)

	if question.Criteria != nil {
		criteria := *question.Criteria
		question.Criteria = &criteria
	}

	return question
}

func typedAs(row Row) func(argv.Event) bool {
	return func(event argv.Event) bool { return event.Name == row.Flag.Long }
}

func questionSetting(question plan.Question, name Name) any {
	policy := question.Policy

	switch {
	case name == Threshold.Name() && policy.Threshold != nil:
		return *policy.Threshold
	case name == MinConfidence.Name() && policy.MinConfidence != nil:
		return *policy.MinConfidence
	case name == FallbackAnswer.Name() && policy.Fallback != nil:
		return policy.Fallback.Text
	case name == pickName && question.Shape == plan.Pick:
		return question.Options
	case name == rateName && question.Shape == plan.Rate:
		return question.Levels
	default:
		return nil
	}
}

func describedNotices(row Row, before, after plan.Question, events []argv.Event) []Notice {
	was, now := descriptions(before), descriptions(after)

	var notices []Notice

	for _, event := range events {
		key, _, _ := strings.Cut(event.Value, "=")
		key = strings.TrimSpace(key)

		described, known := now[key]
		if event.Name != row.Flag.Long || !known || reflect.DeepEqual(was[key], described) {
			continue
		}

		notice, _ := refinedNotice(row, after, was[key], described)
		notice.Loser.Spelled = describedKey(after, key)

		// Several keys under one file key print one notice, which overrides when any key did.
		at := slices.IndexFunc(notices, func(n Notice) bool { return n.Loser.Spelled == notice.Loser.Spelled })
		switch {
		case at < 0:
			notices = append(notices, notice)
		case notice.Verb == VerbOverrides:
			notices[at].Verb = VerbOverrides
		}
	}

	return notices
}

func descriptions(question plan.Question) map[string]any {
	described := map[string]any{}

	switch question.Shape {
	case plan.Pick:
		for _, option := range question.Options {
			described[option.Name] = option.Desc
		}
	case plan.Rate:
		for _, level := range question.Levels {
			described[level.Label] = level.Desc
		}
	case plan.Noul:
		described["yes"], described["no"] = nil, nil
		if question.Criteria != nil {
			described["yes"], described["no"] = question.Criteria.Yes, question.Criteria.No
		}
	case plan.Box:
	}

	return described
}

func describedKey(question plan.Question, key string) string {
	switch question.Shape {
	case plan.Pick:
		return string(pickName)
	case plan.Rate:
		return string(rateName)
	default:
		return key + "_means"
	}
}

func refinedNotice(row Row, question plan.Question, was, now any) (Notice, bool) {
	if now == nil || reflect.DeepEqual(was, now) {
		return Notice{}, false
	}

	notice := Notice{
		Setting:  row.Name,
		Winner:   Origin{Source: SourceFlag, Spelled: "--" + row.Flag.Long},
		Loser:    Origin{Source: SourceFile, Spelled: row.FileKey, Body: question.Origin == plan.OriginBody},
		Question: question.ID,
	}

	if was == nil {
		notice.Verb = VerbSets
	}

	return notice, true
}

func mergeRow(merged *Merged, row Row, ranked []Layer) []Notice {
	candidates := holding(ranked, row.Name)
	if len(candidates) == 0 {
		return nil
	}

	winner := candidates[0]
	merged.Set(row.Name, winner.value, winner.layer.Source)
	merged.spelled(row.Name, originOf(winner.layer, row).Spelled)

	var notices []Notice

	for _, lower := range candidates[1:] {
		if lower.layer.Source == SourceDefault || reflect.DeepEqual(lower.value, winner.value) {
			continue
		}

		notices = append(notices, Notice{
			Setting: row.Name, Winner: originOf(winner.layer, row), Loser: originOf(lower.layer, row),
		})
	}

	return notices
}

func pairKey(merged *Merged, ranked []Layer, notices []Notice) []Notice {
	endpoint, holds := merged.values[endpointName].(EndpointValue)
	if !holds || merged.sources[endpointName] == SourceDefault || ownEndpoint(merged, endpoint) {
		return notices
	}

	pairs := keySources(merged.sources[endpointName])
	if source, held := merged.sources[apiKeyName]; !held || slices.Contains(pairs, source) {
		return notices
	}

	row, _ := Lookup(apiKeyName)
	dropped := merged.Origin(apiKeyName)
	notice := Notice{Setting: apiKeyName, Winner: merged.Origin(endpointName), Loser: dropped, Verb: VerbPairs}

	delete(merged.values, apiKeyName)
	delete(merged.sources, apiKeyName)
	delete(merged.spellings, apiKeyName)
	merged.setAside = map[Name]Origin{apiKeyName: dropped}

	for _, paired := range holding(ranked, apiKeyName) {
		if slices.Contains(pairs, paired.layer.Source) {
			merged.Set(apiKeyName, paired.value, paired.layer.Source)
			merged.spelled(apiKeyName, originOf(paired.layer, row).Spelled)

			break
		}
	}

	kept := slices.DeleteFunc(notices, func(n Notice) bool { return n.Setting == apiKeyName })

	return append(kept, notice)
}

func ownEndpoint(merged *Merged, endpoint EndpointValue) bool {
	// An account is the provider's own endpoint, and so is a whole account URL given as a base URL.
	provider, err := onesie.ProviderNamed(textOf(merged.values[providerName]))
	if err != nil {
		return false
	}

	if endpoint.BaseURL != "" {
		return !provider.CustomBaseURL(endpoint.BaseURL)
	}

	return endpoint.AccountID != ""
}

func keySources(endpoint Source) []Source {
	switch endpoint {
	case SourceEnv:
		return []Source{SourceEnv}
	case SourceFlag:
		// A typed --base-url is meant for this run, so it takes a key chosen for this run or one from
		// the environment, as a run without a profile always has, and never the default profile's.
		return []Source{SourceChosenProfile, SourceEnv}
	default:
		return []Source{endpoint}
	}
}

func textOf(value any) string {
	if text, isText := value.(string); isText {
		return text
	}

	return ""
}

func holding(ranked []Layer, name Name) []candidate {
	var candidates []candidate

	for _, layer := range ranked {
		if value, found := layer.Values[name]; found {
			candidates = append(candidates, candidate{layer: layer, value: value})
		}
	}

	return candidates
}

type candidate struct {
	layer Layer
	value any
}

func originOf(layer Layer, row Row) Origin {
	origin := Origin{Source: layer.Source, Spelled: layer.Spelled[row.Name], Body: layer.Body}
	if origin.Spelled != "" {
		return origin
	}

	switch layer.Source {
	case SourceFlag:
		origin.Spelled = "--" + row.Flag.Long
	case SourceFile:
		origin.Spelled = row.FileKey
	default:
	}

	return origin
}

func mergeChoice(merged *Merged, ranked []Layer) []Notice {
	// The profile and the provider are chosen as one: the highest flag or variable that names
	// either wins both, so a profile's key only ever goes to its own provider.
	for i, layer := range ranked {
		if layer.Source != SourceFlag && layer.Source != SourceEnv {
			continue
		}

		if name := textIn(layer, profileName); name != "" {
			return profileChosen(merged, ranked, i, name)
		}

		if name := textIn(layer, providerName); name != "" {
			return providerChosen(merged, ranked, i, name)
		}
	}

	for _, layer := range ranked {
		if layer.Source == SourceDefaultProfile {
			setFrom(merged, layer, profileName)
			setFrom(merged, layer, providerName)
		}
	}

	fallBack(merged, ranked, profileName)
	defaultProvider(merged)

	return nil
}

func defaultProvider(merged *Merged) {
	if _, holds := merged.values[providerName]; !holds {
		merged.Set(providerName, onesie.TypeSafe().Name(), SourceDefault)
	}
}

func profileChosen(merged *Merged, ranked []Layer, at int, name string) []Notice {
	chooser := ranked[at]
	merged.Set(profileName, name, chooser.Source)

	for _, layer := range ranked {
		if layer.Source == SourceChosenProfile {
			setFrom(merged, layer, providerName)
		}
	}

	defaultProvider(merged)

	winner := Origin{Source: chooser.Source, Spelled: chooser.Spelled[profileName]}

	var notices []Notice

	for _, layer := range ranked[at:] {
		if layer.Source != SourceFlag && layer.Source != SourceEnv {
			continue
		}

		if textIn(layer, providerName) != "" {
			notices = append(notices, choiceNotice(profileName, winner, layer, providerName, name))
		}

		if lower := textIn(layer, profileName); lower != "" && lower != name {
			notices = append(notices, choiceNotice(profileName, winner, layer, profileName, name))
		}
	}

	return notices
}

func providerChosen(merged *Merged, ranked []Layer, at int, name string) []Notice {
	chooser := ranked[at]
	merged.Set(providerName, name, chooser.Source)
	fallBack(merged, ranked, profileName)

	winner := Origin{Source: chooser.Source, Spelled: chooser.Spelled[providerName]}

	var notices []Notice

	for _, layer := range ranked[at+1:] {
		switch layer.Source {
		case SourceFlag, SourceEnv:
			if lower := textIn(layer, providerName); lower != "" && lower != name {
				notices = append(notices, choiceNotice(providerName, winner, layer, providerName, name))
			}
		case SourceDefaultProfile:
			if without := textIn(layer, profileName); without != "" {
				notices = append(notices, Notice{
					Setting: providerName, Winner: winner, Loser: Origin{Source: SourceDefaultProfile},
					Verb: VerbRunsWithout, Values: [2]string{name, without},
				})
			}
		default:
		}
	}

	return notices
}

func choiceNotice(setting Name, winner Origin, loser Layer, lost Name, name string) Notice {
	return Notice{
		Setting: setting, Winner: winner, Loser: Origin{Source: loser.Source, Spelled: loser.Spelled[lost]},
		Values: [2]string{name},
	}
}

func setFrom(merged *Merged, layer Layer, name Name) {
	if value, found := layer.Values[name]; found {
		merged.Set(name, value, layer.Source)
	}
}

func fallBack(merged *Merged, ranked []Layer, name Name) {
	if _, holds := merged.values[name]; holds {
		return
	}

	for _, layer := range ranked {
		if layer.Source == SourceDefault {
			setFrom(merged, layer, name)
		}
	}
}

func mergeQuestion(merged *Merged, row Row, id string, ranked []Layer) []Notice {
	var (
		candidates []candidate
		file       *Layer
	)

	for i, layer := range ranked {
		values, asks := layer.Questions[id]
		if layer.Source == SourceFile && asks {
			file = &ranked[i]
		}

		if value, found := values[row.Name]; found {
			candidates = append(candidates, candidate{layer: layer, value: value})
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	winner := candidates[0]
	merged.setFor(id, row.Name, winner.value)

	var notices []Notice

	for _, lower := range candidates[1:] {
		if lower.layer.Source != SourceDefault && !reflect.DeepEqual(lower.value, winner.value) {
			notices = append(notices, Notice{
				Setting: row.Name, Winner: originOf(winner.layer, row), Loser: originOf(lower.layer, row), Question: id,
			})
		}
	}

	if file != nil && winner.layer.Source > SourceFile && !slices.ContainsFunc(candidates, readFromFile) {
		notices = append(notices, Notice{
			Setting: row.Name, Winner: originOf(winner.layer, row), Loser: originOf(*file, row), Question: id,
			Verb: VerbSets,
		})
	}

	return notices
}

func readFromFile(c candidate) bool {
	return c.layer.Source == SourceFile
}

// Merged holds the value each setting takes for a run and the source that gave it, and the values
// each question takes.
type Merged struct {
	values    map[Name]any
	sources   map[Name]Source
	spellings map[Name]string
	setAside  map[Name]Origin
	questions map[string]map[Name]any
}

// Set records the value name takes for the run and the source that gave it.
func (m *Merged) Set(name Name, value any, source Source) {
	if m.values == nil {
		m.values = map[Name]any{}
	}

	if m.sources == nil {
		m.sources = map[Name]Source{}
	}

	m.values[name], m.sources[name] = value, source
}

func (m *Merged) spelled(name Name, spelling string) {
	if m.spellings == nil {
		m.spellings = map[Name]string{}
	}

	m.spellings[name] = spelling
}

func (m *Merged) setFor(questionID string, name Name, value any) {
	if m.questions == nil {
		m.questions = map[string]map[Name]any{}
	}

	if m.questions[questionID] == nil {
		m.questions[questionID] = map[Name]any{}
	}

	m.questions[questionID][name] = value
}

// Names lists every setting the merge holds a value for.
func (m *Merged) Names() []Name {
	if m == nil {
		return nil
	}

	names := make([]Name, 0, len(m.values))
	for name := range m.values {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// Value is name's value as stored, for a caller with no Key, such as a check that every value fits
// its row.
func (m *Merged) Value(name Name) any {
	if m == nil {
		return nil
	}

	return m.values[name]
}

// Source is where name's value came from, SourceDefault when nothing gave it.
func (m *Merged) Source(name Name) Source {
	if m == nil {
		return SourceDefault
	}

	return m.sources[name]
}

// Origin is where name's value came from, as a notice or a message names it.
func (m *Merged) Origin(name Name) Origin {
	if m == nil {
		return Origin{}
	}

	return Origin{Source: m.sources[name], Spelled: m.spellings[name]}
}

// SetAside is where a value came from that the merge set aside after it won, as a key from another
// source than the endpoint it would go to.
func (m *Merged) SetAside(name Name) (Origin, bool) {
	if m == nil {
		return Origin{}, false
	}

	origin, set := m.setAside[name]

	return origin, set
}

// Source ranks where a value came from, the higher one winning.
type Source int

const (
	// SourceDefault is a value no source gave.
	SourceDefault Source = iota
	// SourceDefaultProfile is a value from the default profile.
	SourceDefaultProfile
	// SourceEnv is a value from an environment variable.
	SourceEnv
	// SourceFile is a value from the question file or the request body.
	SourceFile
	// SourceChosenProfile is a value from the profile --profile or ONESIE_PROFILE chose.
	SourceChosenProfile
	// SourceFlag is a value typed on the command line.
	SourceFlag
)
