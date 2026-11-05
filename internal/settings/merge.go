package settings

// Merged holds the value each setting takes for a run, and the values each question takes.
type Merged struct {
	values    map[Name]any
	questions map[string]map[Name]any
}
