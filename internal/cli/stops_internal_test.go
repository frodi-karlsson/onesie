package cli

import (
	"slices"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestStopsOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values map[string]any
		want   []output.Verdict
	}{
		{name: "should stop on nothing when no stop flag is given", values: map[string]any{}},
		{
			name:   "should stop on an error under --stop-on-error",
			values: map[string]any{"stop_on_error": true},
			want:   []output.Verdict{output.Error},
		},
		{
			name:   "should stop on a fail under --stop-on-assert",
			values: map[string]any{"stop_on_assert": true},
			want:   []output.Verdict{output.Fail},
		},
		{
			name:   "should stop on both under both flags",
			values: map[string]any{"stop_on_error": true, "stop_on_assert": true},
			want:   []output.Verdict{output.Fail, output.Error},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stops := stopsOf(runWith(tc.values))

			for verdict := output.Answered; verdict <= output.Error; verdict++ {
				if got, want := stops.has(verdict), slices.Contains(tc.want, verdict); got != want {
					t.Errorf("has(%v) = %v, want %v", verdict, got, want)
				}
			}
		})
	}
}

func TestStopSet_judges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stops stopSet
		want  bool
	}{
		{name: "should judge nothing when nothing stops the run", stops: stopSet(0)},
		{name: "should judge nothing when only an error stops the run", stops: stopSet(0).with(output.Error)},
		{name: "should judge when a fail stops the run", stops: stopSet(0).with(output.Fail), want: true},
		{name: "should judge when an unsure stops the run", stops: stopSet(0).with(output.Unsure), want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.stops.judges(); got != tc.want {
				t.Errorf("judges() = %v, want %v", got, tc.want)
			}
		})
	}
}
