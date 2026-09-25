package cache_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/cache"
)

const tagSignature = "Signature: 8a477f597d28d172789f06886806bc55"

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func TestOpen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    os.FileMode // Zero leaves the directory for Open to create.
		goos    string
		wantErr bool
	}{
		{name: "should create a missing directory with mode 700 and a CACHEDIR.TAG", goos: "darwin"},
		{name: "should accept an existing directory with mode 700", mode: 0o700, goos: "linux"},
		{name: "should refuse a directory with mode 755", mode: 0o755, goos: "darwin", wantErr: true},
		{name: "should refuse a directory with mode 770", mode: 0o770, goos: "linux", wantErr: true},
		{name: "should accept any mode on windows", mode: 0o755, goos: "windows"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "nested", "onesie")
			if tc.mode != 0 {
				mustMkdir(t, dir, tc.mode)
			}

			_, err := cache.Open(dir, cache.Options{Now: fixed(t0), GOOS: tc.goos})

			if tc.wantErr {
				want := fmt.Sprintf("onesie: the cache dir %s has mode %o, so others can read cached answers. Run chmod 700 %s",
					dir, tc.mode, dir)
				if err == nil || err.Error() != want {
					t.Fatalf("Open error = %v, want %q", err, want)
				}

				return
			}

			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			if tc.mode != 0 {
				return
			}

			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}

			if got := info.Mode().Perm(); got != 0o700 {
				t.Errorf("created directory mode = %o, want 700", got)
			}

			tag, err := os.ReadFile(filepath.Join(dir, "CACHEDIR.TAG"))
			if err != nil {
				t.Fatalf("reading CACHEDIR.TAG: %v", err)
			}

			if !strings.HasPrefix(string(tag), tagSignature+"\n") || !strings.Contains(string(tag), "onesie") {
				t.Errorf("CACHEDIR.TAG = %q, want the signature line and a line naming onesie", tag)
			}
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	t.Run("should return a stored value byte for byte and miss another key", func(t *testing.T) {
		t.Parallel()

		store, _ := openAt(t, fixed(t0), cache.Options{})
		value := []byte(`{"a": [1, 2.50],  "b":"x"}`)

		mustPut(t, store, key(0xab, 1), "typesafe", "jev-1.13.0", value)

		got, hit, err := store.Get(key(0xab, 1), "typesafe", "jev-1.13.0")
		if err != nil || !hit || !bytes.Equal(got, value) {
			t.Fatalf("Get = %q, %v, %v, want %q", got, hit, err, value)
		}

		if _, hit, err := store.Get(key(0xab, 2), "typesafe", "jev-1.13.0"); hit || err != nil {
			t.Errorf("Get of another key = %v, %v, want a miss", hit, err)
		}
	})

	t.Run("should set the entry's mtime to Now on a hit", func(t *testing.T) {
		t.Parallel()

		clock := newClock(t0)
		store, dir := openAt(t, clock.now, cache.Options{})

		mustPut(t, store, key(0xab, 1), "typesafe", "jev-latest", []byte(`1`))
		clock.set(t0.Add(3 * time.Hour))

		if _, hit, err := store.Get(key(0xab, 1), "typesafe", "jev-latest"); !hit || err != nil {
			t.Fatalf("Get = %v, %v, want a hit", hit, err)
		}

		if got := mtime(t, entryPath(dir, key(0xab, 1), true)); !got.Equal(t0.Add(3 * time.Hour)) {
			t.Errorf("mtime = %v, want %v", got, t0.Add(3*time.Hour))
		}
	})

	lifetimes := []struct {
		name  string
		model string
		ttl   time.Duration
		age   time.Duration
		hit   bool
	}{
		{name: "should hit a pinned entry a year after it was stored", model: "jev-1.13.0", age: 365 * 24 * time.Hour, hit: true},
		{name: "should hit an alias entry at 23 hours", model: "jev-latest", age: 23 * time.Hour, hit: true},
		{name: "should miss an alias entry at 25 hours and remove it", model: "jev-latest", age: 25 * time.Hour},
		{name: "should hit an alias entry at 89 minutes under a 90 minute TTL", model: "jev-latest", ttl: 90 * time.Minute, age: 89 * time.Minute, hit: true},
		{name: "should miss an alias entry at 91 minutes under a 90 minute TTL", model: "jev-latest", ttl: 90 * time.Minute, age: 91 * time.Minute},
		{name: "should hit a pinned entry a year on under a 90 minute TTL", model: "jev-1.13.0", ttl: 90 * time.Minute, age: 365 * 24 * time.Hour, hit: true},
	}

	for _, tc := range lifetimes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clock := newClock(t0)
			store, dir := openAt(t, clock.now, cache.Options{AliasTTL: tc.ttl})

			mustPut(t, store, key(0xcd, 1), "typesafe", tc.model, []byte(`{"x":1}`))
			clock.set(t0.Add(tc.age))

			_, hit, err := store.Get(key(0xcd, 1), "typesafe", tc.model)
			if err != nil || hit != tc.hit {
				t.Fatalf("Get = %v, %v, want hit %v", hit, err, tc.hit)
			}

			_, statErr := os.Stat(entryPath(dir, key(0xcd, 1), !cache.Pinned("typesafe", tc.model)))
			if exists := statErr == nil; exists != tc.hit {
				t.Errorf("entry file exists = %v after the Get, want %v", exists, tc.hit)
			}
		})
	}

	t.Run("should neither store nor read an alias entry under NoAliasTTL, and keep a pinned one", func(t *testing.T) {
		t.Parallel()

		store, dir := openAt(t, fixed(t0), cache.Options{AliasTTL: cache.NoAliasTTL})

		mustPut(t, store, key(0xab, 1), "typesafe", "jev-latest", []byte(`1`))
		mustPut(t, store, key(0xab, 2), "typesafe", "jev-1.13.0", []byte(`2`))

		if _, err := os.Stat(entryPath(dir, key(0xab, 1), true)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("alias entry stat = %v, want it absent", err)
		}

		if _, hit, err := store.Get(key(0xab, 1), "typesafe", "jev-latest"); hit || err != nil {
			t.Errorf("alias Get = %v, %v, want a miss", hit, err)
		}

		if _, hit, err := store.Get(key(0xab, 2), "typesafe", "jev-1.13.0"); !hit || err != nil {
			t.Errorf("pinned Get = %v, %v, want a hit", hit, err)
		}
	})

	broken := []struct {
		name  string
		plant func(t *testing.T, path string)
	}{
		{name: "should miss an entry of another version", plant: func(t *testing.T, path string) {
			mustWrite(t, path, `{"version":2,"model":"jev-1.13.0","stored":"2026-03-01T12:00:00Z","value":{}}`)
		}},
		{name: "should miss a truncated entry", plant: func(t *testing.T, path string) {
			mustWrite(t, path, `{"version":1,"model":"jev-1.13.0","stored":"2026-03-01T12:00:00Z","value":{"a"`)
		}},
		{name: "should miss a directory in place of the entry", plant: func(t *testing.T, path string) {
			mustMkdir(t, path, 0o700)
		}},
	}

	for _, tc := range broken {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store, dir := openAt(t, fixed(t0), cache.Options{})
			path := entryPath(dir, key(0xab, 1), false)
			mustMkdir(t, filepath.Dir(path), 0o700)
			tc.plant(t, path)

			if _, hit, err := store.Get(key(0xab, 1), "typesafe", "jev-1.13.0"); hit || err != nil {
				t.Errorf("Get = %v, %v, want a miss with no error", hit, err)
			}
		})
	}
}

