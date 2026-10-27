package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
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
			state:     json.RawMessage(`{"id":1,"photo":` + jsonText(t, empty) + `}`),
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
			state:   json.RawMessage(`{"photo":` + jsonText(t, junk) + `}`),
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

			m, err := newImaging(tc.images, tc.fields, false, os.ReadFile, nil)
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
		notErr  string
	}{
		{name: "should refuse an --image without a name", images: []string{"=x.png"}, wantErr: "--image takes NAME=PATH"},
		{name: "should refuse a name a backtick path cannot reach", images: []string{"a.b=x.png"}, wantErr: "--image a.b: a name holds no '.', '[', ']' or backtick"},
		{
			name:    "should refuse a missing file, naming it once",
			images:  []string{"a=" + filepath.Join(dir, "nope.png")},
			wantErr: "--image a: reading " + filepath.Join(dir, "nope.png") + ": ",
			notErr:  "open ",
		},
		{name: "should refuse an image the api would refuse", images: []string{"a=" + small}, wantErr: "is 200 by 200 pixels"},
		{name: "should refuse a name given twice", images: []string{"a=" + square, "a=" + square}, wantErr: "--image a is given twice"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := newImaging(tc.images, nil, false, os.ReadFile, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}

			if tc.notErr != "" && strings.Contains(err.Error(), tc.notErr) {
				t.Errorf("error = %v, want it without %q", err, tc.notErr)
			}
		})
	}
}

