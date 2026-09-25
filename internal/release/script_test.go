//go:build !windows

package release_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const pushLine = "git push --atomic origin main v0.2.0"

func TestReleaseScript(t *testing.T) {
	t.Parallel()

	t.Run("should run the release workflow's four jq checks word for word", func(t *testing.T) {
		t.Parallel()

		workflow := jqChecks(t, filepath.Join("..", "..", ".github", "workflows", "release.yml"))
		script := jqChecks(t, filepath.Join("..", "..", "scripts", "release.sh"))

		if len(workflow) != 4 {
			t.Fatalf("release.yml holds %d jq checks, want 4:\n%s", len(workflow), strings.Join(workflow, "\n"))
		}

		if strings.Join(script, "\n") != strings.Join(workflow, "\n") {
			t.Errorf("release.sh checks:\n%s\nrelease.yml checks:\n%s", strings.Join(script, "\n"), strings.Join(workflow, "\n"))
		}
	})

	t.Run("should skip the checks for the same tags the release workflow skips them for", func(t *testing.T) {
		t.Parallel()

		// The script takes its tag as tag=$1 and skips the checks when releaseprep calls the
		// tag a prerelease. These are the workflow lines that rule mirrors.
		const (
			wantIf  = `if: ${{ !contains(github.ref_name, '-') }}`
			wantTag = `tag="${GITHUB_REF_NAME}"`
		)

		step := checkStep(t, filepath.Join("..", "..", ".github", "workflows", "release.yml"))

		for _, want := range []string{wantIf, wantTag} {
			if !slices.Contains(step, want) {
				t.Errorf("the manifest check step lacks %q:\n%s", want, strings.Join(step, "\n"))
			}
		}

		for _, tag := range []string{"v0.2.0", "v0.2.0-rc.1", "v1.0.0-beta-2", "v10.20.30"} {
			if got, want := mustParse(t, tag).Prerelease(), strings.Contains(tag, "-"); got != want {
				t.Errorf("%s: Prerelease = %v, and the workflow's contains rule says %v", tag, got, want)
			}
		}
	})

	tests := []struct {
		name       string
		tag        string
		dryRun     bool
		setup      func(t *testing.T, w *world)
		wantCode   int
		wantStderr string
		wantStdout []string
		wantLast   string
		wantMake   string
		wantTags   []string
		wantDirty  string
		notStderr  string
		env        []string
		version    string
		released   bool
		check      func(t *testing.T, w *world)
	}{
		{
			name:       "should refuse a dirty tree",
			tag:        "v0.2.0",
			setup:      func(t *testing.T, w *world) { w.write(t, "stray.txt", "left here\n") },
			wantCode:   1,
			wantStderr: "uncommitted changes",
			wantDirty:  "?? stray.txt",
		},
		{
			name: "should refuse a dirty manifest and leave the edit in place",
			tag:  "v0.2.0",
			setup: func(t *testing.T, w *world) {
				w.write(t, ".claude-plugin/plugin.json", "an edit in progress\n")
			},
			wantCode:   1,
			wantStderr: "uncommitted changes",
			wantDirty:  " M .claude-plugin/plugin.json",
			check: func(t *testing.T, w *world) {
				if got := w.read(t, ".claude-plugin/plugin.json"); got != "an edit in progress\n" {
					t.Errorf("the edit became %q", got)
				}
			},
		},
		{
			name:       "should refuse a branch other than main",
			tag:        "v0.2.0",
			setup:      func(t *testing.T, w *world) { w.git(t, w.clone, "checkout", "--quiet", "-b", "feature") },
			wantCode:   1,
			wantStderr: "runs on main, and this is feature",
		},
		{
			name: "should refuse a HEAD ahead of origin/main",
			tag:  "v0.2.0",
			setup: func(t *testing.T, w *world) {
				w.write(t, "extra.txt", "ahead\n")
				w.git(t, w.clone, "add", "extra.txt")
				w.git(t, w.clone, "commit", "--quiet", "-m", "chore: extra")
				w.before = w.head(t)
			},
			wantCode:   1,
			wantStderr: "1 commit ahead of origin/main",
		},
		{
			name: "should refuse a HEAD behind origin/main",
			tag:  "v0.2.0",
			setup: func(t *testing.T, w *world) {
				second := w.secondClone(t)
				w.git(t, w.remote, "fetch", "--quiet", second, "main:main")
			},
			wantCode:   1,
			wantStderr: "1 commit behind origin/main",
		},
		{
			name:       "should refuse a tag with no patch",
			tag:        "v0.2",
			wantCode:   2,
			wantStderr: "vMAJOR.MINOR.PATCH",
		},
		{
			name:       "should refuse a tag with no v",
			tag:        "0.2.0",
			wantCode:   2,
			wantStderr: "vMAJOR.MINOR.PATCH",
		},
		{
			name:       "should refuse a tag with build metadata",
			tag:        "v0.2.0+b",
			wantCode:   2,
			wantStderr: "vMAJOR.MINOR.PATCH",
		},
		{
			name:       "should refuse the latest tag",
			tag:        "v0.1.0",
			wantCode:   1,
			wantStderr: "latest tag is v0.1.0",
		},
		{
			name: "should refuse a tag below one that exists only on the remote",
			tag:  "v0.2.0",
			setup: func(t *testing.T, w *world) {
				second := w.secondClone(t)
				w.git(t, w.remote, "fetch", "--quiet", second, "main:refs/tags/v0.3.0")
			},
			wantCode:   1,
			wantStderr: "latest tag is v0.3.0",
		},
		{
			name:     "should stop when make check fails, leaving the tree as it was",
			tag:      "v0.2.0",
			setup:    func(t *testing.T, w *world) { w.control(t, "fail-check") },
			wantCode: 2,
			wantMake: "check\n",
		},
		{
			name:     "should stop when make skills-check fails, leaving the tree as it was",
			tag:      "v0.2.0",
			setup:    func(t *testing.T, w *world) { w.control(t, "fail-skills-check") },
			wantCode: 2,
			wantMake: "check\nskills-check\n",
		},
		{
			name:     "should bump, check, commit and tag, then print the push",
			tag:      "v0.2.0",
			wantLast: pushLine,
			wantMake: "check\nskills-check\n",
			wantTags: []string{"v0.1.0", "v0.2.0"},
			released: true,
			check: func(t *testing.T, w *world) {
				if got := w.git(t, w.clone, "log", "-1", "--format=%s"); got != "chore: release v0.2.0" {
					t.Errorf("commit subject = %q", got)
				}

				if got := w.git(t, w.clone, "rev-parse", "HEAD~1"); got != w.before {
					t.Errorf("the release commit's parent is %s, want %s", got, w.before)
				}

				if got := w.git(t, w.clone, "rev-parse", "v0.2.0^{commit}"); got != w.head(t) {
					t.Errorf("v0.2.0 points at %s, want HEAD %s", got, w.head(t))
				}

				if commit := w.git(t, w.clone, "cat-file", "commit", "HEAD"); !strings.Contains(commit, "gpgsig -----BEGIN PGP SIGNATURE-----") {
					t.Errorf("the release commit is not signed:\n%s", commit)
				}

				if tag := w.git(t, w.clone, "cat-file", "tag", "v0.2.0"); !strings.Contains(tag, "-----BEGIN PGP SIGNATURE-----") {
					t.Errorf("the tag is not signed:\n%s", tag)
				}
			},
		},
		{
			name:       "should stop when a manifest check fails, restoring the four files",
			tag:        "v0.2.0",
			setup:      func(t *testing.T, w *world) { w.control(t, "jq-wrong") },
			wantCode:   1,
			wantMake:   "check\nskills-check\n",
			wantStdout: []string{"set .agents/plugins/marketplace.json ref from v0.1.0 to v0.2.0"},
			wantStderr: "put the tree, HEAD and tags back",
		},
		{
			name:     "should tag a prerelease on HEAD with no commit",
			tag:      "v0.2.0-rc.1",
			wantLast: "git push --atomic origin main v0.2.0-rc.1",
			wantMake: "check\nskills-check\n",
			wantTags: []string{"v0.1.0", "v0.2.0-rc.1"},
			check: func(t *testing.T, w *world) {
				if got := w.git(t, w.clone, "rev-parse", "v0.2.0-rc.1^{commit}"); got != w.before {
					t.Errorf("v0.2.0-rc.1 points at %s, want %s", got, w.before)
				}
			},
		},
		{
			name:   "should print every change and the four checks under DRY_RUN and change nothing",
			tag:    "v0.2.0",
			dryRun: true,
			wantStdout: []string{
				"would set .claude-plugin/plugin.json version from 0.1.0 to 0.2.0",
				"would set .codex-plugin/plugin.json version from 0.1.0 to 0.2.0",
				"would set .claude-plugin/marketplace.json ref from v0.1.0 to v0.2.0",
				"would set .agents/plugins/marketplace.json ref from v0.1.0 to v0.2.0",
				`would run: test "$(jq -r .version .claude-plugin/plugin.json)" = "${tag#v}", which expects 0.2.0`,
				`would run: test "$(jq -r .version .codex-plugin/plugin.json)" = "${tag#v}", which expects 0.2.0`,
				`would run: test "$(jq -r '.plugins[0].source.ref' .claude-plugin/marketplace.json)" = "$tag", which expects v0.2.0`,
				`would run: test "$(jq -r '.plugins[0].source.ref' .agents/plugins/marketplace.json)" = "$tag", which expects v0.2.0`,
				"would commit chore: release v0.2.0",
				"would tag v0.2.0",
			},
			wantLast: pushLine,
			wantMake: "check\nskills-check\n",
		},
		{
			name:       "should restore the four files when bump fails after writing two of them",
			tag:        "v0.2.0",
			setup:      func(t *testing.T, w *world) { w.control(t, "prep-half") },
			wantCode:   1,
			wantMake:   "check\nskills-check\n",
			wantStderr: "put the tree, HEAD and tags back",
		},
		{
			name:       "should undo its commit when signing the tag fails",
			tag:        "v0.2.0",
			setup:      func(t *testing.T, w *world) { w.control(t, "gpg-fail-tag") },
			wantCode:   128,
			wantStderr: "put the tree, HEAD and tags back",
			wantMake:   "check\nskills-check\n",
		},
		{
			name:       "should leave local tags alone when a colleague's tag on the remote is the latest",
			tag:        "v0.2.0",
			setup:      func(t *testing.T, w *world) { w.git(t, w.remote, "tag", "v0.2.0", "main") },
			wantCode:   1,
			wantStderr: "latest tag is v0.2.0",
			notStderr:  "put the tree, HEAD and tags back",
		},
		{
			name:     "should release past a colleague's lower tag on the remote without fetching it",
			tag:      "v0.2.0",
			setup:    func(t *testing.T, w *world) { w.git(t, w.remote, "tag", "v0.1.5", "main") },
			wantLast: pushLine,
			wantMake: "check\nskills-check\n",
			wantTags: []string{"v0.1.0", "v0.2.0"},
			released: true,
		},
		{
			name:       "should refuse to run under make -n",
			tag:        "v0.2.0",
			env:        []string{"MAKEFLAGS=n -- TAG=v0.2.0"},
			wantCode:   2,
			wantStderr: "Preview with DRY_RUN=1",
		},
		{
			name:       "should refuse to run under make -i",
			tag:        "v0.2.0",
			env:        []string{"MAKEFLAGS=ki"},
			wantCode:   2,
			wantStderr: "Preview with DRY_RUN=1",
		},
		{
			name:     "should run the inner makes with MAKEFLAGS cleared, and read a leading variable as no flag",
			tag:      "v0.2.0",
			env:      []string{"MAKEFLAGS=BREAK=nqti", "MFLAGS=-n"},
			wantLast: pushLine,
			wantMake: "check\nskills-check\n",
			wantTags: []string{"v0.1.0", "v0.2.0"},
			released: true,
		},
		{
			name: "should run a make whose path holds a space",
			tag:  "v0.2.0",
			setup: func(t *testing.T, w *world) {
				system, err := exec.LookPath("make")
				if err != nil {
					t.Fatal(err)
				}

				spaced := filepath.Join(w.dir, "my tools", "make")
				if err := os.MkdirAll(filepath.Dir(spaced), 0o755); err != nil {
					t.Fatal(err)
				}

				if err := os.Symlink(system, spaced); err != nil {
					t.Fatal(err)
				}

				w.env = append(w.env, "MAKE="+spaced)
			},
			wantLast: pushLine,
			wantMake: "check\nskills-check\n",
			wantTags: []string{"v0.1.0", "v0.2.0"},
			released: true,
		},
		{
			name:       "should refuse when the manifests already name the tag",
			tag:        "v0.2.0",
			version:    "0.2.0",
			wantCode:   1,
			wantStderr: "release: the manifests already name v0.2.0",
			wantMake:   "check\nskills-check\n",
			notStderr:  "put the tree, HEAD and tags back",
		},
		{
			name:       "should refuse under DRY_RUN when the manifests already name the tag",
			tag:        "v0.2.0",
			version:    "0.2.0",
			dryRun:     true,
			wantCode:   1,
			wantStderr: "release: the manifests already name v0.2.0",
			wantMake:   "check\nskills-check\n",
		},
		{
			name:       "should refuse and put HEAD back when a make moves it",
			tag:        "v0.2.0",
			setup:      func(t *testing.T, w *world) { w.control(t, "move-head") },
			wantCode:   1,
			wantStderr: "moved HEAD",
			wantMake:   "check\nskills-check\n",
		},
		{
			name: "should leave an existing tag alone when it refuses",
			tag:  "v0.2.0",
			setup: func(t *testing.T, w *world) {
				w.git(t, w.clone, "tag", "v0.2.0", "HEAD")
			},
			wantCode:   1,
			wantStderr: "latest tag is v0.2.0",
			wantTags:   []string{"v0.1.0", "v0.2.0"},
			check: func(t *testing.T, w *world) {
				if got := w.git(t, w.clone, "rev-parse", "v0.2.0"); got != w.before {
					t.Errorf("v0.2.0 now points at %s, want %s", got, w.before)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			version := tc.version
			if version == "" {
				version = "0.1.0"
			}

			w := newWorld(t, version)
			if tc.setup != nil {
				tc.setup(t, w)
			}

			remoteBefore := w.git(t, w.remote, "for-each-ref", "--format=%(refname) %(objectname)")

			code, stdout, stderr := w.release(t, tc.tag, tc.dryRun, tc.env...)

			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, stdout, stderr)
			}

			if !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.wantStderr)
			}

			if tc.notStderr != "" && strings.Contains(stderr, tc.notStderr) {
				t.Errorf("stderr = %q, want it without %q", stderr, tc.notStderr)
			}

			for _, line := range tc.wantStdout {
				if !strings.Contains(stdout, line+"\n") {
					t.Errorf("stdout lacks %q:\n%s", line, stdout)
				}
			}

			if tc.wantLast != "" {
				lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
				if last := lines[len(lines)-1]; last != tc.wantLast {
					t.Errorf("last line of stdout = %q, want %q", last, tc.wantLast)
				}
			} else if strings.Contains(stdout, "git push") {
				t.Errorf("printed a push line on a failed run:\n%s", stdout)
			}

			if got := w.makeLog(t); got != tc.wantMake {
				t.Errorf("make ran %q, want %q", got, tc.wantMake)
			}

			if got := w.git(t, w.clone, "status", "--porcelain"); got != tc.wantDirty {
				t.Errorf("git status --porcelain = %q, want %q", got, tc.wantDirty)
			}

			wantTags := tc.wantTags
			if wantTags == nil {
				wantTags = []string{"v0.1.0"}
			}

			if got := w.git(t, w.clone, "tag", "--list"); got != strings.Join(wantTags, "\n") {
				t.Errorf("tags = %q, want %q", got, wantTags)
			}

			if !tc.released && w.head(t) != w.before {
				t.Errorf("HEAD moved from %s to %s", w.before, w.head(t))
			}

			if tc.released {
				version = "0.2.0"
			}

			if tc.wantDirty == "" {
				for name, content := range manifests(version) {
					if got := w.read(t, name); got != content {
						t.Errorf("%s =\n%s\nwant\n%s", name, got, content)
					}
				}
			}

			if tc.check != nil {
				tc.check(t, w)
			}

			if got := w.git(t, w.remote, "for-each-ref", "--format=%(refname) %(objectname)"); got != remoteBefore {
				t.Errorf("the remote's refs changed from\n%s\nto\n%s", remoteBefore, got)
			}
		})
	}
}

func TestReleaseTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantScript string
	}{
		{
			name:       "should only print the script line under make -n",
			args:       []string{"-n", "release", "TAG=v0.2.0"},
			wantStdout: "bash scripts/release.sh 'v0.2.0'",
		},
		{
			name:       "should only print the script line under make -n with DRY_RUN",
			args:       []string{"-n", "release", "TAG=v0.2.0", "DRY_RUN=1"},
			wantStdout: "bash scripts/release.sh 'v0.2.0'",
		},
		{
			name:       "should run the script with the tag, DRY_RUN and the make that ran it",
			args:       []string{"release", "TAG=v0.2.0", "DRY_RUN=1"},
			wantScript: "tag=v0.2.0 dry_run=1 make=set\n",
		},
		{
			name:       "should refuse without a TAG and not run the script",
			args:       []string{"release"},
			wantCode:   2,
			wantStdout: "make release needs TAG",
		},
	}

	makefile, err := filepath.Abs(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			log := filepath.Join(dir, "script.log")
			writeFile(t, filepath.Join(dir, "scripts", "release.sh"), `echo "tag=$1 dry_run=$DRY_RUN make=${MAKE:+set}" >> '`+log+`'
`, 0o755)

			cmd := exec.Command("make", append([]string{"--no-print-directory", "-f", makefile}, tc.args...)...)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(dir, "gitconfig")}

			out, err := cmd.CombinedOutput()

			var exitErr *exec.ExitError

			code := 0
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}

			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d\n%s", code, tc.wantCode, out)
			}

			if !strings.Contains(string(out), tc.wantStdout) {
				t.Errorf("output = %q, want it to contain %q", out, tc.wantStdout)
			}

			script, err := os.ReadFile(log)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}

			if string(script) != tc.wantScript {
				t.Errorf("the script ran as %q, want %q", script, tc.wantScript)
			}
		})
	}
}

