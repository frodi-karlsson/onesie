//go:build windows

package engine

import (
	"syscall"
	"testing"
)

func TestPlatformBrokenPipe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "should report broken for ERROR_BROKEN_PIPE",
			err:  syscall.ERROR_BROKEN_PIPE,
			want: true,
		},
		{
			name: "should report broken for ERROR_NO_DATA",
			err:  syscall.Errno(0xe8),
			want: true,
		},
		{
			name: "should stay quiet for an unrelated errno",
			err:  syscall.EACCES,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := platformBrokenPipe(tc.err); got != tc.want {
				t.Errorf("platformBrokenPipe(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