func TestPut(t *testing.T) {
	t.Parallel()

	t.Run("should write mode 600 entries named for a pinned model and for an alias", func(t *testing.T) {
		t.Parallel()

		store, dir := openAt(t, fixed(t0), cache.Options{})
		mustPut(t, store, key(0xab, 1), "typesafe", "jev-1.13.0", []byte(`1`))
		mustPut(t, store, key(0xab, 2), "openrouter", "jev-1.13.0", []byte(`2`))

		for _, path := range []string{entryPath(dir, key(0xab, 1), false), entryPath(dir, key(0xab, 2), true)} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("entry %s: %v", path, err)
			}

			if got := info.Mode().Perm(); got != 0o600 {
				t.Errorf("entry %s mode = %o, want 600", path, got)
			}
		}

		if info, err := os.Stat(filepath.Join(dir, "ab")); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("fan out directory = %v, %v, want mode 700", info, err)
		}
	})

	for _, failOn := range []string{"Write", "Sync", "Close"} {
		t.Run("should return a failed "+failOn+", keep the old entry and leave no temporary file", func(t *testing.T) {
			t.Parallel()

			store, dir := openAt(t, fixed(t0), cache.Options{})
			mustPut(t, store, key(0xab, 1), "typesafe", "jev-1.13.0", []byte(`"old"`))

			failing, err := cache.Open(dir, cache.Options{Now: fixed(t0), FS: failingFS{failOn: failOn}})
			if err != nil {
				t.Fatal(err)
			}

			if putErr := failing.Put(key(0xab, 1), "typesafe", "jev-1.13.0", []byte(`"new"`)); putErr == nil {
				t.Fatalf("Put succeeded, want the %s error", failOn)
			}

			got, hit, err := store.Get(key(0xab, 1), "typesafe", "jev-1.13.0")
			if err != nil || !hit || string(got) != `"old"` {
				t.Errorf("Get after the failed Put = %q, %v, %v, want the old entry", got, hit, err)
			}

			assertNoTemp(t, dir)
		})
	}

	t.Run("should fail when a directory sits in place of the entry", func(t *testing.T) {
		t.Parallel()

		store, dir := openAt(t, fixed(t0), cache.Options{})
		path := entryPath(dir, key(0xab, 1), false)
		mustMkdir(t, path, 0o700)
		mustWrite(t, filepath.Join(path, "inside"), "x")

		if err := store.Put(key(0xab, 1), "typesafe", "jev-1.13.0", []byte(`1`)); err == nil {
			t.Fatal("Put succeeded over a directory, want the rename error")
		}

		assertNoTemp(t, dir)
	})

	t.Run("should never show a partial value to 32 goroutines putting and getting at once", func(t *testing.T) {
		t.Parallel()

		store, dir := openAt(t, fixed(t0), cache.Options{})

		valueOf := func(k int) []byte {
			return []byte(fmt.Sprintf(`{"k":%d,"pad":%q}`, k, strings.Repeat("x", 4096*(k+1))))
		}

		var wg sync.WaitGroup

		for g := range 32 {
			wg.Go(func() {
				for i := range 20 {
					k := (g + i) % 4
					if err := store.Put(key(0xab, byte(k)), "typesafe", "jev-1.13.0", valueOf(k)); err != nil {
						t.Errorf("Put: %v", err)
					}

					got, hit, err := store.Get(key(0xab, byte((k+1)%4)), "typesafe", "jev-1.13.0")
					if err != nil {
						t.Errorf("Get: %v", err)
					}

					if hit && !bytes.Equal(got, valueOf((k+1)%4)) {
						t.Errorf("Get returned %d bytes that are not the value put", len(got))
					}
				}
			})
		}

		wg.Wait()
		assertNoTemp(t, dir)
	})

	t.Run("should evict the least recently used entries once the cap is passed", func(t *testing.T) {
		t.Parallel()

		size := entrySize(t)
		clock := newClock(t0)
		store, dir := openAt(t, clock.now, cache.Options{MaxBytes: 3*size + size/2})

		for i, k := range []byte{1, 2, 3} {
			clock.set(t0.Add(time.Duration(i) * time.Minute))
			mustPut(t, store, key(0xab, k), "typesafe", "jev-1.13.0", sizedValue())
		}

		clock.set(t0.Add(10 * time.Minute))

		if _, hit, err := store.Get(key(0xab, 1), "typesafe", "jev-1.13.0"); !hit || err != nil {
			t.Fatalf("Get = %v, %v, want a hit", hit, err)
		}

		clock.set(t0.Add(20 * time.Minute))
		mustPut(t, store, key(0xab, 4), "typesafe", "jev-1.13.0", sizedValue())

		for k, want := range map[byte]bool{1: true, 2: false, 3: true, 4: true} {
			_, err := os.Stat(entryPath(dir, key(0xab, k), false))
			if exists := err == nil; exists != want {
				t.Errorf("entry %d exists = %v, want %v", k, exists, want)
			}
		}
	})

	t.Run("should remove expired alias entries and old temporary files in the walk", func(t *testing.T) {
		t.Parallel()

		clock := newClock(t0)
		seed, dir := openAt(t, clock.now, cache.Options{})
		mustPut(t, seed, key(0xab, 1), "typesafe", "jev-latest", []byte(`1`))
		mustPut(t, seed, key(0xab, 2), "typesafe", "jev-1.13.0", []byte(`2`))

		later := t0.Add(48 * time.Hour)
		oldTemp := filepath.Join(dir, "ab", ".onesie-tmp-111")
		freshTemp := filepath.Join(dir, "ab", ".onesie-tmp-222")
		mustWrite(t, oldTemp, "partial")
		mustWrite(t, freshTemp, "partial")
		mustChtimes(t, oldTemp, later.Add(-2*time.Hour))
		mustChtimes(t, freshTemp, later.Add(-10*time.Minute))

		store, err := cache.Open(dir, cache.Options{Now: fixed(later)})
		if err != nil {
			t.Fatal(err)
		}

		mustPut(t, store, key(0xcd, 1), "typesafe", "jev-1.13.0", []byte(`3`))

		for path, want := range map[string]bool{
			entryPath(dir, key(0xab, 1), true):  false,
			entryPath(dir, key(0xab, 2), false): true,
			oldTemp:                             false,
			freshTemp:                           true,
		} {
			_, err := os.Stat(path)
			if exists := err == nil; exists != want {
				t.Errorf("%s exists = %v, want %v", path, exists, want)
			}
		}
	})

	t.Run("should leave foreign files alone and not count them", func(t *testing.T) {
		t.Parallel()

		size := entrySize(t)
		clock := newClock(t0)
		store, dir := openAt(t, clock.now, cache.Options{MaxBytes: 3*size + size/2})
		foreign := plantForeign(t, dir, int(10*size))

		for i, k := range []byte{1, 2} {
			clock.set(t0.Add(time.Duration(i) * time.Minute))
			mustPut(t, store, key(0xab, k), "typesafe", "jev-1.13.0", sizedValue())
		}

		for _, k := range []byte{1, 2} {
			if _, err := os.Stat(entryPath(dir, key(0xab, k), false)); err != nil {
				t.Errorf("entry %d was evicted, so a foreign file was counted: %v", k, err)
			}
		}

		for i, k := range []byte{3, 4, 5, 6} {
			clock.set(t0.Add(time.Duration(i+2) * time.Minute))
			mustPut(t, store, key(0xab, k), "typesafe", "jev-1.13.0", sizedValue())
		}

		for _, path := range foreign {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("foreign file %s: %v", path, err)
			}
		}
	})
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	t.Run("should count entries, alias entries and bytes, skipping temporary and foreign files", func(t *testing.T) {
		t.Parallel()

		store, dir := openAt(t, fixed(t0), cache.Options{})
		mustPut(t, store, key(0xab, 1), "typesafe", "jev-1.13.0", []byte(`1`))
		mustPut(t, store, key(0xcd, 2), "typesafe", "jev-1.13.0", []byte(`22`))
		mustPut(t, store, key(0xcd, 3), "typesafe", "jev-latest", []byte(`333`))
		mustWrite(t, filepath.Join(dir, "ab", ".onesie-tmp-1"), "partial")
		plantForeign(t, dir, 100)

		var want int64
		for _, path := range []string{
			entryPath(dir, key(0xab, 1), false),
			entryPath(dir, key(0xcd, 2), false),
			entryPath(dir, key(0xcd, 3), true),
		} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			want += info.Size()
		}

		got, err := cache.Summarize(dir, nil)
		if err != nil {
			t.Fatal(err)
		}

		if got != (cache.Summary{Entries: 3, Aliases: 1, Bytes: want}) {
			t.Errorf("Summarize = %+v, want 3 entries, 1 alias, %d bytes", got, want)
		}
	})

	t.Run("should report zero for a missing directory without creating it", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "absent")

		got, err := cache.Summarize(dir, nil)
		if err != nil || got != (cache.Summary{}) {
			t.Errorf("Summarize = %+v, %v, want zero", got, err)
		}

		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Summarize created %s: %v", dir, err)
		}
	})
}

