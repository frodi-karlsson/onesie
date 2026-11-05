package settings

import (
	"reflect"
	"slices"

	"github.com/frodi-karlsson/onesie-go"

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

	return merged, notices
}

func mergeRow(merged *Merged, row Row, ranked []Layer) []Notice {
	candidates := holding(ranked, row.Name)
	if len(candidates) == 0 {
		return nil
	}

	winner := candidates[0]
	merged.Set(row.Name, winner.value, winner.layer.Source)

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
