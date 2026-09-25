package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const noKeyCacheSentence = "The cache needs the key too, to find the API address it keys answers by"

var cacheEpoch = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func TestCaching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		args    []string
		want    bool
		wantErr string
	}{
		{name: "should be on under ONESIE_CACHE=1", env: map[string]string{envCache: "1"}, want: true},
		{name: "should be on under ONESIE_CACHE=true", env: map[string]string{envCache: "true"}, want: true},
		{name: "should be off under ONESIE_CACHE=0", env: map[string]string{envCache: "0"}},
		{name: "should be off under ONESIE_CACHE=false", env: map[string]string{envCache: "false"}},
		{name: "should be off under an empty ONESIE_CACHE", env: map[string]string{envCache: ""}},
		{name: "should be off with no variable and no flag"},
		{name: "should be on under --cache", args: []string{"--cache"}, want: true},
		{name: "should let --cache=false win over ONESIE_CACHE=1", env: map[string]string{envCache: "1"}, args: []string{"--cache=false"}},
		{name: "should let --cache win over ONESIE_CACHE=0", env: map[string]string{envCache: "0"}, args: []string{"--cache"}, want: true},
		{
			name: "should refuse a value the flag would not take, naming the variable",
			env:  map[string]string{envCache: "yes"}, wantErr: "ONESIE_CACHE=yes",
		},
		{name: "should be off under --mock", args: []string{"--cache", "--mock", "m.json"}},
		{name: "should be off under ONESIE_MOCK", env: map[string]string{envCache: "1", envMock: "m.json"}},
		{name: "should be off under ONESIE_MOCK before it reads a bad ONESIE_CACHE", env: map[string]string{envCache: "yes", envMock: "m.json"}},
		{name: "should be off under --print-request", args: []string{"--cache", "--print-request"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flags := &runFlags{}
			cmd := &cobra.Command{Use: "onesie"}
			cmd.Flags().BoolVar(&flags.cache, "cache", false, "")
			cmd.Flags().StringVar(&flags.mock, "mock", "", "")
			cmd.Flags().BoolVar(&flags.printRequest, "print-request", false, "")

			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatal(err)
			}

			got, err := caching(cmd, rootSettings{lookupEnv: lookupFrom(tc.env)}, flags)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || Classify(err) != ExitUsage {
					t.Fatalf("caching = %v, %v, want a usage error naming %q", got, err, tc.wantErr)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Errorf("caching = %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestOpenCache(t *testing.T) {
	t.Parallel()

	settingsAt := func(t *testing.T, env map[string]string, goos string, now func() time.Time) (rootSettings, string) {
		t.Helper()

		dir := filepath.Join(t.TempDir(), "cache")
		all := map[string]string{"ONESIE_CACHE_DIR": dir}
		maps.Copy(all, env)

		return rootSettings{lookupEnv: lookupFrom(all), homeDir: os.UserHomeDir, goos: goos, now: now}, dir
	}

	t.Run("should pass settings.goos to cache.Open", func(t *testing.T) {
		t.Parallel()

		for goos, wantErr := range map[string]bool{"windows": false, "linux": true} {
			settings, dir := settingsAt(t, nil, goos, time.Now)
			mustMkdirMode(t, dir, 0o755)

			_, err := openCache(settings)
			if (err != nil) != wantErr {
				t.Errorf("openCache on %s = %v, want an error %v", goos, err, wantErr)
			}
		}
	})

	t.Run("should refuse only a readable directory and a bad lifetime, and pass every other error on", func(t *testing.T) {
		t.Parallel()

		var refused *cacheRefusedError

		settings, dir := settingsAt(t, nil, "linux", time.Now)
		mustMkdirMode(t, dir, 0o755)

		if _, err := openCache(settings); !errors.As(err, &refused) {
			t.Errorf("openCache on mode 755 = %v, want a refusal", err)
		}

		settings, _ = settingsAt(t, map[string]string{envCacheTTL: "soon"}, "linux", time.Now)
		if _, err := openCache(settings); !errors.As(err, &refused) {
			t.Errorf("openCache under a bad lifetime = %v, want a refusal", err)
		}

		settings, dir = settingsAt(t, nil, "linux", time.Now)
		if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		if _, err := openCache(settings); err == nil || errors.As(err, &refused) {
			t.Errorf("openCache on a file = %v, want an error that is not a refusal", err)
		}
	})

	t.Run("should read ONESIE_CACHE_TTL for alias entries", func(t *testing.T) {
		t.Parallel()

		clock := cacheEpoch
		settings, _ := settingsAt(t, map[string]string{envCacheTTL: "90m"}, "linux", func() time.Time { return clock })

		store, err := openCache(settings)
		if err != nil {
			t.Fatal(err)
		}

		if err := store.Put(cache.Key{1}, "typesafe", "jev-latest", []byte(`1`)); err != nil {
			t.Fatal(err)
		}

		clock = cacheEpoch.Add(91 * time.Minute)

		if _, hit, err := store.Get(cache.Key{1}, "typesafe", "jev-latest"); hit || err != nil {
			t.Errorf("Get at 91 minutes = %v, %v, want a miss", hit, err)
		}
	})

	t.Run("should neither store nor read an alias entry under ONESIE_CACHE_TTL=0", func(t *testing.T) {
		t.Parallel()

		settings, dir := settingsAt(t, map[string]string{envCacheTTL: "0"}, "linux", time.Now)

		store, err := openCache(settings)
		if err != nil {
			t.Fatal(err)
		}

		if err := store.Put(cache.Key{1}, "typesafe", "jev-latest", []byte(`1`)); err != nil {
			t.Fatal(err)
		}

		if summary, _ := cache.Summarize(dir, nil); summary.Entries != 0 {
			t.Errorf("Summarize = %+v, want no entry", summary)
		}
	})

	for _, value := range []string{"-1h", "soon"} {
		t.Run("should exit 2 for ONESIE_CACHE_TTL="+value, func(t *testing.T) {
			t.Parallel()

			settings, _ := settingsAt(t, map[string]string{envCacheTTL: value}, "linux", time.Now)

			_, err := openCache(settings)
			if err == nil || !strings.Contains(err.Error(), "ONESIE_CACHE_TTL="+value) || Classify(err) != ExitUsage {
				t.Errorf("openCache = %v, want a usage error naming ONESIE_CACHE_TTL", err)
			}
		})
	}
}

func TestCachedAnswerer(t *testing.T) {
	t.Parallel()

	questions := []plan.Question{{ID: "u", Shape: plan.Noul}}
	request := jev.Request{State: json.RawMessage(`"site down"`), Questions: wireAll(questions)}
	key := recordKey{position: 1, line: 1}

	build := func(t *testing.T, stub *answerStub, warn func(error), extra ...jev.Option) (answererFactory, string) {
		t.Helper()

		dir := filepath.Join(t.TempDir(), "cache")
		base := stubFactory(stub.url)
		settings := rootSettings{
			lookupEnv: lookupFrom(map[string]string{"ONESIE_CACHE_DIR": dir}),
			homeDir:   os.UserHomeDir,
			goos:      runtime.GOOS,
			now:       time.Now,
			newClient: func(ctx context.Context, opts ...jev.Option) (*jev.Client, error) {
				return base(ctx, append(opts, extra...)...)
			},
		}

		if warn == nil {
			warn = func(err error) { t.Errorf("unexpected cache warning: %v", err) }
		}

		return cachedAnswers(settings, &runFlags{}, "typesafe", jev.DefaultModel, questions, warn), dir
	}

	t.Run("should ask on a miss, store the answer and answer the next call from it", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)
		factory, _ := build(t, stub, nil)

		asker, err := factory(t.Context(), &collector{})
		if err != nil {
			t.Fatal(err)
		}

		first, err := asker.answer(t.Context(), key, request)
		if err != nil || first.cached {
			t.Fatalf("first answer = %+v, %v, want a live answer", first, err)
		}

		second, err := asker.answer(t.Context(), key, request)
		if err != nil || !second.cached {
			t.Fatalf("second answer = %+v, %v, want a cached answer", second, err)
		}

		if got := stub.requests.Load(); got != 1 {
			t.Errorf("%d requests, want 1", got)
		}

		if second.result.Usage != (jev.Usage{}) || second.result.RequestID != "" {
			t.Errorf("cached result usage %+v, request id %q, want zero", second.result.Usage, second.result.RequestID)
		}

		if _, noulErr := second.result.Noul("u"); noulErr != nil {
			t.Errorf("cached result: %v", noulErr)
		}
	})

	t.Run("should key the entry by the body sent, the provider and the base URL", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)
		factory, dir := build(t, stub, nil)

		asker, err := factory(t.Context(), &collector{})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := asker.answer(t.Context(), key, request); err != nil {
			t.Fatal(err)
		}

		sum := sha256.Sum256(append(stub.body(0), []byte("typesafe\x00"+stub.url)...))
		name := hex.EncodeToString(sum[:])

		if _, err := os.Stat(filepath.Join(dir, name[:2], name+".alias.json")); err != nil {
			t.Errorf("no entry under the key the request implies: %v", err)
		}
	})

	failures := []struct {
		name  string
		setup func(stub *answerStub)
		opts  []jev.Option
	}{
		{name: "should not store a 503", setup: func(s *answerStub) { s.status.Store(http.StatusServiceUnavailable) }},
		{
			name: "should not store a timeout", setup: func(s *answerStub) { s.delay.Store(int64(time.Second)) },
			opts: []jev.Option{jev.WithAttemptTimeout(50 * time.Millisecond)},
		},
		{name: "should not store a 200 missing a question", setup: func(s *answerStub) { s.missing.Store(true) }},
		{name: "should not store a 200 of the wrong answer type", setup: func(s *answerStub) { s.wrongType.Store(true) }},
	}

	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stub := newAnswerStub(t)
			tc.setup(stub)
			factory, dir := build(t, stub, nil, tc.opts...)

			asker, err := factory(t.Context(), &collector{})
			if err != nil {
				t.Fatal(err)
			}

			for range 2 {
				if got, _ := asker.answer(t.Context(), key, request); got.cached {
					t.Fatalf("answer = %+v, want no cached answer", got)
				}
			}

			if summary, _ := cache.Summarize(dir, nil); summary.Entries != 0 {
				t.Errorf("Summarize = %+v, want nothing stored", summary)
			}

			if got := stub.requests.Load(); got != 2 {
				t.Errorf("%d requests, want 2, since nothing was stored", got)
			}
		})
	}

	t.Run("should warn once and ask the API when the cache cannot open", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)

		var (
			mu       sync.Mutex
			warnings []string
		)

		factory, dir := build(t, stub, func(err error) {
			mu.Lock()
			defer mu.Unlock()

			warnings = append(warnings, err.Error())
		})

		if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}

		asker, err := factory(t.Context(), &collector{})
		if err != nil {
			t.Fatal(err)
		}

		for range 2 {
			if got, answerErr := asker.answer(t.Context(), key, request); answerErr != nil || got.cached || got.result == nil {
				t.Fatalf("answer = %+v, %v, want a live answer", got, answerErr)
			}
		}

		mu.Lock()
		defer mu.Unlock()

		if len(warnings) != 1 || !strings.Contains(warnings[0], "for the rest of this run") || !strings.Contains(warnings[0], dir) {
			t.Errorf("warnings = %q, want one naming the directory and the rest of this run", warnings)
		}
	})

	t.Run("should not open the store until the first answer", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)
		factory, dir := build(t, stub, nil)

		if _, err := factory(t.Context(), &collector{}); err != nil {
			t.Fatal(err)
		}

		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the cache dir exists before any answer: %v", err)
		}
	})

	t.Run("should warn once and ask the API for the rest of the run after a Put error", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)

		var (
			mu       sync.Mutex
			warnings []string
		)

		factory, dir := build(t, stub, func(err error) {
			mu.Lock()
			defer mu.Unlock()

			warnings = append(warnings, err.Error())
		})

		asker, factoryErr := factory(t.Context(), &collector{})
		if factoryErr != nil {
			t.Fatal(factoryErr)
		}

		// The key is only known from the body the first request sends, so a first answer finds it,
		// and the entry it stored is swapped for a directory the next Put cannot rename over.
		if _, err := asker.answer(t.Context(), key, request); err != nil {
			t.Fatal(err)
		}

		sum := sha256.Sum256(append(stub.body(0), []byte("typesafe\x00"+stub.url)...))
		name := hex.EncodeToString(sum[:])
		entry := filepath.Join(dir, name[:2], name+".alias.json")

		if err := os.Remove(entry); err != nil {
			t.Fatal(err)
		}

		mustMkdirMode(t, entry, 0o700)

		if err := os.WriteFile(filepath.Join(entry, "inside"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		got, answerErr := asker.answer(t.Context(), key, request)
		if answerErr != nil || got.result == nil || got.cached {
			t.Fatalf("answer beside the failing Put = %+v, %v, want the live answer", got, answerErr)
		}

		if err := os.RemoveAll(entry); err != nil {
			t.Fatal(err)
		}

		if _, err := asker.answer(t.Context(), key, request); err != nil {
			t.Fatal(err)
		}

		if got := stub.requests.Load(); got != 3 {
			t.Errorf("%d requests, want 3, since the cache is off after the failure", got)
		}

		if _, err := os.Stat(entry); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("an answer after the failure was stored: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()

		if len(warnings) != 1 || !strings.Contains(warnings[0], "for the rest of this run") || !strings.Contains(warnings[0], dir) {
			t.Errorf("warnings = %q, want one naming the directory and the rest of this run", warnings)
		}
	})
}

