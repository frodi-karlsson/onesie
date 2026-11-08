package settings

import "github.com/frodi-karlsson/onesie/internal/settingrow"

// Keys returns every key, so a test can check each one against its row.
func Keys() []Keyed {
	return []Keyed{
		Version, Output, Raw, Quiet, Input, State, StateFile, Provider, Profile, PrintQuestions, PrintSchema,
		ListModels, Unordered, StopOnError, StopOnAssert, NoDedup, MergeInput, MergeKey, Map, ID, Model,
		APIEndpoint, APIKey, AllowHTTP, LongContext, File, Replace, Jobs, Timeout, Retries, MaxRetryAfter, Out, Resume,
		Prune, SkipBlank, Delimiter, Stats, Usage, PrintRequest, Cache, Mock, Cuts, Offline, CacheTTL, ConfigDir,
		CacheDir, Sep, Box, Threshold, MinConfidence, FallbackAnswer,
	}
}

// Keyed is a key of any value type.
type Keyed interface {
	Name() settingrow.Name
	Fits(kind settingrow.Kind) bool
}

// Fits reports whether kind yields the key's value type.
func (k Key[T]) Fits(kind settingrow.Kind) bool {
	_, ok := kind.Zero().(T)

	return ok
}
