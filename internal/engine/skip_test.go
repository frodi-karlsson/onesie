package engine_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/engine"
)

func TestSkip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		skip int
		want []string
	}{
		{name: "should drop the first records", skip: 2, want: []string{"c", "d"}},
		{name: "should pass everything through when told to skip none", skip: 0, want: []string{"a", "b", "c", "d"}},
		{name: "should yield nothing when told to skip more than there is", skip: 9, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source := engine.Skip[string](&sliceSource[string]{items: []string{"a", "b", "c", "d"}}, tc.skip)

			var got []string

			for {
				item, ok, err := source.Next()
				if err != nil {
					t.Fatalf("Next: %v", err)
				}

				if !ok {
					break
				}

				got = append(got, item)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("records = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("should return an error met while skipping", func(t *testing.T) {
		t.Parallel()

		broken := errors.New("stdin went away")

		_, _, err := engine.Skip[string](failingSource{err: broken}, 3).Next()
		if !errors.Is(err, broken) {
			t.Errorf("error = %v, want %v", err, broken)
		}
	})
}

type failingSource struct {
	err error
}

func (s failingSource) Next() (string, bool, error) {
	return "", false, s.err
}
