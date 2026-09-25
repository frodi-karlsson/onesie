package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNewCacheCmd(t *testing.T) {
	t.Parallel()

	fill := func(t *testing.T, env map[string]string) {
		t.Helper()

		stub := newAnswerStub(t)

		for _, args := range [][]string{
			{"-m", "jev-1.13.0", "--state", "one"},
			{"-m", "jev-1.13.0", "--state", "two"},
			{"--state", "three"},
		} {
			args = append([]string{"--cache", "--base-url", stub.url, "--ask", "u=is it urgent"}, args...)
			if _, errOut, code := runCached(t, env, args, ""); code != ExitOK {
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
		want := regexp.MustCompile(`^3 entries, 1 of them for an alias, [0-9.]+ (B|KB|MB) in ` +
			regexp.QuoteMeta(env["ONESIE_CACHE_DIR"]) + "\n$")

		if code != ExitOK || !want.MatchString(out) {
			t.Errorf("exit %d, stdout %q, want it to match %s\n%s", code, out, want, errOut)
		}
	})

	t.Run("should name a directory others can read on stderr and still exit 0", func(t *testing.T) {
		t.Parallel()

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

	t.Run("should ignore --mock and ONESIE_CACHE", func(t *testing.T) {
		t.Parallel()

		env := cacheEnv(t)
		env[envCache] = "yes"
		env[envMock] = "absent.json"

		for _, args := range [][]string{{"cache", "--mock", "absent.json"}, {"cache", "clear", "--mock", "absent.json"}} {
			if out, errOut, code := runCached(t, env, args, ""); code != ExitOK {
				t.Errorf("%v exit %d, stdout %q, stderr %q, want 0", args, code, out, errOut)
			}
		}
	})

	for _, args := range [][]string{{"cache", "extra"}, {"cache", "clear", "extra"}, {"cache", "bogus"}} {
		t.Run("should refuse "+strings.Join(args, " ")+" with exit 2", func(t *testing.T) {
			t.Parallel()

			if _, errOut, code := runCached(t, cacheEnv(t), args, ""); code != ExitUsage || errOut == "" {
				t.Errorf("exit %d, stderr %q, want exit 2 with a message", code, errOut)
			}
		})
	}
}