func TestClear(t *testing.T) {
	t.Parallel()

	populate := func(t *testing.T) (string, []string) {
		t.Helper()

		store, dir := openAt(t, fixed(t0), cache.Options{})
		mustPut(t, store, key(0xab, 1), "typesafe", "jev-1.13.0", []byte(`1`))
		mustPut(t, store, key(0xab, 2), "typesafe", "jev-latest", []byte(`2`))
		mustPut(t, store, key(0xcd, 3), "typesafe", "jev-1.13.0", []byte(`3`))
		mustWrite(t, filepath.Join(dir, "cd", ".onesie-tmp-1"), "partial")

		return dir, plantForeign(t, dir, 10)
	}

	t.Run("should remove every entry and temporary file, keep the rest, and return the count", func(t *testing.T) {
		t.Parallel()

		dir, foreign := populate(t)

		removed, err := cache.Clear(dir, nil)
		if err != nil || removed != 3 {
			t.Fatalf("Clear = %d, %v, want 3", removed, err)
		}

		if got, _ := cache.Summarize(dir, nil); got != (cache.Summary{}) {
			t.Errorf("Summarize after Clear = %+v, want zero", got)
		}

		assertNoTemp(t, dir)

		for _, path := range append(foreign, filepath.Join(dir, "CACHEDIR.TAG"), filepath.Join(dir, "ab"), filepath.Join(dir, "cd")) {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("Clear removed %s: %v", path, err)
			}
		}
	})

	t.Run("should clear a directory with mode 755", func(t *testing.T) {
		t.Parallel()

		dir, _ := populate(t)
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		if removed, err := cache.Clear(dir, nil); err != nil || removed != 3 {
			t.Errorf("Clear = %d, %v, want 3", removed, err)
		}
	})

	t.Run("should refuse a directory with no CACHEDIR.TAG and remove nothing", func(t *testing.T) {
		t.Parallel()

		dir, _ := populate(t)
		if err := os.Remove(filepath.Join(dir, "CACHEDIR.TAG")); err != nil {
			t.Fatal(err)
		}

		removed, err := cache.Clear(dir, nil)
		if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "CACHEDIR.TAG") {
			t.Fatalf("Clear = %d, %v, want a refusal naming the directory and CACHEDIR.TAG", removed, err)
		}

		if got, _ := cache.Summarize(dir, nil); got.Entries != 3 {
			t.Errorf("Summarize after the refusal = %+v, want 3 entries", got)
		}
	})

	t.Run("should remove nothing from a missing directory and not create it", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "absent")

		if removed, err := cache.Clear(dir, nil); err != nil || removed != 0 {
			t.Errorf("Clear = %d, %v, want 0", removed, err)
		}

		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Clear created %s: %v", dir, err)
		}
	})
}

