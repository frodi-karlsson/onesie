package settings_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestGet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		merged     func() *settings.Merged
		wantJobs   int
		wantSource settings.Source
	}{
		{
			name:       "should return the zero of a row with no source",
			merged:     func() *settings.Merged { return &settings.Merged{} },
			wantSource: settings.SourceDefault,
		},
		{
			name:       "should return the zero of a nil merge",
			merged:     func() *settings.Merged { return nil },
			wantSource: settings.SourceDefault,
		},
		{
			name: "should return a merged value with its source",
			merged: func() *settings.Merged {
				merged := &settings.Merged{}
				merged.Set(settings.Jobs.Name(), 4, settings.SourceFlag)

				return merged
			},
			wantJobs:   4,
			wantSource: settings.SourceFlag,
		},
		{
			name: "should return the zero of a value of another type",
			merged: func() *settings.Merged {
				merged := &settings.Merged{}
				merged.Set(settings.Jobs.Name(), "4", settings.SourceEnv)

				return merged
			},
			wantSource: settings.SourceEnv,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			merged := tc.merged()

			if got := settings.Get(merged, settings.Jobs); got != tc.wantJobs {
				t.Errorf("Get = %d, want %d", got, tc.wantJobs)
			}

			if got := merged.Source(settings.Jobs.Name()); got != tc.wantSource {
				t.Errorf("Source = %d, want %d", got, tc.wantSource)
			}
		})
	}
}