type world struct {
	dir    string
	clone  string
	remote string
	before string
	env    []string
}

func newWorld(t *testing.T, version string) *world {
	t.Helper()

	dir := t.TempDir()
	testBin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	w := &world{
		dir:    dir,
		clone:  filepath.Join(dir, "clone"),
		remote: filepath.Join(dir, "remote.git"),
	}

	home := filepath.Join(dir, "home")
	bin := filepath.Join(dir, "bin")

	for _, sub := range []string{home, bin} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeFile(t, filepath.Join(dir, "gitconfig"), `[user]
	name = Release Test
	email = release@example.com
	signingkey = FAKEKEY
[gpg]
	program = `+filepath.Join(bin, "gpg")+`
[init]
	defaultBranch = main
[advice]
	detachedHead = false
`, 0o644)

	writeFile(t, filepath.Join(bin, "gpg"), `#!/bin/sh
payload=$(cat)
case $payload in
object\ *)
	if [ -e '`+dir+`/gpg-fail-tag' ]; then
		echo "fake gpg: refusing to sign the tag" >&2
		exit 1
	fi
	;;
esac
echo "[GNUPG:] SIG_CREATED D 1 8 00 1700000000 FAKEKEY" >&2
printf '%s\n' '-----BEGIN PGP SIGNATURE-----' '' 'ZmFrZSBzaWduYXR1cmU=' '-----END PGP SIGNATURE-----'
`, 0o755)

	writeFile(t, filepath.Join(bin, "releaseprep"), `#!/bin/sh
if [ "$1" = bump ] && [ "$2" != -dry-run ] && [ -e '`+dir+`/prep-half' ]; then
	echo broken > .claude-plugin/plugin.json
	echo broken > .codex-plugin/plugin.json
	echo "releaseprep: failed halfway" >&2
	exit 1
fi
ONESIE_RELEASE_CHILD=releaseprep exec '`+testBin+`' "$@"
`, 0o755)

	writeFile(t, filepath.Join(bin, "jq"), `#!/bin/sh
if [ -e '`+dir+`/jq-wrong' ]; then
	echo 0.0.0
	exit 0
fi
ONESIE_RELEASE_CHILD=gojq exec '`+testBin+`' "$@"
`, 0o755)

	w.env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"GIT_CONFIG_GLOBAL=" + filepath.Join(dir, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"MAKE=make",
		"RELEASEPREP=" + filepath.Join(bin, "releaseprep"),
		"JQ=" + filepath.Join(bin, "jq"),
		"LC_ALL=C",
	}

	seed := filepath.Join(dir, "seed")
	w.git(t, dir, "init", "--quiet", seed)

	for name, content := range manifests(version) {
		writeFile(t, filepath.Join(seed, filepath.FromSlash(name)), content, 0o644)
	}

	writeFile(t, filepath.Join(seed, "Makefile"), stubMakefile(dir), 0o644)
	w.git(t, seed, "add", ".")
	w.git(t, seed, "commit", "--quiet", "-m", "chore: seed")
	w.git(t, seed, "tag", "-a", "v0.1.0", "-m", "v0.1.0")
	w.git(t, dir, "clone", "--quiet", "--bare", seed, w.remote)
	w.git(t, dir, "clone", "--quiet", w.remote, w.clone)
	w.before = w.head(t)

	return w
}