func openAt(t *testing.T, now func() time.Time, opts cache.Options) (*cache.Store, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "cache")
	opts.Now = now

	store, err := cache.Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	return store, dir
}

func key(fanout, n byte) cache.Key {
	var k cache.Key
	k[0] = fanout
	k[31] = n

	return k
}

func entryPath(dir string, k cache.Key, alias bool) string {
	name := hex.EncodeToString(k[:])
	suffix := ".json"

	if alias {
		suffix = ".alias.json"
	}

	return filepath.Join(dir, name[:2], name+suffix)
}

func sizedValue() []byte {
	return []byte(`"` + strings.Repeat("v", 200) + `"`)
}

func entrySize(t *testing.T) int64 {
	t.Helper()

	store, dir := openAt(t, fixed(t0), cache.Options{})
	mustPut(t, store, key(0xab, 1), "typesafe", "jev-1.13.0", sizedValue())

	info, err := os.Stat(entryPath(dir, key(0xab, 1), false))
	if err != nil {
		t.Fatal(err)
	}

	return info.Size()
}

func plantForeign(t *testing.T, dir string, size int) []string {
	t.Helper()

	deep := entryPath(filepath.Join(dir, "ab", "cd"), key(0xab, 9), false)
	paths := []string{
		filepath.Join(dir, "notes.txt"),
		filepath.Join(dir, "ab", "notes.txt"),
		filepath.Join(dir, "zz", "file"),
		deep,
	}

	for _, path := range paths {
		mustMkdir(t, filepath.Dir(path), 0o700)
		mustWrite(t, path, strings.Repeat("f", size))
		mustChtimes(t, path, t0.Add(-1000*time.Hour))
	}

	return paths
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, "*", ".onesie-tmp-*"))
	if err != nil {
		t.Fatal(err)
	}

	if len(matches) > 0 {
		t.Errorf("temporary files left behind: %v", matches)
	}
}