func TestImagingWrapLine(t *testing.T) {
	t.Parallel()

	t.Run("should wrap a line as the field's path under -i lines", func(t *testing.T) {
		t.Parallel()

		m, err := newImaging(nil, []string{"photo"}, true, os.ReadFile, nil)
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

func TestRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	square := writeImage(t, dir, "square.png", squarePNG(t))
	gif := writeImage(t, dir, "x.gif", []byte("GIF89a"))
	ref := refOf(t, squarePNG(t))
	encoded := base64.StdEncoding.EncodeToString(emptyPNG(t))
	asset := `"assets":{"body-img":{"type":"image","format":"png","data_base64":"` + encoded + `"}}`
	question := `"questions":{"red":{"type":"noul","instructions":"is there a red square in ` + "`photo`" + `"}}`
	withState := writeImage(t, dir, "with-state.json", []byte(`{"state":{"photo":"body-img"},`+question+`,`+asset+`}`))
	stateless := writeImage(t, dir, "stateless.json", []byte(`{`+question+`,`+asset+`}`))
	levanto := map[string]string{"ONESIE_PROVIDER": "levanto"}
	cloudflare := map[string]string{"ONESIE_PROVIDER": "cloudflare"}
	openrouter := map[string]string{"ONESIE_PROVIDER": "openrouter"}
	mockFile := writeImage(t, dir, "mock.json", []byte(`{"answer":0.9}`))
	refused := "onesie: the levanto provider takes no images"
	longBody := writeImage(t, dir, "long.json",
		[]byte(`{"state":"s","questions":{"red":{"type":"noul","instructions":"q"}},"long_context":true}`))
	shortBody := writeImage(t, dir, "short.json",
		[]byte(`{"state":"s","questions":{"red":{"type":"noul","instructions":"q"}},"long_context":false}`))
	const names = 25
	noise := writeImage(t, dir, "noise.png", noisePNG(t, 600, names))
	boxMock := writeImage(t, dir, "box-mock.json",
		[]byte(`{"where":{"left":0.25,"top":0.25,"right":0.5,"bottom":0.5}}`))
	// One image under many names is one asset, which passes the body limit, and openrouter places it at
	// every name, which does not.
	manyNames := func(extra ...string) []string {
		var args []string
		for i := range names {
			args = append(args, "--image", fmt.Sprintf("p%d=%s", i, noise))
		}

		return append(append(args, "q"), extra...)
	}

	tests := []struct {
		name         string
		args         []string
		stdin        string
		env          map[string]string
		wantCode     int
		wantRequests int
		wantBody     []string
		noBody       []string
		wantHeader   map[string]string
		wantErr      string
		wantOut      string
		emptyOut     bool
		errOnce      bool
	}{
		{
			name:         "should send the image as the state and never read stdin",
			args:         []string{"--image", "photo=" + square, "is there a red square in `photo`", "-o", "json"},
			stdin:        "text that must not be read",
			wantCode:     ExitOK,
			wantRequests: 1,
			wantBody:     []string{`"state":{"photo":"` + ref + `"}`, `"assets":{"` + ref + `":{"type":"image","format":"png"`},
			noBody:       []string{"text that must not be read"},
			wantHeader:   map[string]string{onesie.HeaderImages: "true"},
		},
		{
			name:     "should refuse -i json beside --image with no state flag, since stdin is never read",
			args:     []string{"-i", "json", "--image", "photo=" + square, "q", "--print-request"},
			stdin:    `{"a":1}`,
			wantCode: ExitUsage,
			wantErr:  "onesie: -i json holds one record, which beside --image reads stdin only through --state -. Pass --state - to read the state from stdin\n",
			emptyOut: true,
		},
		{
			name:         "should read a json state from stdin beside the image under --state -",
			args:         []string{"-i", "json", "--state", "-", "--image", "photo=" + square, "q", "-o", "json"},
			stdin:        `{"a":1}`,
			wantCode:     ExitOK,
			wantRequests: 1,
			wantBody:     []string{`"state":{"a":1,"photo":"` + ref + `"}`},
		},
		{
			name:         "should put --state text beside the image",
			args:         []string{"--image", "photo=" + square, "--state", "the door", "q", "-o", "json"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantBody:     []string{`"state":{"photo":"` + ref + `","text":"the door"}`},
		},
		{
			name:         "should merge the image state with its reference",
			args:         []string{"--image", "photo=" + square, "q", "-o", "json", "--merge"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantOut:      `"photo":"` + ref + `"`,
		},
		{
			name:         "should merge the text beside the image",
			args:         []string{"--image", "photo=" + square, "--state", "the door", "q", "-o", "json", "--merge"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantOut:      `"text":"the door"`,
		},
		{
			name:     "should refuse an image that is not png or jpeg before any request",
			args:     []string{"--image", "photo=" + gif, "q"},
			wantCode: ExitUsage,
			wantErr:  "onesie: --image photo: ",
		},
		{
			name:         "should send a body's state and assets",
			args:         []string{"-f", withState, "-o", "json"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantBody:     []string{`"state":{"photo":"body-img"}`, `"assets":{"body-img":`},
			wantHeader:   map[string]string{onesie.HeaderImages: "true"},
		},
		{
			name:     "should refuse --image beside a body that holds a state",
			args:     []string{"-f", withState, "--image", "photo=" + square},
			wantCode: ExitUsage,
			wantErr:  "onesie: --image and the state in",
		},
		{
			name:         "should drop an asset only the ignored state referenced, and say so",
			args:         []string{"-f", withState, "-i", "lines", "-o", "json"},
			stdin:        "one record\n",
			wantCode:     ExitOK,
			wantRequests: 1,
			noBody:       []string{`"assets"`},
			wantErr:      "and its 1 asset",
		},
		{
			name:     "should refuse a stateless body whose asset nothing references",
			args:     []string{"-f", stateless, "--image", "photo=" + square, "-o", "json"},
			wantCode: ExitUsage,
			wantErr:  "asset 'body-img' is not referenced",
		},
		{
			name:         "should send openrouter inline parts and no images header",
			args:         []string{"--image", "photo=" + square, "q", "-o", "json"},
			env:          openrouter,
			wantCode:     ExitOK,
			wantRequests: 1,
			wantBody:     []string{`"state":{"photo":{"type":"image_url","image_url":{"url":"data:image/png;base64,`},
			noBody:       []string{`"assets"`},
			wantHeader:   map[string]string{onesie.HeaderImages: ""},
		},
		{
			name:         "should rewrite a replayed body for openrouter",
			args:         []string{"-i", "request"},
			env:          openrouter,
			stdin:        `{"state":{"p":"r"},"model":"m","questions":{"answer":{"type":"noul","instructions":"q"}},"assets":{"r":{"type":"image","format":"png","data_base64":"AQ=="}}}` + "\n",
			wantCode:     ExitOK,
			wantRequests: 1,
			wantBody:     []string{`"state":{"p":{"type":"image_url"`},
			noBody:       []string{`"assets"`},
			wantHeader:   map[string]string{onesie.HeaderImages: ""},
		},
		{
			name:     "should write an error line for a replayed body with images on levanto",
			args:     []string{"-i", "request"},
			env:      levanto,
			stdin:    `{"state":{"p":"r"},"model":"m","questions":{"answer":{"type":"noul","instructions":"q"}},"assets":{"r":{"type":"image","format":"png","data_base64":"AQ=="}}}` + "\n",
			wantCode: ExitRecords,
			wantOut:  "takes no images. The typesafe, openrouter and cloudflare providers do",
		},
		{
			name:     "should refuse --long-context on cloudflare before any request",
			args:     []string{"--long-context", "--state", "s", "q"},
			env:      cloudflare,
			wantCode: ExitUsage,
			wantErr:  "onesie: the cloudflare provider takes no long context. The typesafe, openrouter, berget and levanto providers do\n",
		},
		{
			name:     "should say ONESIE_LONG_CONTEXT turned long context on when cloudflare refuses it",
			args:     []string{"--state", "s", "q"},
			env:      map[string]string{"ONESIE_PROVIDER": "cloudflare", "ONESIE_LONG_CONTEXT": "1"},
			wantCode: ExitUsage,
			wantErr: "onesie: the cloudflare provider takes no long context. The typesafe, openrouter, berget and " +
				"levanto providers do. ONESIE_LONG_CONTEXT turned it on, so unset it or pass --long-context=false\n",
		},
		{
			name:     "should say ONESIE_LONG_CONTEXT turned long context on under -i request",
			args:     []string{"-i", "request"},
			stdin:    `{"state":"s","questions":{"q":{"type":"noul","instructions":"q"}}}` + "\n",
			env:      map[string]string{"ONESIE_PROVIDER": "cloudflare", "ONESIE_LONG_CONTEXT": "1"},
			wantCode: ExitUsage,
			wantErr:  "ONESIE_LONG_CONTEXT turned it on, so unset it or pass --long-context=false\n",
		},
		{
			name:     "should refuse --box on cloudflare before any request",
			args:     []string{"--image", "photo=" + square, "--ask", "mug=the mug in `photo`", "--box"},
			env:      cloudflare,
			wantCode: ExitUsage,
			wantErr:  "onesie: the cloudflare provider takes no box questions. The typesafe and openrouter providers do\n",
		},
		{
			name:     "should refuse a question file's long context on cloudflare before any request",
			args:     []string{"-f", longBody},
			env:      cloudflare,
			wantCode: ExitUsage,
			wantErr:  "takes no long context",
		},
		{
			name:     "should refuse a cloudflare stream under --long-context once with no error lines",
			args:     []string{"-i", "lines", "--long-context", "q", "-o", "json"},
			env:      cloudflare,
			stdin:    "a\nb\n",
			wantCode: ExitUsage,
			wantErr:  "takes no long context",
			emptyOut: true,
			errOnce:  true,
		},
		{
			name:     "should refuse -i request --long-context on cloudflare before reading input",
			args:     []string{"-i", "request", "--long-context"},
			stdin:    `{"state":"s","questions":{"q":{"type":"noul","instructions":"q"}}}` + "\n",
			env:      cloudflare,
			wantCode: ExitUsage,
			wantErr:  "takes no long context",
		},
		{
			name:     "should print -i request bodies under --long-context on cloudflare",
			args:     []string{"-i", "request", "--long-context", "--print-request"},
			stdin:    `{"state":"s","questions":{"q":{"type":"noul","instructions":"q"}}}` + "\n",
			env:      cloudflare,
			wantCode: ExitOK,
			wantOut:  `"state":"s"`,
		},
		{
			name:     "should refuse --image on levanto before any request",
			args:     []string{"--image", "photo=" + square, "q"},
			env:      levanto,
			wantCode: ExitUsage,
			wantErr:  refused + ". The typesafe, openrouter and cloudflare providers do\n",
		},
		{
			name:     "should refuse --box on levanto before any request",
			args:     []string{"--ask", "mug=the mug", "--box", "--state", "s"},
			env:      levanto,
			wantCode: ExitUsage,
			wantErr:  "onesie: the levanto provider takes no box questions. The typesafe and openrouter providers do\n",
		},
		{
			name:     "should refuse an image and a box question on levanto, naming who takes both",
			args:     []string{"--image", "photo=" + square, "--ask", "mug=the mug in `photo`", "--box"},
			env:      levanto,
			wantCode: ExitUsage,
			wantErr:  refused + " or box questions. The typesafe and openrouter providers do\n",
		},
		{
			name:     "should refuse a levanto stream once with no error lines",
			args:     []string{"-i", "jsonl", "--image-field", "photo", "q", "-o", "json"},
			env:      levanto,
			stdin:    `{"photo":` + jsonText(t, square) + `}` + "\n" + `{"photo":` + jsonText(t, square) + `}` + "\n",
			wantCode: ExitUsage,
			wantErr:  refused,
			emptyOut: true,
			errOnce:  true,
		},
		{
			name: "should refuse calibrate with images on levanto",
			args: []string{
				"calibrate", "--ask", "red=is there a red square in `photo`", "-i", "jsonl", "--map", "{photo}",
				"--image-field", "photo", "--label", "red=.red", "--id", ".id",
			},
			env:      levanto,
			stdin:    `{"id":"a","photo":` + jsonText(t, square) + `,"red":true}` + "\n",
			wantCode: ExitUsage,
			wantErr:  refused,
		},
		{
			name:     "should print a levanto request with images, since a dry run sends nothing",
			args:     []string{"--image", "photo=" + square, "q", "--print-request"},
			env:      levanto,
			wantCode: ExitOK,
			wantOut:  `"assets":{"` + ref + `"`,
		},
		{
			name:     "should refuse a mock run with images under levanto, as the real run does",
			args:     []string{"--image", "photo=" + square, "q", "--mock", mockFile, "-o", "values"},
			env:      levanto,
			wantCode: ExitUsage,
			wantErr:  refused,
		},
		{
			name: "should refuse a mock box question under cloudflare, as the real run does",
			args: []string{
				"--image", "photo=" + square, "--ask", "where=the red square in `photo`", "--box",
				"--mock", boxMock,
			},
			env:      cloudflare,
			wantCode: ExitUsage,
			wantErr:  "onesie: the cloudflare provider takes no box questions",
			emptyOut: true,
		},
		{
			name:     "should fail a mock run whose images openrouter would inline past the body limit",
			args:     manyNames("--mock", mockFile, "-o", "values"),
			env:      openrouter,
			wantCode: ExitUsage,
			wantErr:  "name each image once",
		},
		{
			name:     "should answer the same mock run under typesafe, which sends each image once",
			args:     manyNames("--mock", mockFile, "-o", "values"),
			wantCode: ExitOK,
			wantOut:  `{"answer":0.9}`,
		},
		{
			name:     "should refuse a body whose long_context is not true or false",
			args:     []string{"-f", writeImage(t, dir, "bad-long.json", []byte(`{"state":"s","questions":{"red":{"type":"noul","instructions":"q"}},"long_context":"yes"}`))},
			wantCode: ExitUsage,
			wantErr:  "bad-long.json: 'long_context' in a request body must be true or false",
		},
		{
			name:     "should warn that --print-questions drops a body's long context",
			args:     []string{"-f", longBody, "--print-questions"},
			wantCode: ExitOK,
			wantErr:  "warning: --print-questions does not carry the body's long_context. Pass --long-context when you reload",
		},
		{
			name:         "should send long context from a body with no flag, and not the key",
			args:         []string{"-f", longBody, "-o", "json"},
			wantCode:     ExitOK,
			wantRequests: 1,
			noBody:       []string{"long_context"},
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name:         "should send long context from the flag beside a body's false",
			args:         []string{"-f", shortBody, "--long-context", "-o", "json"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name:     "should print long_context for --long-context",
			args:     []string{"--long-context", "--state", "s", "q", "--print-request"},
			wantCode: ExitOK,
			wantOut:  `"long_context":true`,
		},
		{
			name:     "should print long_context for ONESIE_LONG_CONTEXT",
			args:     []string{"--state", "s", "q", "--print-request"},
			env:      map[string]string{"ONESIE_LONG_CONTEXT": "1"},
			wantCode: ExitOK,
			wantOut:  `"long_context":true`,
		},
		{
			name:     "should print long_context for ONESIE_LONG_CONTEXT=TRUE, as the flag takes it",
			args:     []string{"--state", "s", "q", "--print-request"},
			env:      map[string]string{"ONESIE_LONG_CONTEXT": "TRUE"},
			wantCode: ExitOK,
			wantOut:  `"long_context":true`,
		},
		{
			name:     "should refuse an ONESIE_LONG_CONTEXT the flag would not take",
			args:     []string{"--state", "s", "q", "--print-request"},
			env:      map[string]string{"ONESIE_LONG_CONTEXT": "yes"},
			wantCode: ExitUsage,
			wantErr:  "onesie: ONESIE_LONG_CONTEXT=yes is not a value --long-context takes. Use 1 or 0\n",
			emptyOut: true,
		},
		{
			name:     "should refuse a bad ONESIE_CACHE under --mock",
			args:     []string{"--state", "s", "q", "--mock", mockFile},
			env:      map[string]string{"ONESIE_CACHE": "garbage"},
			wantCode: ExitUsage,
			wantErr:  "onesie: ONESIE_CACHE=garbage is not a value --cache takes. Use 1 or 0\n",
			emptyOut: true,
		},
		{
			name:     "should refuse a bad ONESIE_CACHE under --print-request",
			args:     []string{"--state", "s", "q", "--print-request"},
			env:      map[string]string{"ONESIE_CACHE": "garbage"},
			wantCode: ExitUsage,
			wantErr:  "onesie: ONESIE_CACHE=garbage is not a value --cache takes. Use 1 or 0\n",
			emptyOut: true,
		},
		{
			name:     "should refuse a bad ONESIE_CACHE_TTL under --mock",
			args:     []string{"--state", "s", "q", "--mock", mockFile},
			env:      map[string]string{"ONESIE_CACHE_TTL": "soon"},
			wantCode: ExitUsage,
			wantErr:  "onesie: ONESIE_CACHE_TTL=soon is not a duration of zero or more, such as 90m or 72h\n",
			emptyOut: true,
		},
		{
			name:     "should print long_context from a body",
			args:     []string{"-f", longBody, "--print-request"},
			wantCode: ExitOK,
			wantOut:  `"long_context":true`,
		},
		{
			name:     "should print long_context for a stream",
			args:     []string{"-i", "lines", "--long-context", "q", "--print-request"},
			stdin:    "a\n",
			wantCode: ExitOK,
			wantOut:  `"long_context":true`,
		},
		{
			name: "should print long_context for calibrate",
			args: []string{
				"calibrate", "--ask", "red=q", "-i", "jsonl", "--map", ".t", "--label", "red=.y",
				"--id", ".id", "--long-context", "--print-request",
			},
			stdin:    `{"id":"a","t":"x","y":true}` + "\n",
			wantCode: ExitOK,
			wantOut:  `"long_context":true`,
		},
		{
			name:         "should send --long-context under -i request as the header",
			args:         []string{"-i", "request", "--long-context"},
			stdin:        `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}}}` + "\n",
			wantCode:     ExitOK,
			wantRequests: 1,
			noBody:       []string{"long_context"},
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name:         "should send ONESIE_LONG_CONTEXT under -i request as the header",
			args:         []string{"-i", "request"},
			stdin:        `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}}}` + "\n",
			env:          map[string]string{"ONESIE_LONG_CONTEXT": "1"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name: "should keep ONESIE_LONG_CONTEXT on beside a replayed body's false",
			args: []string{"-i", "request"},
			stdin: `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}},` +
				`"long_context":false}` + "\n",
			env:          map[string]string{"ONESIE_LONG_CONTEXT": "1"},
			wantCode:     ExitOK,
			wantRequests: 1,
			noBody:       []string{"long_context"},
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name: "should keep a replayed body's true beside --long-context=false",
			args: []string{"-i", "request", "--long-context=false"},
			stdin: `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}},` +
				`"long_context":true}` + "\n",
			env:          map[string]string{"ONESIE_LONG_CONTEXT": "1"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name:     "should print long_context for --long-context under -i request",
			args:     []string{"-i", "request", "--long-context", "--print-request"},
			stdin:    `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}}}` + "\n",
			wantCode: ExitOK,
			wantOut:  `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}},"long_context":true}`,
		},
		{
			name: "should print long_context for ONESIE_LONG_CONTEXT over a replayed body's false",
			args: []string{"-i", "request", "--print-request"},
			stdin: `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}},` +
				`"long_context":false}` + "\n",
			env:      map[string]string{"ONESIE_LONG_CONTEXT": "1"},
			wantCode: ExitOK,
			wantOut:  `"long_context":true}`,
		},
		{
			name:         "should send a replayed body's long_context as the header",
			args:         []string{"-i", "request"},
			stdin:        `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}},"long_context":true}` + "\n",
			wantCode:     ExitOK,
			wantRequests: 1,
			noBody:       []string{"long_context"},
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name:         "should report the image attempt timeout under --stats",
			args:         []string{"--image", "photo=" + square, "q", "-o", "json", "--stats"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantErr:      "1m0s/attempt",
		},
		{
			name:         "should report an explicit --timeout under --stats beside an image",
			args:         []string{"--image", "photo=" + square, "q", "-o", "json", "--stats", "--timeout", "30"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantErr:      "30s/attempt",
		},
		{
			name:         "should send the long context header for --long-context",
			args:         []string{"--long-context", "--state", "s", "q", "-o", "json"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name:         "should let --long-context=false win over ONESIE_LONG_CONTEXT, as --cache=false does",
			args:         []string{"--long-context=false", "--state", "s", "q", "-o", "json"},
			env:          map[string]string{"ONESIE_LONG_CONTEXT": "true"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: ""},
		},
		{
			name:         "should let --long-context=false win over ONESIE_LONG_CONTEXT under -i request",
			args:         []string{"-i", "request", "--long-context=false"},
			stdin:        `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}}}` + "\n",
			env:          map[string]string{"ONESIE_LONG_CONTEXT": "1"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: ""},
		},
		{
			name: "should let --long-context=false win over ONESIE_LONG_CONTEXT on calibrate",
			args: []string{
				"calibrate", "--ask", "red=q", "-i", "jsonl", "--map", ".t", "--label", "red=.y", "--long-context=false",
			},
			stdin:        `{"t":"x","y":true}` + "\n",
			env:          map[string]string{"ONESIE_LONG_CONTEXT": "1"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: ""},
		},
		{
			name:         "should send the long context header for ONESIE_LONG_CONTEXT",
			args:         []string{"--state", "s", "q", "-o", "json"},
			env:          map[string]string{"ONESIE_LONG_CONTEXT": "1"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
		{
			name:         "should send no long context header without either",
			args:         []string{"--state", "s", "q", "-o", "json"},
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: ""},
		},
		{
			name:         "should send the images header on a replayed body with assets",
			args:         []string{"-i", "request"},
			stdin:        `{"state":{"p":"r"},"model":"m","questions":{"answer":{"type":"noul","instructions":"q"}},"assets":{"r":{"type":"image","format":"png","data_base64":"AQ=="}}}` + "\n",
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderImages: "true"},
		},
		{
			name:         "should send the long context header on a replayed body",
			args:         []string{"--long-context", "-i", "request"},
			stdin:        `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}}}` + "\n",
			wantCode:     ExitOK,
			wantRequests: 1,
			wantHeader:   map[string]string{onesie.HeaderLongContext: "true"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := runImaged(t, tc.args, tc.stdin, noulAnswer("answer", "red"), tc.env)

			if run.code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", run.code, tc.wantCode, run.stderr)
			}

			if len(run.bodies) != tc.wantRequests {
				t.Fatalf("requests = %d, want %d", len(run.bodies), tc.wantRequests)
			}

			if tc.wantErr != "" && !strings.Contains(run.stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", run.stderr, tc.wantErr)
			}

			if tc.wantOut != "" && !strings.Contains(run.stdout, tc.wantOut) {
				t.Errorf("stdout = %q, want it to contain %q", run.stdout, tc.wantOut)
			}

			if tc.emptyOut && run.stdout != "" {
				t.Errorf("stdout = %q, want it empty", run.stdout)
			}

			if tc.errOnce && strings.Count(run.stderr, tc.wantErr) != 1 {
				t.Errorf("stderr = %q, want %q once", run.stderr, tc.wantErr)
			}

			if len(run.bodies) == 0 {
				return
			}

			for _, want := range tc.wantBody {
				if !strings.Contains(run.bodies[0], want) {
					t.Errorf("body = %.300s, want it to contain %s", run.bodies[0], want)
				}
			}

			for _, unwanted := range tc.noBody {
				if strings.Contains(run.bodies[0], unwanted) {
					t.Errorf("body = %.300s, want it without %s", run.bodies[0], unwanted)
				}
			}

			for name, want := range tc.wantHeader {
				if got := run.headers[0].Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}

	t.Run("should send cloudflare requests through its routes", func(t *testing.T) {
		t.Parallel()

		wrapped := `{"result":` + noulAnswer("answer", "red") + `,"success":true}`

		tests := []struct {
			name     string
			args     []string
			wantBody []string
		}{
			{
				name:     "should send jev the image as an asset",
				args:     []string{"--image", "photo=" + square, "is there a red square in `photo`", "-o", "json"},
				wantBody: []string{`{"model":"typesafe/jev","input":{`, `"assets":{"` + ref + `":`},
			},
			{
				name:     "should send clef the image numbered",
				args:     []string{"--image", "photo=" + square, "is there a red square in `photo`", "-m", "clef-flash", "-o", "json"},
				wantBody: []string{`{"model":"@cf/cloudflare/clef-flash","input":{"state":{"photo":"image 1"}`, `"images":["data:image/png;base64,`},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				run := runImaged(t, tc.args, "", wrapped, cloudflare)
				if run.code != ExitOK || len(run.bodies) != 1 {
					t.Fatalf("exit code = %d, requests = %d, stderr:\n%s", run.code, len(run.bodies), run.stderr)
				}

				for _, want := range tc.wantBody {
					if !strings.Contains(run.bodies[0], want) {
						t.Errorf("body = %.300s, want it to contain %s", run.bodies[0], want)
					}
				}
			})
		}
	})

	t.Run("should refuse to resume once long context is added", func(t *testing.T) {
		t.Parallel()

		for _, mode := range []struct {
			name  string
			args  []string
			stdin string
		}{
			{name: "should refuse a stream", args: []string{"-i", "lines", "q"}, stdin: "a\n"},
			{
				name:  "should refuse -i request",
				args:  []string{"-i", "request"},
				stdin: `{"state":"s","model":"m","questions":{"answer":{"type":"noul","instructions":"q"}}}` + "\n",
			},
			{
				name: "should refuse calibrate",
				args: []string{
					"calibrate", "--ask", "red=q", "-i", "jsonl", "--map", ".t", "--label", "red=.y", "--id", ".id",
				},
				stdin: `{"id":"a","t":"x","y":true}` + "\n" + `{"id":"b","t":"z","y":false}` + "\n",
			},
		} {
			t.Run(mode.name, func(t *testing.T) {
				t.Parallel()

				out := filepath.Join(t.TempDir(), "out.jsonl")
				run := func(extra ...string) recordedRun {
					return runImaged(t, append(append(slices.Clone(mode.args), "--out", out), extra...),
						mode.stdin, noulAnswer("answer", "red"), nil)
				}

				if first := run(); first.code != ExitOK {
					t.Fatalf("first run = %d\n%s", first.code, first.stderr)
				}

				if same := run("--resume"); same.code != ExitOK {
					t.Fatalf("a resume with the same flags = %d\n%s", same.code, same.stderr)
				}

				added := run("--resume", "--long-context")
				if added.code != ExitUsage || !strings.Contains(added.stderr, "changed") {
					t.Errorf("a resume adding --long-context = %d\n%s, want 2 and changed", added.code, added.stderr)
				}
			})
		}
	})
}

func TestReadRawRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want rawRequest
	}{
		{
			name: "should read the model and count the questions",
			body: `{"state":"s","model":"m","questions":{"a":{"type":"noul"},"b":{"type":"choice"}}}`,
			want: rawRequest{model: "m", questions: 2},
		},
		{
			name: "should need images for a body with assets",
			body: `{"state":"s","questions":{"q":{"type":"noul"}},"assets":{"r":{"format":"png","data_base64":"AQ=="}}}`,
			want: rawRequest{questions: 1, needsImages: true},
		},
		{
			name: "should need images for a box question",
			body: `{"state":"s","questions":{"m":{"type":"box"}}}`,
			want: rawRequest{questions: 1, needsImages: true, boxed: true},
		},
		{
			name: "should read a body's long_context",
			body: `{"model":"m","questions":{"q":{"type":"noul"}},"long_context":true}`,
			want: rawRequest{model: "m", questions: 1, longContext: true},
		},
		{name: "should read nothing from a body it cannot parse", body: `not json`},
		{
			name: "should keep the model and count past a question that is not an object",
			body: `{"model":"m","questions":{"q":"text"}}`,
			want: rawRequest{model: "m", questions: 1},
		},
		{
			name: "should keep the model past assets that are not an object",
			body: `{"model":"m","questions":{"q":{"type":"noul"}},"assets":"x"}`,
			want: rawRequest{model: "m", questions: 1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := readRawRequest([]byte(tc.body)); got != tc.want {
				t.Errorf("readRawRequest = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRequestOptions(t *testing.T) {
	t.Parallel()

	withImage := onesie.Request{Assets: onesie.Assets{"r": {}}}

	tests := []struct {
		name       string
		req        onesie.Request
		timeoutSet bool
		want       int
	}{
		{name: "should raise the attempt timeout for a request with an image", req: withImage, want: 1},
		{name: "should leave an explicit --timeout alone", req: withImage, timeoutSet: true},
		{name: "should add nothing to a text request", req: onesie.Request{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := requestOptions(tc.req, tc.timeoutSet); len(got) != tc.want {
				t.Errorf("options = %d, want %d", len(got), tc.want)
			}
		})
	}
}

func TestAskLabelled(t *testing.T) {
	t.Parallel()

	t.Run("should send each record's image when calibrating", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		square := writeImage(t, dir, "square.png", squarePNG(t))
		empty := writeImage(t, dir, "empty.png", emptyPNG(t))

		stdin := `{"id":"a","photo":` + jsonText(t, square) + `,"red":true}` + "\n" +
			`{"id":"b","photo":` + jsonText(t, empty) + `,"red":false}` + "\n"

		run := runImaged(t, []string{
			"calibrate", "--ask", "red=is there a red square in `photo`", "-i", "jsonl",
			"--map", "{photo}", "--image-field", "photo", "--label", "red=.red", "--id", ".id", "-o", "json",
		}, stdin, noulAnswer("red"), nil)

		if run.code != ExitOK {
			t.Fatalf("exit code = %d, want 0\nstderr:\n%s", run.code, run.stderr)
		}

		if len(run.bodies) != 2 {
			t.Fatalf("requests = %d, want two", len(run.bodies))
		}

		for i, body := range run.bodies {
			if !strings.Contains(body, `"assets":{"image-`) {
				t.Errorf("body %d = %.200s, want an image asset", i, body)
			}
		}
	})
}

func runImaged(t *testing.T, args []string, stdin, answers string, env map[string]string) recordedRun {
	t.Helper()

	var (
		mu  sync.Mutex
		run recordedRun
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}

		mu.Lock()
		run.bodies = append(run.bodies, string(body))
		run.headers = append(run.headers, r.Header.Clone())
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		if _, err := io.WriteString(w, answers); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))
	defer srv.Close()

	provider, err := onesie.ProviderNamed(env["ONESIE_PROVIDER"])
	if err != nil {
		t.Fatalf("unknown provider: %v", err)
	}

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithClientFactory(func(_ context.Context, opts ...onesie.Option) (*onesie.Client, error) {
			return onesie.New(append([]onesie.Option{
				onesie.WithProvider(provider),
				onesie.WithAPIKey("k"),
				onesie.WithBaseURL(srv.URL),
				onesie.WithEnv(func(string) (string, bool) { return "", false }),
			}, opts...)...)
		}),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(env)),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	run.code = Execute(t.Context(), root)

	mu.Lock()
	defer mu.Unlock()

	run.stdout = out.String()
	run.stderr = errOut.String()

	return run
}

type recordedRun struct {
	bodies  []string
	headers []http.Header
	stdout  string
	stderr  string
	code    int
}

func noulAnswer(ids ...string) string {
	answers := make([]string, 0, len(ids))
	for _, id := range ids {
		answers = append(answers, `"`+id+`":{"type":"noul","noul":0.9}`)
	}

	return `{"model":"m","answers":{` + strings.Join(answers, ",") + `},"usage":{}}`
}

func jsonText(t *testing.T, text string) string {
	t.Helper()

	encoded, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("encoding %q: %v", text, err)
	}

	return string(encoded)
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

func noisePNG(t *testing.T, side, names int) []byte {
	t.Helper()

	// Seeded, so the size is the same on every run. Noise does not compress, so the file stays
	// near four bytes a pixel.
	random := rand.New(rand.NewPCG(1, 2))

	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = byte(random.UintN(256))
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding the png: %v", err)
	}

	if encoded := base64.StdEncoding.EncodedLen(buf.Len()); encoded*names <= onesie.MaxBodyBytes {
		t.Fatalf("the noise png is %d bytes, which no longer goes over the body limit at %d names", buf.Len(), names)
	}

	if buf.Len() >= onesie.MaxImageBytes {
		t.Fatalf("the noise png is %d bytes, which is over the image limit", buf.Len())
	}

	return buf.Bytes()
}
