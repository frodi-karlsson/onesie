package settings

import "time"

// The keys of the settings that take one value, by row. A row given repeatedly, such as --assert,
// has no key.
var (
	Version        = Key[bool]{name: "version"}
	Output         = Key[string]{name: "output"}
	Raw            = Key[bool]{name: "raw"}
	Quiet          = Key[bool]{name: "quiet"}
	Input          = Key[string]{name: "input"}
	State          = Key[string]{name: "state"}
	StateFile      = Key[string]{name: "state_file"}
	Provider       = Key[string]{name: "provider"}
	Profile        = Key[string]{name: "profile"}
	PrintQuestions = Key[bool]{name: "print_questions"}
	PrintSchema    = Key[bool]{name: "print_schema"}
	ListModels     = Key[bool]{name: "list_models"}
	Unordered      = Key[bool]{name: "unordered"}
	StopOnError    = Key[bool]{name: "stop_on_error"}
	StopOnAssert   = Key[bool]{name: "stop_on_assert"}
	NoDedup        = Key[bool]{name: "no_dedup"}
	MergeInput     = Key[bool]{name: "merge"}
	MergeKey       = Key[string]{name: "merge_key"}
	Map            = Key[string]{name: "map"}
	ID             = Key[string]{name: "id"}
	Model          = Key[string]{name: "model"}
	APIEndpoint    = Key[EndpointValue]{name: "endpoint"}
	AllowHTTP      = Key[bool]{name: "allow_http"}
	LongContext    = Key[bool]{name: "long_context"}
	File           = Key[string]{name: "file"}
	Replace        = Key[bool]{name: "replace"}
	Jobs           = Key[int]{name: "jobs"}
	Timeout        = Key[int]{name: "timeout"}
	Retries        = Key[int]{name: "retries"}
	MaxRetryAfter  = Key[int]{name: "max_retry_after"}
	Out            = Key[string]{name: "out"}
	Resume         = Key[bool]{name: "resume"}
	Prune          = Key[bool]{name: "prune"}
	SkipBlank      = Key[bool]{name: "skip_blank"}
	Delimiter      = Key[string]{name: "delimiter"}
	Stats          = Key[bool]{name: "stats"}
	Usage          = Key[bool]{name: "usage"}
	PrintRequest   = Key[bool]{name: "print_request"}
	Cache          = Key[bool]{name: "cache"}
	Mock           = Key[string]{name: "mock"}
	Cuts           = Key[string]{name: "cuts"}
	Offline        = Key[bool]{name: "offline"}
	CacheTTL       = Key[time.Duration]{name: "cache_ttl"}
	ConfigDir      = Key[string]{name: "config_dir"}
	CacheDir       = Key[string]{name: "cache_dir"}
	Sep            = Key[string]{name: "sep"}
	Box            = Key[bool]{name: "box"}
	Threshold      = Key[float64]{name: "threshold"}
	MinConfidence  = Key[float64]{name: "min_confidence"}
	FallbackAnswer = Key[string]{name: "fallback"}
)

// Keys returns every key, so a test can check each one against its row.
func Keys() []Keyed {
	return []Keyed{
		Version, Output, Raw, Quiet, Input, State, StateFile, Provider, Profile, PrintQuestions, PrintSchema,
		ListModels, Unordered, StopOnError, StopOnAssert, NoDedup, MergeInput, MergeKey, Map, ID, Model,
		APIEndpoint, AllowHTTP, LongContext, File, Replace, Jobs, Timeout, Retries, MaxRetryAfter, Out, Resume,
		Prune, SkipBlank, Delimiter, Stats, Usage, PrintRequest, Cache, Mock, Cuts, Offline, CacheTTL, ConfigDir,
		CacheDir, Sep, Box, Threshold, MinConfidence, FallbackAnswer,
	}
}

// Keyed is a key of any value type.
type Keyed interface {
	Name() Name
	Fits(kind Kind) bool
}

// Get returns the value k's setting takes for the run, or the zero value of its kind when nothing
// gives it.
func Get[T any](m *Merged, k Key[T]) T {
	var value T

	if m == nil {
		return value
	}

	if given, ok := m.values[k.name].(T); ok {
		value = given
	}

	return value
}

// GetFor returns the value k's setting takes for one question, and whether that question has one.
func GetFor[T any](m *Merged, questionID string, k Key[T]) (T, bool) {
	var value T

	if m == nil {
		return value, false
	}

	value, found := m.questions[questionID][k.name].(T)

	return value, found
}

// Key gives typed access to a merged value, so a consumer never type asserts.
type Key[T any] struct {
	name Name
}

// Name is the row the key reads.
func (k Key[T]) Name() Name {
	return k.name
}

// Fits reports whether kind yields the key's value type.
func (k Key[T]) Fits(kind Kind) bool {
	_, ok := kind.Zero().(T)

	return ok
}
