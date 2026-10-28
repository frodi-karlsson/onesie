package cli

import (
	"errors"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
)

func TestEmbed(t *testing.T) {
	t.Parallel()

	cause := &onesie.ValidationError{Message: "image 'photo' is not a PNG or JPEG"}

	tests := []struct {
		name    string
		context string
		want    string
	}{
		{name: "should put the context before the reason", context: "photo.gif", want: "photo.gif: image 'photo' is not a PNG or JPEG"},
		{name: "should give the reason alone with no context", want: "image 'photo' is not a PNG or JPEG"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := embed(tc.context, cause)
			if got.Error() != tc.want {
				t.Errorf("Error = %q, want %q", got.Error(), tc.want)
			}

			if !errors.Is(got, onesie.ErrValidation) {
				t.Error("the embedded error does not unwrap to its cause")
			}
		})
	}
}

func TestText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "should read a library error's reason", err: &onesie.ValidationError{Message: "question too long"}, want: "question too long"},
		{name: "should read the text of any other error", err: errors.New("reading x.yaml: no such file"), want: "reading x.yaml: no such file"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := text(tc.err); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
		})
	}
}