func (w *world) release(t *testing.T, tag string, dryRun bool, env ...string) (int, string, string) {
	t.Helper()

	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", script, tag)
	cmd.Dir = w.clone
	cmd.Env = w.env

	if dryRun {
		cmd.Env = append(cmd.Env, "DRY_RUN=1")
	}

	cmd.Env = append(cmd.Env, env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	var exitErr *exec.ExitError

	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String()
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), stdout.String(), stderr.String()
	}

	t.Fatal(err)

	return 0, "", ""
}

func (w *world) git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = w.env

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimRight(string(out), "\n")
}

func (w *world) head(t *testing.T) string {
	t.Helper()

	return w.git(t, w.clone, "rev-parse", "HEAD")
}

func (w *world) secondClone(t *testing.T) string {
	t.Helper()

	second := filepath.Join(w.dir, "second")
	w.git(t, w.dir, "clone", "--quiet", w.remote, second)
	writeFile(t, filepath.Join(second, "later.txt"), "later\n", 0o644)
	w.git(t, second, "add", "later.txt")
	w.git(t, second, "commit", "--quiet", "-m", "chore: later")

	return second
}

func (w *world) write(t *testing.T, name, content string) {
	t.Helper()

	writeFile(t, filepath.Join(w.clone, filepath.FromSlash(name)), content, 0o644)
}

