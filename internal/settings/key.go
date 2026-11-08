package settings

import (
	"time"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

// The keys of the settings that take one value, by row. A row given repeatedly, such as --assert,
// has no key.
var (
	Output         = Key[string]{name: "output"}
	Quiet          = Key[bool]{name: "quiet"}
	Input          = Key[string]{name: "input"}
	State          = Key[string]{name: "state"}
	Provider       = Key[string]{name: "provider"}
	Profile        = Key[string]{name: "profile"}
	PrintQuestions = Key[bool]{name: "print_questions"}
	PrintSchema    = Key[bool]{name: "print_schema"}
	ListModels     = Key[bool]{name: "list_models"}
	Unordered      = Key[bool]{name: "unordered"}
	StopOn         = Key[string]{name: "stop_on"}
	NoDedup        = Key[bool]{name: "no_dedup"}
	MergeInput     = Key[bool]{name: "merge"}
	MergeKey       = Key[string]{name: "merge_key"}
	Map            = Key[string]{name: "map"}
	ID             = Key[string]{name: "id"}
	Model          = Key[string]{name: "model"}
	APIEndpoint    = Key[settingrow.EndpointValue]{name: "endpoint"}
	APIKey         = Key[string]{name: "api_key"}
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

// Derived returns the run default of k, or its merged value when no default applies.
func Derived[T any](v View, k Key[T]) T {
	var derive func(View) (any, bool)

	switch k.name {
	case Output.Name():
		derive = autoOutput
	case CacheTTL.Name():
		derive = aliasLifetime
	}
	if derive != nil {
		if value, derives := derive(v); derives {
			if typed, fits := value.(T); fits {
				return typed
			}
		}
	}

	return Get(v.Merged, k)
}

// Key gives typed access to a merged value, so a consumer never type asserts.
type Key[T any] struct {
	name settingrow.Name
}

// Name is the row the key reads.
func (k Key[T]) Name() settingrow.Name {
	return k.name
}
