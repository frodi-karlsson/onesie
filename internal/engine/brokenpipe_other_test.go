//go:build !windows

package engine

import (
	"errors"
	"syscall"
	"testing"
)

func TestPlatformBrokenPipe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "should stay quiet for an ordinary error", err: errors.New("boom")},
		{
			// 109 is ERROR_BROKEN_PIPE on Windows and means nothing on this platform, so it must
			// not match here.
			name: "should stay quiet for the Windows broken pipe errno",
			err:  syscall.Errno(109),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := platformBrokenPipe(tc.err); got {
				t.Errorf("platformBrokenPipe(%v) = true, want false", tc.err)
			}
		})
	}
}
