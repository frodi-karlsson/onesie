package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImagingAttach(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	square := writeImage(t, dir, "square.png", squarePNG(t))
	empty := writeImage(t, dir, "empty.png", emptyPNG(t))
	junk := writeImage(t, dir, "junk.txt", []byte("not an image"))

	tests := []struct {
		name      string
		images    []string
		fields    []string
		state     any
		wantState string
		wantRefs  int
		wantErr   string
	}{
		{
			name:      "should make the images the state when there is no other state",
			images:    []string{"photo=" + square},
			wantState: `{"photo":"` + refOf(t, squarePNG(t)) + `"}`,
			wantRefs:  1,
		},
		{
			name:      "should put a text state under text beside the images",
			images:    []string{"photo=" + square},
			state:     "the front door",
			wantState: `{"photo":"` + refOf(t, squarePNG(t)) + `","text":"the front door"}`,
			wantRefs:  1,
		},
		{
			name:      "should add the images to an object state",
			images:    []string{"photo=" + square},
			state:     json.RawMessage(`{"camera":"front","id":12345678901234567890}`),
			wantState: `{"camera":"front","id":12345678901234567890,"photo":"` + refOf(t, squarePNG(t)) + `"}`,
			wantRefs:  1,
		},
		{
			name:    "should refuse an image named like a key the state has",
			images:  []string{"camera=" + square},
			state:   json.RawMessage(`{"camera":"front"}`),
			wantErr: "--image camera: the state already has a key 'camera'",
		},
		{
			name:    "should refuse an image named text beside a text state",
			images:  []string{"text=" + square},
			state:   "the front door",
			wantErr: "--image text: a text state goes under the key 'text'",
		},
		{
			name:    "should refuse an array state",
			images:  []string{"photo=" + square},
			state:   json.RawMessage(`["a","b"]`),
			wantErr: "--image needs an object or a text state to sit beside, got an array",
		},
		{
			name:      "should replace a field's path with its image",
			fields:    []string{"photo"},
			state:     json.RawMessage(`{"id":1,"photo":"` + empty + `"}`),
			wantState: `{"id":1,"photo":"` + refOf(t, emptyPNG(t)) + `"}`,
			wantRefs:  1,
		},
		{
			name:    "should refuse a record without the field",
			fields:  []string{"photo"},
			state:   json.RawMessage(`{"id":1}`),
			wantErr: "--image-field photo: the record has no 'photo'",
		},
		{
			name:    "should refuse a field that is not a path",
			fields:  []string{"photo"},
			state:   json.RawMessage(`{"photo":7}`),
			wantErr: "--image-field photo: the value is a number, not a path",
		},
		{
			name:    "should refuse a field naming a file that is not an image",
			fields:  []string{"photo"},
			state:   json.RawMessage(`{"photo":"` + junk + `"}`),
			wantErr: "an image must be PNG or JPEG",
		},
		{
			name:      "should send one asset for an image used twice",
			images:    []string{"a=" + square, "b=" + square},
			wantState: `{"a":"` + refOf(t, squarePNG(t)) + `","b":"` + refOf(t, squarePNG(t)) + `"}`,
			wantRefs:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m, err := newImaging(tc.images, tc.fields, false, os.ReadFile)
			if err != nil {
				if tc.wantErr == "" || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("newImaging error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			state, assets, err := m.attach(tc.state)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("attach error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if string(encoded) != tc.wantState {
				t.Errorf("state = %s, want %s", encoded, tc.wantState)
			}

			if len(assets) != tc.wantRefs {
				t.Errorf("assets = %d, want %d", len(assets), tc.wantRefs)
			}
		})
	}
}

func TestNewImaging(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	small := writeImage(t, dir, "small.png", sizedPNG(t, 200, 200))
	square := writeImage(t, dir, "square.png", squarePNG(t))

	tests := []struct {
		name    string
		images  []string
		wantErr string
	}{
		{name: "should refuse an --image without a name", images: []string{"=x.png"}, wantErr: "--image takes NAME=PATH"},
		{name: "should refuse a name a backtick path cannot reach", images: []string{"a.b=x.png"}, wantErr: "--image a.b: a name holds no '.', '[', ']' or backtick"},
		{name: "should refuse a missing file", images: []string{"a=" + filepath.Join(dir, "nope.png")}, wantErr: "--image a: reading"},
		{name: "should refuse an image the api would refuse", images: []string{"a=" + small}, wantErr: "is 200 by 200 pixels"},
		{name: "should refuse a name given twice", images: []string{"a=" + square, "a=" + square}, wantErr: "--image a is given twice"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := newImaging(tc.images, nil, false, os.ReadFile)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestImagingWrapLine(t *testing.T) {
	t.Parallel()

	t.Run("should wrap a line as the field's path under -i lines", func(t *testing.T) {
		t.Parallel()

		m, err := newImaging(nil, []string{"photo"}, true, os.ReadFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		state, wire := m.wrapLine("a.jpg", "a.jpg")

		want := map[string]any{"photo": "a.jpg"}
		if !equalJSON(t, state, want) || !equalJSON(t, wire, want) {
			t.Errorf("wrapLine = %v, %v, want %v", state, wire, want)
		}
	})
}

func refOf(t *testing.T, data []byte) string {
	t.Helper()

	return imageRef(data)
}

func writeImage(t *testing.T, dir, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}

	return path
}

func equalJSON(t *testing.T, got, want any) bool {
	t.Helper()

	left, leftErr := json.Marshal(got)
	right, rightErr := json.Marshal(want)

	return errors.Join(leftErr, rightErr) == nil && bytes.Equal(left, right)
}

func squarePNG(t *testing.T) []byte {
	t.Helper()

	return drawPNG(t, 640, 480, image.Rect(160, 120, 320, 240))
}

func emptyPNG(t *testing.T) []byte {
	t.Helper()

	return drawPNG(t, 640, 480, image.Rectangle{})
}

func sizedPNG(t *testing.T, width, height int) []byte {
	t.Helper()

	return drawPNG(t, width, height, image.Rectangle{})
}

func drawPNG(t *testing.T, width, height int, square image.Rectangle) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})

			if image.Pt(x, y).In(square) {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding the png: %v", err)
	}

	return buf.Bytes()
}