func (w *world) read(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(w.clone, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func (w *world) control(t *testing.T, name string) {
	t.Helper()

	writeFile(t, filepath.Join(w.dir, name), "", 0o644)
}

func (w *world) makeLog(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(w.dir, "make.log"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}

	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func stubMakefile(dir string) string {
	return "check:\n" +
		"\t@echo check >> '" + dir + "/make.log'\n" +
		"\t@test ! -e '" + dir + "/fail-check'\n" +
		"\t@test -z '$(BREAK)'\n" +
		"\t@if [ -e '" + dir + "/move-head' ]; then git commit --quiet --allow-empty -m moved; fi\n" +
		"\n" +
		"skills-check:\n" +
		"\t@echo skills-check >> '" + dir + "/make.log'\n" +
		"\t@test ! -e '" + dir + "/fail-skills-check'\n"
}

func jqChecks(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var checks []string

	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, `test "$(jq -r`) {
			checks = append(checks, strings.TrimSpace(line))
		}
	}

	return checks
}

func checkStep(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if !strings.Contains(line, `test "$(jq -r`) {
			continue
		}

		start := i
		for start > 0 && !strings.HasPrefix(strings.TrimSpace(lines[start]), "- name:") {
			start--
		}

		var step []string
		for _, l := range lines[start:i] {
			step = append(step, strings.TrimSpace(l))
		}

		return step
	}

	t.Fatalf("%s holds no jq check", path)

	return nil
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