func TestAsk(t *testing.T) {
	t.Parallel()

	questionSets := map[string][]string{
		"yes/no": {"--ask", "u=is it urgent"},
		"pick":   {"--ask", "t=which team", "--pick", "billing,platform"},
		"rate":   {"--ask", "r=how rude", "--rate", "calm,curt,rude"},
	}

	for _, mode := range []string{"json", "values", "table", "raw", "csv", "tsv", "markdown"} {
		for shape, asked := range questionSets {
			t.Run("should print the same "+mode+" output for a cached "+shape+" answer", func(t *testing.T) {
				t.Parallel()

				stub := newAnswerStub(t)
				env := cacheEnv(t)
				args := append(append([]string{"--cache", "--base-url", stub.url, "-o", mode}, asked...), "--state", "site down")

				first, errOut, code := runCached(t, env, args, "")
				if code != ExitOK {
					t.Fatalf("first run exit %d\n%s", code, errOut)
				}

				second, errOut, code := runCached(t, env, args, "")
				if code != ExitOK || second != first {
					t.Fatalf("second run exit %d, stdout %q, want %q\n%s", code, second, first, errOut)
				}

				if got := stub.requests.Load(); got != 1 {
					t.Errorf("%d requests, want 1", got)
				}
			})
		}
	}

	base := []string{"--cache", "--ask", "u=is it urgent", "--state", "site down"}

	keyed := []struct {
		name   string
		second []string // Replaces base on the second run when set.
		change []string
		other  bool // Asks through a second stub, so the base URL differs.
		hit    bool
	}{
		{name: "should miss when the state changes", second: []string{"--cache", "--ask", "u=is it urgent", "--state", "site up"}},
		{name: "should miss when a question changes", second: []string{"--cache", "--ask", "u=is it on fire", "--state", "site down"}},
		{name: "should miss when -m changes", change: []string{"-m", "jev-1.13.0"}},
		{name: "should miss when --provider changes", change: []string{"--provider", "openrouter"}},
		{name: "should miss when --base-url changes", other: true},
		{name: "should hit when --timeout changes", change: []string{"--timeout", "5"}, hit: true},
		{name: "should hit when --retries changes", change: []string{"--retries", "1"}, hit: true},
		{name: "should hit when --assert changes", change: []string{"--assert", "u.value > 0.5"}, hit: true},
		{name: "should hit when -o changes", change: []string{"-o", "json"}, hit: true},
		{name: "should hit when --usage changes", change: []string{"--usage", "-o", "json"}, hit: true},
	}

	for _, tc := range keyed {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stub, other := newAnswerStub(t), newAnswerStub(t)
			env := cacheEnv(t)

			if _, errOut, code := runCached(t, env, append(slicesOf(base), "--base-url", stub.url), ""); code != ExitOK {
				t.Fatalf("first run exit %d\n%s", code, errOut)
			}

			url := stub.url
			if tc.other {
				url = other.url
			}

			second := slicesOf(base)
			if tc.second != nil {
				second = slicesOf(tc.second)
			}

			args := append(append(second, "--base-url", url), tc.change...)
			if _, errOut, code := runCached(t, env, args, ""); code != ExitOK {
				t.Fatalf("second run exit %d\n%s", code, errOut)
			}

			want := int32(2)
			if tc.hit {
				want = 1
			}

			if got := stub.requests.Load() + other.requests.Load(); got != want {
				t.Errorf("%d requests, want %d", got, want)
			}
		})
	}

	t.Run("should report zero usage and no cost on a hit", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)
		stub.cost.Store(true)
		env := cacheEnv(t)
		args := append(slicesOf(base), "--base-url", stub.url, "--provider", "openrouter", "--usage", "-o", "json")

		first, _, _ := runCached(t, env, args, "")
		if !strings.Contains(first, `"cost":0.5`) {
			t.Fatalf("first run = %s, want the stub's cost", first)
		}

		second, errOut, code := runCached(t, env, args, "")
		if code != ExitOK || !strings.Contains(second, `"usage":{"input_tokens":0,"output_tokens":0}`) || strings.Contains(second, "cost") {
			t.Errorf("second run exit %d = %s, want zero usage and no cost\n%s", code, second, errOut)
		}
	})

	t.Run("should count a hit as a cached record and not a request under --stats", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)
		env := cacheEnv(t)
		args := append(slicesOf(base), "--base-url", stub.url, "--stats")

		runCached(t, env, args, "")

		_, errOut, code := runCached(t, env, args, "")
		if code != ExitOK || !strings.Contains(errOut, "1 record, 1 cached, 0 requests, 0 questions, 0 in / 0 out, model onesie-1.13.0") {
			t.Errorf("exit %d, stderr %q, want a cached record", code, errOut)
		}
	})

	lifetimes := []struct {
		name  string
		extra []string
		env   map[string]string
		after time.Duration
		hit   bool
	}{
		{name: "should hit jev-latest 23 hours later", after: 23 * time.Hour, hit: true},
		{name: "should ask jev-latest again 24 hours later", after: 24 * time.Hour},
		{name: "should ask jev-latest again 90 minutes later under ONESIE_CACHE_TTL=90m", after: 90 * time.Minute, env: map[string]string{envCacheTTL: "90m"}},
		{name: "should hit a pinned jev-1.13.0 on typesafe a year later", extra: []string{"-m", "jev-1.13.0"}, after: 365 * 24 * time.Hour, hit: true},
		{name: "should ask jev-1.13.0 on openrouter again 24 hours later", extra: []string{"-m", "jev-1.13.0", "--provider", "openrouter"}, after: 24 * time.Hour},
	}

	for _, tc := range lifetimes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stub := newAnswerStub(t)
			env := cacheEnv(t)
			maps.Copy(env, tc.env)
			args := append(append(slicesOf(base), "--base-url", stub.url), tc.extra...)

			if _, errOut, code := runCached(t, env, args, "", WithNow(fixedNow(cacheEpoch))); code != ExitOK {
				t.Fatalf("first run exit %d\n%s", code, errOut)
			}

			if _, errOut, code := runCached(t, env, args, "", WithNow(fixedNow(cacheEpoch.Add(tc.after)))); code != ExitOK {
				t.Fatalf("second run exit %d\n%s", code, errOut)
			}

			want := int32(2)
			if tc.hit {
				want = 1
			}

			if got := stub.requests.Load(); got != want {
				t.Errorf("%d requests, want %d", got, want)
			}
		})
	}

	t.Run("should need a key even when every answer is cached", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)
		env := cacheEnv(t)
		args := append(slicesOf(base), "--base-url", stub.url)

		runCached(t, env, args, "")
		delete(env, jev.EnvAPIKey)

		_, errOut, code := runCached(t, env, args, "")
		if code != ExitUsage || !strings.Contains(errOut, "onesie: no API key") || !strings.Contains(errOut, noKeyCacheSentence) {
			t.Errorf("exit %d, stderr %q, want the no key message with the cache sentence", code, errOut)
		}
	})

	skipping := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "should answer from --mock and leave the cache dir absent", args: []string{"--cache", "--mock", "MOCK"}},
		{name: "should answer from ONESIE_MOCK under ONESIE_CACHE=1 and leave the cache dir absent", env: map[string]string{envCache: "1", envMock: "MOCK"}},
		{name: "should leave the cache dir absent under --print-request --cache", args: []string{"--cache", "--print-request"}},
	}

	for _, tc := range skipping {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stub := newAnswerStub(t)
			env := cacheEnv(t)
			mockFile := filepath.Join(t.TempDir(), "answers.json")

			if err := os.WriteFile(mockFile, []byte(`{"u":0.93}`), 0o600); err != nil {
				t.Fatal(err)
			}

			for name, value := range tc.env {
				env[name] = strings.ReplaceAll(value, "MOCK", mockFile)
			}

			args := []string{"--ask", "u=is it urgent", "--state", "site down", "--base-url", stub.url}
			for _, arg := range tc.args {
				args = append(args, strings.ReplaceAll(arg, "MOCK", mockFile))
			}

			if _, errOut, code := runCached(t, env, args, ""); code != ExitOK {
				t.Fatalf("exit %d\n%s", code, errOut)
			}

			if _, err := os.Stat(env["ONESIE_CACHE_DIR"]); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the cache dir exists: %v", err)
			}

			if got := stub.requests.Load(); got != 0 {
				t.Errorf("%d requests, want none", got)
			}
		})
	}

	t.Run("should warn once and exit 0 under ONESIE_CACHE=1 with a home onesie cannot write", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)
		env := cacheEnv(t)
		delete(env, "ONESIE_CACHE_DIR")
		env[envCache] = "1"

		home := filepath.Join(t.TempDir(), "home")
		mustMkdirMode(t, home, 0o500)
		t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
		env["HOME"] = home

		out, errOut, code := runCached(t, env, []string{"is it urgent", "-i", "lines", "--base-url", stub.url}, "one\ntwo\n")
		if code != ExitOK || strings.Count(out, "\n") != 2 {
			t.Fatalf("exit %d, stdout %q, want two answers\n%s", code, out, errOut)
		}

		if strings.Count(errOut, "warning: ") != 1 || !strings.Contains(errOut, "for the rest of this run") {
			t.Errorf("stderr = %q, want one cache warning", errOut)
		}

		if got := stub.requests.Load(); got != 2 {
			t.Errorf("%d requests, want 2", got)
		}
	})

	t.Run("should exit 2 before any request for a cache dir others can read", func(t *testing.T) {
		t.Parallel()

		stub := newAnswerStub(t)
		env := cacheEnv(t)
		mustMkdirMode(t, env["ONESIE_CACHE_DIR"], 0o755)

		_, errOut, code := runCached(t, env, append(slicesOf(base), "--base-url", stub.url), "")
		if code != ExitUsage || !strings.Contains(errOut, env["ONESIE_CACHE_DIR"]) || !strings.Contains(errOut, "chmod 700") {
			t.Errorf("exit %d, stderr %q, want exit 2 naming the directory", code, errOut)
		}

		if got := stub.requests.Load(); got != 0 {
			t.Errorf("%d requests, want none", got)
		}
	})
}

