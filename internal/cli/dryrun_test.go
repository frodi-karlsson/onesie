package cli

import (
	"slices"
	"testing"
)

func TestDryRunRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		wantAlways        []string
		wantQuestionsOnly []string
	}{
		{
			name: "should strip what a dry run refuses, and before --print-questions what it refuses too",
			wantAlways: []string{
				"output", "quiet", "usage", "merge", "merge-key", "stats", "stop-on", "mock", "cache",
			},
			wantQuestionsOnly: []string{
				"input", "jobs", "model", "state", "state-file", "unordered", "skip-blank", "map", "id",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root, always, questionsOnly := DryRunRules()
			if root == nil {
				t.Fatal("root is nil")
			}

			if !slices.Equal(always, tc.wantAlways) {
				t.Errorf("always = %q, want %q", always, tc.wantAlways)
			}

			if !slices.Equal(questionsOnly, tc.wantQuestionsOnly) {
				t.Errorf("questionsOnly = %q, want %q", questionsOnly, tc.wantQuestionsOnly)
			}
		})
	}
}
