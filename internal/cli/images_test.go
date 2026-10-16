package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		name        string
		req         onesie.Request
		timeoutSet  bool
		longContext bool
		want        int
	}{
		{name: "should raise the attempt timeout for a request with an image", req: withImage, want: 1},
		{name: "should leave an explicit --timeout alone", req: withImage, timeoutSet: true},
		{name: "should add nothing to a text request", req: onesie.Request{}},
		{name: "should add the long context header", req: onesie.Request{}, longContext: true, want: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := requestOptions(tc.req, tc.timeoutSet, tc.longContext); len(got) != tc.want {
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

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithClientFactory(func(_ context.Context, opts ...onesie.Option) (*onesie.Client, error) {
			return onesie.New(append([]onesie.Option{
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
