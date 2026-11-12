package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/cache"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestNewCacheCmd(t *testing.T) {
	t.Parallel()

	fill := func(t *testing.T, env map[string]string) {
		t.Helper()

		// The provider's own base URL, since an answer from a custom one is never pinned.
		_, dial := newTLSAnswerStub(t)

		for _, args := range [][]string{
			{"-m", "jev-1.13.0", "--state", "one"},
			{"-m", "jev-1.13.0", "--state", "two"},
			{"--state", "three"},
		} {
			args = append([]string{"--cache", "--ask", "u=is it urgent"}, args...)
			if _, errOut, code := runCached(t, env, args, "", dial); code != ExitOK {
				t.Fatalf("filling the cache exited %d\n%s", code, errOut)
			}
		}
	}

	t.Run("should print 0 entries for a missing directory and create nothing", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		dir := env["ONESIE_CACHE_DIR"]

		out, errOut, code := runCached(t, env, []string{"cache"}, "")
		if code != ExitOK || out != "0 entries in "+dir+"\n" || errOut != "" {
			t.Errorf("exit %d, stdout %q, stderr %q, want 0 entries", code, out, errOut)
		}

		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("onesie cache created %s: %v", dir, err)
		}
	})

	t.Run("should report the entries, the alias entries and the size", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		fill(t, env)

		out, errOut, code := runCached(t, env, []string{"cache"}, "")
		want := regexp.MustCompile(`^3 entries, [0-9.]+ (B|KB|MB) in ` + regexp.QuoteMeta(env["ONESIE_CACHE_DIR"]) +
			`\. 1 expires, since its model is an alias or its base URL is custom\n$`)

		if code != ExitOK || !want.MatchString(out) {
			t.Errorf("exit %d, stdout %q, want it to match %s\n%s", code, out, want, errOut)
		}
	})

	t.Run("should count a pinned model's answer from a custom base URL as expiring", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		stub := newAnswerStub(t)

		args := []string{"--cache", "--base-url", stub.url, "--ask", "u=is it urgent", "-m", "jev-1.13.0", "--state", "one"}
		if _, errOut, code := runCached(t, env, args, ""); code != ExitOK {
			t.Fatalf("filling the cache exited %d\n%s", code, errOut)
		}

		out, errOut, code := runCached(t, env, []string{"cache"}, "")
		if code != ExitOK || !strings.HasPrefix(out, "1 entry, ") || !strings.HasSuffix(out, ". 1 expires, since its model is an alias or its base URL is custom\n") {
			t.Errorf("exit %d, stdout %q, want the entry counted as expiring\n%s", code, out, errOut)
		}
	})

	t.Run("should name a directory others can read on stderr and still exit 0", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("windows carries no unix permission bits, so the mode check does not apply")
		}

		env := cacheEnv(t)
		dir := env["ONESIE_CACHE_DIR"]
		fill(t, env)

		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		out, errOut, code := runCached(t, env, []string{"cache"}, "")
		if code != ExitOK || !strings.HasPrefix(out, "3 entries") || !strings.Contains(errOut, dir) ||
			!strings.Contains(errOut, "chmod 700") {
			t.Errorf("exit %d, stdout %q, stderr %q, want the summary and the chmod advice", code, out, errOut)
		}
	})

	t.Run("should name a file --cache refuses on stderr, tag nothing and still exit 0", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		dir := env["ONESIE_CACHE_DIR"]

		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		out, errOut, code := runCached(t, env, []string{"cache"}, "")
		want := "onesie: the cache dir " + dir + " holds 'notes.txt', which onesie did not write, and no " +
			"CACHEDIR.TAG, so onesie will not store answers there. Point ONESIE_CACHE_DIR at a new or empty directory\n"

		if code != ExitOK || out != "0 entries in "+dir+"\n" || errOut != want {
			t.Errorf("exit %d, stdout %q, stderr %q, want 0 entries and %q", code, out, errOut, want)
		}

		if _, err := os.Stat(filepath.Join(dir, "CACHEDIR.TAG")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("onesie cache tagged %s: %v", dir, err)
		}
	})

	t.Run("should fill a directory that holds only .DS_Store", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		dir := env["ONESIE_CACHE_DIR"]

		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		fill(t, env)

		if out, errOut, code := runCached(t, env, []string{"cache"}, ""); code != ExitOK || !strings.HasPrefix(out, "3 entries") || errOut != "" {
			t.Errorf("exit %d, stdout %q, stderr %q, want 3 entries and no notice", code, out, errOut)
		}
	})

	t.Run("should say a missing directory holds no cache under cache clear and create nothing", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		dir := env["ONESIE_CACHE_DIR"]

		out, errOut, code := runCached(t, env, []string{"cache", "clear"}, "")
		if code != ExitOK || out != "no cache in "+dir+", so nothing was removed\n" || errOut != "" {
			t.Errorf("exit %d, stdout %q, stderr %q, want the note that there is no cache", code, out, errOut)
		}

		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("cache clear created %s: %v", dir, err)
		}
	})

	t.Run("should remove every entry with cache clear and then report none", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		dir := env["ONESIE_CACHE_DIR"]
		fill(t, env)

		out, errOut, code := runCached(t, env, []string{"cache", "clear"}, "")
		if code != ExitOK || out != "removed 3 entries from "+dir+"\n" {
			t.Fatalf("exit %d, stdout %q, want 3 removed\n%s", code, out, errOut)
		}

		if out, _, code := runCached(t, env, []string{"cache"}, ""); code != ExitOK || out != "0 entries in "+dir+"\n" {
			t.Errorf("exit %d, stdout %q after the clear, want 0 entries", code, out)
		}
	})

	t.Run("should clear a directory with mode 755", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		fill(t, env)

		if err := os.Chmod(env["ONESIE_CACHE_DIR"], 0o755); err != nil {
			t.Fatal(err)
		}

		if out, errOut, code := runCached(t, env, []string{"cache", "clear"}, ""); code != ExitOK || !strings.HasPrefix(out, "removed 3 entries") {
			t.Errorf("exit %d, stdout %q, want 3 removed\n%s", code, out, errOut)
		}
	})

	t.Run("should refuse cache clear on a directory with no CACHEDIR.TAG and remove nothing", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		dir := env["ONESIE_CACHE_DIR"]
		fill(t, env)

		if err := os.Remove(filepath.Join(dir, "CACHEDIR.TAG")); err != nil {
			t.Fatal(err)
		}

		_, errOut, code := runCached(t, env, []string{"cache", "clear"}, "")
		if code != ExitUsage || !strings.Contains(errOut, "CACHEDIR.TAG") {
			t.Errorf("exit %d, stderr %q, want exit 2 naming CACHEDIR.TAG", code, errOut)
		}

		if out, _, _ := runCached(t, env, []string{"cache"}, ""); !strings.HasPrefix(out, "3 entries") {
			t.Errorf("stdout %q after the refusal, want 3 entries left", out)
		}
	})

	t.Run("should ignore ONESIE_MOCK and ONESIE_CACHE", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		env[settingEnv(setting.Cache.Name())] = "yes"
		env["ONESIE_MOCK"] = "absent.json"

		for _, args := range [][]string{{"cache"}, {"cache", "clear"}} {
			if out, errOut, code := runCached(t, env, args, ""); code != ExitOK {
				t.Errorf("%v exit %d, stdout %q, stderr %q, want 0", args, code, out, errOut)
			}
		}
	})

	t.Run("should refuse --mock, which cache does not take", func(t *testing.T) {
		t.Parallel()

		for _, args := range [][]string{{"cache", "--mock", "absent.json"}, {"cache", "clear", "--mock", "absent.json"}} {
			_, errOut, code := runCached(t, cacheEnv(t), args, "")
			if want := "has no flag --mock. --mock belongs to a run"; code != ExitUsage || !strings.Contains(errOut, want) {
				t.Errorf("%v exit %d, stderr %q, want exit 2 containing %q", args, code, errOut, want)
			}
		}
	})

	for _, args := range [][]string{{"cache"}, {"cache", "clear"}} {
		t.Run("should refuse a bad ONESIE_CACHE_TTL under "+strings.Join(args, " ")+" as a run does", func(t *testing.T) {
			t.Parallel()

			env := cacheEnv(t)
			env[settingEnv(setting.CacheTTL.Name())] = "banana"

			out, errOut, code := runCached(t, env, args, "")
			want := "onesie: ONESIE_CACHE_TTL=banana is not a duration of zero or more, such as 90m or 72h\n"

			if code != ExitUsage || out != "" || errOut != want {
				t.Errorf("exit %d, stdout %q, stderr %q, want exit 2 and %q", code, out, errOut, want)
			}
		})
	}

	for _, args := range [][]string{{"cache", "extra"}, {"cache", "clear", "extra"}, {"cache", "bogus"}} {
		t.Run("should refuse "+strings.Join(args, " ")+" with exit 2", func(t *testing.T) {
			t.Parallel()

			if _, errOut, code := runCached(t, cacheEnv(t), args, ""); code != ExitUsage || errOut == "" {
				t.Errorf("exit %d, stderr %q, want exit 2 with a message", code, errOut)
			}
		})
	}
}

func TestDescribeCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		summary cache.Summary
		want    string
	}{
		{name: "should name only the directory when empty", summary: cache.Summary{}, want: "0 entries in /c"},
		{name: "should leave out the expiry sentence when nothing expires", summary: cache.Summary{Entries: 2, Bytes: 10}, want: "2 entries, 10 B in /c"},
		{
			name:    "should give one expiring entry the singular reason",
			summary: cache.Summary{Entries: 2, Aliases: 1, Bytes: 10},
			want:    "2 entries, 10 B in /c. 1 expires, since its model is an alias or its base URL is custom",
		},
		{
			name:    "should give several expiring entries the plural reason",
			summary: cache.Summary{Entries: 3, Aliases: 2, Bytes: 10},
			want:    "3 entries, 10 B in /c. 2 expire, since their model is an alias or their base URL is custom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := describeCache(tc.summary, "/c"); got != tc.want {
				t.Errorf("describeCache = %q, want %q", got, tc.want)
			}
		})
	}
}