func newAnswerStub(t *testing.T) *answerStub {
	t.Helper()

	stub := &answerStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("stub could not read the request: %v", err)
		}

		stub.requests.Add(1)
		stub.mu.Lock()
		stub.bodies = append(stub.bodies, body)
		stub.mu.Unlock()

		if status := stub.status.Load(); status != 0 {
			w.WriteHeader(int(status))
			_, _ = w.Write([]byte(`{"error":{"message":"stub"}}`))

			return
		}

		if delay := stub.delay.Load(); delay != 0 {
			select {
			case <-time.After(time.Duration(delay)):
			case <-r.Context().Done():
				return
			}
		}

		if _, err := w.Write(stub.response(t, body)); err != nil {
			t.Errorf("writing stub response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	stub.url = srv.URL

	return stub
}

func (s *answerStub) response(t *testing.T, body []byte) []byte {
	var sent struct {
		Questions map[string]struct {
			Type string `json:"type"`
		} `json:"questions"`
	}

	if err := json.Unmarshal(body, &sent); err != nil {
		t.Errorf("stub could not decode the request: %v", err)
	}

	canned := map[string]string{
		"noul":   `{"type":"noul","noul":0.93}`,
		"choice": `{"type":"choice","choice":"platform","confidence":0.8,"probabilities":{"billing":0.2,"platform":0.8}}`,
		"score": `{"type":"score","score":1.6,"confidence":0.7,"probabilities":{"0":0.1,"1":0.2,"2":0.7},` +
			`"legend":{"0":"calm","1":"curt","2":"rude"}}`,
	}

	answers := map[string]json.RawMessage{}

	for id, question := range sent.Questions {
		kind := question.Type
		if s.wrongType.Load() && kind == "noul" {
			kind = "choice"
		}

		if !s.missing.Load() {
			answers[id] = json.RawMessage(canned[kind])
		}
	}

	encoded, err := json.Marshal(answers)
	if err != nil {
		t.Errorf("stub could not encode its answers: %v", err)
	}

	usage := `{"input_tokens":7,"output_tokens":3}`
	if s.cost.Load() {
		usage = `{"input_tokens":7,"output_tokens":3,"cost":0.5}`
	}

	return fmt.Appendf(nil, `{"model":"onesie-1.13.0","answers":%s,"usage":%s}`, encoded, usage)
}

func (s *answerStub) body(n int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.bodies[n]
}

type answerStub struct {
	url       string
	requests  atomic.Int32
	status    atomic.Int32
	delay     atomic.Int64
	missing   atomic.Bool
	wrongType atomic.Bool
	cost      atomic.Bool

	mu     sync.Mutex
	bodies [][]byte
}

func cacheEnv(t *testing.T) map[string]string {
	t.Helper()

	home := t.TempDir()

	return map[string]string{
		"HOME":               home,
		"ONESIE_CONFIG_DIR":  filepath.Join(home, "config"),
		"ONESIE_CACHE_DIR":   filepath.Join(home, "cache"),
		jev.EnvAPIKey:        "k",
		"OPENROUTER_API_KEY": "k",
	}
}

func runCached(t *testing.T, env map[string]string, args []string, stdin string, extra ...RootOption) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	// No client factory, so the real one resolves the provider, the key and the base URL the cache
	// keys answers by.
	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append([]RootOption{
		WithKeychain(noKeychain()),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(maps.Clone(env))),
		WithHomeDir(func() (string, error) { return env["HOME"], nil }),
		WithTerminalWidth(func() (int, bool) { return 100, true }),
	}, extra...)...)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

func slicesOf(args []string) []string {
	return append([]string{}, args...)
}

func fixedNow(when time.Time) func() time.Time {
	return func() time.Time { return when }
}

func mustMkdirMode(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
}