func mustPut(t *testing.T, store *cache.Store, k cache.Key, provider, model string, value []byte) {
	t.Helper()

	if err := store.Put(k, provider, model, value); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func mustMkdir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustChtimes(t *testing.T, path string, when time.Time) {
	t.Helper()

	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func mtime(t *testing.T, path string) time.Time {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	return info.ModTime()
}

func fixed(when time.Time) func() time.Time {
	return func() time.Time { return when }
}

func newClock(start time.Time) *clock {
	return &clock{at: start}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

func (c *clock) set(when time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = when
}

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (f failingFS) CreateTemp(dir, pattern string) (cache.TempFile, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}

	return &failingFile{File: file, failOn: f.failOn}, nil
}

func (failingFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (failingFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (failingFS) Rename(from, to string) error                 { return os.Rename(from, to) }
func (failingFS) Remove(path string) error                     { return os.Remove(path) }
func (failingFS) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (failingFS) ReadDir(path string) ([]os.DirEntry, error)   { return os.ReadDir(path) }

func (failingFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (failingFS) Chtimes(path string, atime, mtime time.Time) error {
	return os.Chtimes(path, atime, mtime)
}

type failingFS struct {
	failOn string
}

var errInjected = errors.New("injected failure")

func (f *failingFile) Write(p []byte) (int, error) {
	if f.failOn == "Write" {
		half := len(p) / 2
		n, _ := f.File.Write(p[:half])

		return n, errInjected
	}

	return f.File.Write(p)
}

func (f *failingFile) Sync() error {
	if f.failOn == "Sync" {
		return errInjected
	}

	return f.File.Sync()
}

func (f *failingFile) Close() error {
	err := f.File.Close()
	if f.failOn == "Close" {
		return errInjected
	}

	return err
}

type failingFile struct {
	*os.File
	failOn string
}
