package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/examples"
	"github.com/frodi-karlsson/onesie/internal/cli"
)

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("should refuse to run under ONESIE_MOCK", func(t *testing.T) {
		t.Parallel()

		code, ended := run(t.Context(), options{
			lookup: envOf(map[string]string{"ONESIE_MOCK": "m.json", keyName: "k"}), dir: t.TempDir(),
			stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		})
		if code != cli.ExitUsage || ended.Failure == nil || !strings.Contains(ended.Failure.Error(), "ONESIE_MOCK") {
			t.Errorf("run = %d, %v, want exit 2 naming ONESIE_MOCK", code, ended.Failure)
		}
	})

	t.Run("should refuse to run without a key", func(t *testing.T) {
		t.Parallel()

		code, ended := run(t.Context(), options{
			lookup: envOf(nil), dir: t.TempDir(), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		})
		if code != cli.ExitUsage || ended.Failure == nil || !strings.Contains(ended.Failure.Error(), keyName) {
			t.Errorf("run = %d, %v, want exit 2 naming %s", code, ended.Failure, keyName)
		}
	})

	t.Run("should read only ONESIE_MOCK and the key from the environment", func(t *testing.T) {
		t.Parallel()

		dir := starterDir(t)
		stub := newStub(t)

		var mu sync.Mutex
		var asked []string

		lookup := func(name string) (string, bool) {
			mu.Lock()
			defer mu.Unlock()

			asked = append(asked, name)

			return envOf(map[string]string{
				keyName: "k", "ONESIE_PROVIDER": "openrouter", "ONESIE_CONFIG_DIR": t.TempDir(),
			})(name)
		}

		code, ended := runStubbed(t, dir, stub, lookup)
		if code != cli.ExitOK || ended.Report() != nil {
			t.Fatalf("run = %d, %v", code, ended.Report())
		}

		slices.Sort(asked)
		if want := []string{"ONESIE_MOCK", keyName}; !slices.Equal(slices.Compact(asked), want) {
			t.Errorf("run read %v from the environment, want only %v", asked, want)
		}
	})

	t.Run("should start a stale set afresh and resume a set whose fingerprint matches", func(t *testing.T) {
		t.Parallel()

		dir := starterDir(t)
		stub := newStub(t)
		lookup := envOf(map[string]string{keyName: "k"})

		if code, ended := runStubbed(t, dir, stub, lookup); code != cli.ExitOK || ended.Report() != nil {
			t.Fatalf("first run = %d, %v", code, ended.Report())
		}

		if got := stub.requests.Load(); got != 4 {
			t.Fatalf("first run made %d requests, want 4", got)
		}

		question := filepath.Join(dir, "questions", "alpha.yaml")
		if err := os.WriteFile(question, []byte("u:\n  ask: is it urgent now\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		stub.requests.Store(0)

		if code, ended := runStubbed(t, dir, stub, lookup); code != cli.ExitOK || ended.Report() != nil {
			t.Fatalf("second run = %d, %v", code, ended.Report())
		}

		if got := stub.requests.Load(); got != 2 {
			t.Errorf("second run made %d requests, want the 2 of the stale set alone", got)
		}

		answers, err := os.ReadFile(filepath.Join(dir, "data", "alpha.answers.jsonl"))
		if err != nil || strings.Count(string(answers), "\n") != 2 {
			t.Errorf("alpha answers = %q, %v, want 2 lines", answers, err)
		}
	})
}

func TestSettled(t *testing.T) {
	t.Parallel()

	failure := errors.New("alpha exited 6")
	removal := errors.New("removing the scratch dir: busy")

	tests := []struct {
		name    string
		failure error
		removal error
		want    []string
	}{
		{name: "should report nothing when the run and the removal succeeded"},
		{name: "should report the run's failure alone when the removal succeeded", failure: failure, want: []string{"alpha exited 6"}},
		{name: "should make the removal failure the failure when the run succeeded", removal: removal, want: []string{"removing the scratch dir: busy"}},
		{
			name: "should report a removal failure apart from the run's failure, after it", failure: failure, removal: removal,
			want: []string{"alpha exited 6", "removing the scratch dir: busy"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := settled(tc.failure, tc.removal).Report(); !slices.Equal(got, tc.want) {
				t.Errorf("settled reports %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCalibrateArgs(t *testing.T) {
	t.Parallel()

	t.Run("should turn the cache off, so every answer comes from the API", func(t *testing.T) {
		t.Parallel()

		sets, err := examples.Sets(starterDir(t))
		if err != nil || len(sets) == 0 {
			t.Fatalf("Sets = %v, %v", sets, err)
		}

		if args := calibrateArgs(sets[0], "examples"); !slices.Contains(args, "--cache=false") {
			t.Errorf("calibrateArgs = %v, want --cache=false", args)
		}
	})
}

func TestNoKeychain(t *testing.T) {
	t.Parallel()

	t.Run("should refuse every keychain call", func(t *testing.T) {
		t.Parallel()

		keychain := noKeychain{}

		_, getErr := keychain.Get("onesie", "typesafe")
		if getErr == nil || keychain.Set("onesie", "typesafe", "k") == nil || keychain.Delete("onesie", "typesafe") == nil {
			t.Error("a noKeychain call succeeded")
		}
	})
}

func runStubbed(t *testing.T, dir string, stub *stubAPI, lookup func(string) (string, bool)) (int, cli.Outcome) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	code, ended := run(t.Context(), options{
		lookup: lookup, dir: dir, stdout: &stdout, stderr: &stderr,
		root: []cli.RootOption{cli.WithClientFactory(stub.factory)},
	})
	if code != cli.ExitOK {
		t.Logf("stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	return code, ended
}

func starterDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	files := map[string]string{
		"questions/alpha.yaml": "u:\n  ask: is it urgent\n",
		"questions/beta.yaml":  "v:\n  ask: is it spam\n",
		"data/alpha.jsonl":     "{\"id\":\"a1\",\"text\":\"x\",\"u\":true}\n{\"id\":\"a2\",\"text\":\"y\",\"u\":false}\n",
		"data/beta.jsonl":      "{\"id\":\"b1\",\"text\":\"x\",\"v\":true}\n{\"id\":\"b2\",\"text\":\"y\",\"v\":false}\n",
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func newStub(t *testing.T) *stubAPI {
	t.Helper()

	stub := &stubAPI{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.requests.Add(1)

		var body struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("stub could not read the request: %v", err)
		}

		answers := map[string]any{}
		for id := range body.Questions {
			answers[id] = map[string]any{"type": "noul", "noul": 0.9}
		}

		if err := json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0", "answers": answers}); err != nil {
			t.Errorf("stub could not answer: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	stub.factory = func(_ context.Context, extra ...onesie.Option) (*onesie.Client, error) {
		return onesie.New(append([]onesie.Option{
			onesie.WithAPIKey("stub"),
			onesie.WithBaseURL(server.URL),
			onesie.WithEnv(func(string) (string, bool) { return "", false }),
		}, extra...)...)
	}

	return stub
}

type stubAPI struct {
	requests atomic.Int64
	factory  func(context.Context, ...onesie.Option) (*onesie.Client, error)
}

func envOf(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := values[name]

		return value, found
	}
}
