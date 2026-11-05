package settings

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
