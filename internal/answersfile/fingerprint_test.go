package answersfile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestFingerprint(t *testing.T) {
	t.Parallel()

	questions := []plan.Question{{ID: "urgent", Policy: plan.Policy{Fallback: &plan.Fallback{Text: "no"}}}}
	wired := json.RawMessage(`[]`)
	inputs := FingerprintInputs{Provider: "typesafe", Model: "m", Output: "csv", Input: "lines"}

	t.Run("should sum each part of the run after the version", func(t *testing.T) {
		t.Parallel()

		got, err := Fingerprint(questions, wired, inputs)
		if err != nil {
			t.Fatal(err)
		}

		names := []string{}
		for _, part := range strings.Split(strings.TrimPrefix(got.String(), "v3:"), ",") {
			name, sum, _ := strings.Cut(part, "=")
			if len(sum) != 2*partSumLength {
				t.Errorf("part %s has the sum %q, want %d hex digits", name, sum, 2*partSumLength)
			}

			names = append(names, name)
		}

		want := "questions policies provider model map id output input merge_key assert abstain_if fallback_columns"
		if strings.Join(names, " ") != want {
			t.Errorf("parts = %v, want %s", names, want)
		}
	})

	t.Run("should match the single sum version 2 kept and its sum without the fallback columns", func(t *testing.T) {
		t.Parallel()

		got, err := Fingerprint(questions, wired, inputs)
		if err != nil {
			t.Fatal(err)
		}

		if !got.Matches(got.older) || !strings.HasPrefix(got.older, "v2:") {
			t.Errorf("older sum %q does not match, want a version 2 sum that does", got.older)
		}

		if got.olderColumns == "" || got.Matches(got.olderColumns) {
			t.Errorf("older columns sum %q, want one that is set and is not a match", got.olderColumns)
		}
	})

	t.Run("should keep no sum without the fallback columns for json output", func(t *testing.T) {
		t.Parallel()

		json := inputs
		json.Output = "json"

		got, err := Fingerprint(questions, wired, json)
		if err != nil {
			t.Fatal(err)
		}

		if got.olderColumns != "" {
			t.Errorf("older columns sum = %q, want none", got.olderColumns)
		}
	})
}

func TestStaleReason(t *testing.T) {
	t.Parallel()

	current := Print{
		parts: []partSum{{name: "questions", sum: strings.Repeat("ab", 8)}, {name: "model", sum: strings.Repeat("cd", 8)}},
		older: "v2:" + strings.Repeat("ab", 32), olderColumns: "v2:" + strings.Repeat("ef", 32),
	}

	tests := []struct {
		name        string
		stored      string
		found       bool
		want        StaleReason
		wantChanged []string
		wantStale   bool
	}{
		{name: "should accept the run's own fingerprint", stored: current.String(), found: true},
		{name: "should accept the single sum version 2 kept", stored: current.older, found: true},
		{name: "should refuse a file with no fingerprint", want: NoFingerprint, wantStale: true},
		{
			name: "should refuse a fingerprint onesie did not write", stored: "not a fingerprint", found: true,
			want: ForeignFingerprint, wantStale: true,
		},
		{
			name: "should refuse a version 3 fingerprint with no parts", stored: "v3:" + strings.Repeat("0", 64),
			found: true, want: ForeignFingerprint, wantStale: true,
		},
		{
			name: "should refuse a version 3 fingerprint that names a part twice", found: true,
			stored: "v3:model=" + strings.Repeat("cd", 8) + ",model=" + strings.Repeat("cd", 8),
			want:   ForeignFingerprint, wantStale: true,
		},
		{
			name: "should refuse an older version", stored: "v1:" + strings.Repeat("ab", 32), found: true,
			want: OlderFingerprint, wantStale: true,
		},
		{name: "should refuse a newer version", stored: "v4:x", found: true, want: NewerFingerprint, wantStale: true},
		{
			name: "should name the older columns when a version 2 sum is theirs", stored: current.olderColumns,
			found: true, want: OlderColumns, wantStale: true,
		},
		{
			name:   "should refuse any other version 2 sum as a changed run, naming no part",
			stored: "v2:" + strings.Repeat("cd", 32), found: true, want: ChangedRun, wantStale: true,
		},
		{
			name: "should name the part that changed", found: true,
			stored: "v3:questions=" + strings.Repeat("ab", 8) + ",model=" + strings.Repeat("00", 8),
			want:   ChangedRun, wantChanged: []string{"the model"}, wantStale: true,
		},
		{
			name: "should name every part that changed, a part the file lacks and one the run lacks", found: true,
			stored: "v3:questions=" + strings.Repeat("00", 8) + ",mock=" + strings.Repeat("00", 8),
			want:   ChangedRun, wantChanged: []string{"the questions", "the model", "--mock"}, wantStale: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, changed, stale := staleReason(tc.stored, tc.found, current)
			if got != tc.want || stale != tc.wantStale || strings.Join(changed, ", ") != strings.Join(tc.wantChanged, ", ") {
				t.Errorf("staleReason = %v, %v, %v, want %v, %v, %v", got, changed, stale, tc.want, tc.wantChanged, tc.wantStale)
			}
		})
	}
}

func TestStaleAnswersError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		changed   []string
		wantError string
		wantCause string
	}{
		{
			name:      "should keep the wording of a version 2 sidecar, which names no part",
			wantError: "the questions, model or flags changed since A was written",
			wantCause: "the questions, model or flags changed since it was written",
		},
		{
			name:      "should name one part",
			changed:   []string{"-o"},
			wantError: "-o changed since A was written",
			wantCause: "-o changed since it was written",
		},
		{
			name:      "should join the last of several parts with and",
			changed:   []string{"the model", "--map", "--assert"},
			wantError: "the model, --map and --assert changed since A was written",
			wantCause: "the model, --map and --assert changed since it was written",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := &StaleAnswersError{Path: "A", Sidecar: "A.onesie", Reason: ChangedRun, Changed: tc.changed}
			if err.Error() != tc.wantError || err.Cause() != tc.wantCause {
				t.Errorf("Error = %q, Cause = %q, want %q and %q", err.Error(), err.Cause(), tc.wantError, tc.wantCause)
			}
		})
	}
}
