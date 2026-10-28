package answersfile

import (
	"strings"
	"testing"
)

func TestStaleReason(t *testing.T) {
	t.Parallel()

	current := "v2:" + strings.Repeat("ab", 32)
	other := "v2:" + strings.Repeat("cd", 32)

	tests := []struct {
		name         string
		stored       string
		found        bool
		olderColumns string
		want         StaleReason
		wantStale    bool
	}{
		{name: "should accept the run's own fingerprint", stored: current, found: true},
		{name: "should refuse a file with no fingerprint", want: NoFingerprint, wantStale: true},
		{
			name: "should refuse a fingerprint onesie did not write", stored: "not a fingerprint", found: true,
			want: ForeignFingerprint, wantStale: true,
		},
		{
			name: "should refuse an older version", stored: "v1:" + strings.Repeat("ab", 32), found: true,
			want: OlderFingerprint, wantStale: true,
		},
		{name: "should refuse a newer version", stored: "v3:x", found: true, want: NewerFingerprint, wantStale: true},
		{
			name: "should name the older columns when the stored sum is theirs", stored: other, found: true,
			olderColumns: other, want: OlderColumns, wantStale: true,
		},
		{name: "should refuse a changed run", stored: other, found: true, want: ChangedRun, wantStale: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, stale := staleReason(tc.stored, tc.found, current, tc.olderColumns)
			if got != tc.want || stale != tc.wantStale {
				t.Errorf("staleReason = %v, %v, want %v, %v", got, stale, tc.want, tc.wantStale)
			}
		})
	}
}
