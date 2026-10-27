package patherr_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/patherr"
)

func TestInner(t *testing.T) {
	t.Parallel()

	missing := &fs.PathError{Op: "open", Path: "x.txt", Err: fs.ErrNotExist}
	plain := errors.New("gone")

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "should drop the operation and path a path error carries", err: missing, want: "file does not exist"},
		{name: "should keep an error that holds no path error", err: plain, want: "gone"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := patherr.Inner(tc.err)
			if got.Error() != tc.want {
				t.Errorf("Inner = %q, want %q", got, tc.want)
			}

			if errors.Is(tc.err, fs.ErrNotExist) && !errors.Is(got, fs.ErrNotExist) {
				t.Errorf("Inner = %v, want it to still match fs.ErrNotExist", got)
			}
		})
	}

	t.Run("should keep a nil error nil", func(t *testing.T) {
		t.Parallel()

		if got := patherr.Inner(nil); got != nil {
			t.Errorf("Inner(nil) = %v, want nil", got)
		}
	})
}
