package output_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestNotice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "should write the text as one prefixed line", text: "no profiles", want: "onesie: no profiles\n"},
		{name: "should write the prefix alone for empty text", text: "", want: "onesie: \n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got bytes.Buffer
			if err := output.Notice(&got, tc.text); err != nil {
				t.Fatalf("Notice: %v", err)
			}

			if got.String() != tc.want {
				t.Errorf("Notice wrote %q, want %q", got.String(), tc.want)
			}
		})
	}

	t.Run("should return the writer's error", func(t *testing.T) {
		t.Parallel()

		if err := output.Notice(failingWriter{}, "x"); !errors.Is(err, errWrite) {
			t.Errorf("Notice = %v, want %v", err, errWrite)
		}
	})
}

func TestWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "should write the text after the warning prefix",
			text: "-j 4 ignored. -i text reads one record",
			want: "onesie: warning: -j 4 ignored. -i text reads one record\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got bytes.Buffer
			if err := output.Warning(&got, tc.text); err != nil {
				t.Fatalf("Warning: %v", err)
			}

			if got.String() != tc.want {
				t.Errorf("Warning wrote %q, want %q", got.String(), tc.want)
			}
		})
	}

	t.Run("should return the writer's error", func(t *testing.T) {
		t.Parallel()

		if err := output.Warning(failingWriter{}, "x"); !errors.Is(err, errWrite) {
			t.Errorf("Warning = %v, want %v", err, errWrite)
		}
	})
}

var errWrite = errors.New("write refused")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWrite
}
