package runner

import (
	"errors"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
)

func TestNewLiveAnswerer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "should pass an error on unchanged with no explainer", err: errors.New("refused")},
		{name: "should pass a nil error on with no explainer"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answerer := NewLiveAnswerer(nil, nil)

			if got := answerer.explain(tc.err, onesie.Request{}); !errors.Is(got, tc.err) {
				t.Errorf("explain = %v, want %v unchanged", got, tc.err)
			}
		})
	}
}
