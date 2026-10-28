package skillcheck

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// TestRunProcess runs the test binary again as this, since only a real process lets the
	// deadline logic see a real hang.
	switch os.Getenv("SKILLCHECK_WANT_HELPER_PROCESS") {
	case "1":
		// Waits on a signal nobody sends, so only the deadline's kill ends it.
		never := make(chan os.Signal, 1)
		signal.Notify(never, os.Interrupt)
		<-never

		return
	case "mock":
		// Exits 3 when the variable reached it, so the parent can tell from the exit code alone.
		if _, found := os.LookupEnv("ONESIE_MOCK"); found {
			os.Exit(3)
		}

		os.Exit(0)
	}

	os.Exit(m.Run())
}

func TestNewRunner(t *testing.T) {
	t.Parallel()

	newRunner := func(t *testing.T, binary string, environ []string) *Runner {
		t.Helper()

		runner, err := NewRunner(binary, environ, "", testCatalog(t))
		if err != nil {
			t.Fatal(err)
		}

		return runner
	}

	t.Run("should default to onesie on PATH when binary is empty", func(t *testing.T) {
		t.Parallel()

		if got := newRunner(t, "", nil).Binary; got != "onesie" {
			t.Errorf("NewRunner(\"\").Binary = %q, want onesie", got)
		}
	})

	t.Run("should use the binary it is given", func(t *testing.T) {
		t.Parallel()

		if got := newRunner(t, "/usr/bin/onesie", nil).Binary; got != "/usr/bin/onesie" {
			t.Errorf("NewRunner(...).Binary = %q, want /usr/bin/onesie", got)
		}
	})

	t.Run("should run onesie in the environment it is given, with ONESIE_MOCK removed", func(t *testing.T) {
		t.Parallel()

		environ := []string{"SKILLCHECK_WANT_HELPER_PROCESS=mock", "ONESIE_MOCK=answers.json", "ONESIE_MOCKED=x"}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "SKILLCHECK_WANT_HELPER_PROCESS=") && !strings.HasPrefix(entry, "ONESIE_MOCK=") {
				environ = append(environ, entry)
			}
		}

		code, stderr, err := newRunner(t, os.Args[0], environ).run(context.Background(), []string{"-test.run=^$"})
		if err != nil || code != 0 {
			t.Errorf("the child exited %d, %v, so ONESIE_MOCK reached it\n%s", code, err, stderr)
		}

		if got := isolated(environ, ""); slices.Contains(got, "ONESIE_MOCK=answers.json") ||
			!slices.Contains(got, "ONESIE_MOCKED=x") {
			t.Errorf("isolated kept the wrong entries: %v", got)
		}
	})

	t.Run("should refuse a nil catalog", func(t *testing.T) {
		t.Parallel()

		runner, err := NewRunner("", nil, "", nil)
		if runner != nil || err == nil || err.Error() != "onesie: a runner needs a catalog to build its dry runs" {
			t.Errorf("NewRunner = %v, %v, want the missing catalog named", runner, err)
		}
	})
}

func TestIsolated(t *testing.T) {
	t.Parallel()

	environ := []string{
		"PATH=/bin", "ONESIE_MOCK=m.json", "ONESIE_PROFILE=work", "ONESIE_PROVIDER=openrouter",
		"ONESIE_CONFIG_DIR=/home/me/.config/onesie", "ONESIE_CACHE=1", "TYPESAFE_API_KEY=k",
	}

	t.Run("should drop the mock, the profile and provider choices and the config dir, and set the one given", func(t *testing.T) {
		t.Parallel()

		got := isolated(environ, "/tmp/check")
		want := []string{"PATH=/bin", "ONESIE_CACHE=1", "TYPESAFE_API_KEY=k", "ONESIE_CONFIG_DIR=/tmp/check"}

		if !slices.Equal(got, want) {
			t.Errorf("isolated = %v, want %v", got, want)
		}
	})

	t.Run("should set no config dir when none is given", func(t *testing.T) {
		t.Parallel()

		got := isolated(environ, "")
		want := []string{"PATH=/bin", "ONESIE_CACHE=1", "TYPESAFE_API_KEY=k"}

		if !slices.Equal(got, want) {
			t.Errorf("isolated = %v, want %v", got, want)
		}
	})
}

func TestRunProcess(t *testing.T) {
	// Sets the variable TestMain reads, so it cannot run in parallel with a sibling that relies on
	// the environment being unchanged.
	t.Setenv("SKILLCHECK_WANT_HELPER_PROCESS", "1")

	t.Run("should report an error rather than an ordinary exit code when the deadline kills it", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, _, err := runProcess(ctx, os.Args[0], []string{"-test.run=^$"}, os.Environ())
		if err == nil {
			t.Fatalf("runProcess(...) error = nil, want an error naming the deadline")
		}
	})
}

func TestRunnerDryRun(t *testing.T) {
	t.Parallel()

	t.Run("should strip -q and run under --print-request, reporting the exit code and stderr", func(t *testing.T) {
		t.Parallel()

		var gotArgs []string

		runner := &Runner{Binary: "onesie", catalog: testCatalog(t), exec: func(_ context.Context, _ string, args []string) (int, string, error) {
			gotArgs = args

			return 2, "onesie: bad", nil
		}}

		result, err := runner.DryRun(context.Background(), "onesie 'is this safe' -q --state 'ls'")
		if err != nil {
			t.Fatalf("DryRun(...) error = %v", err)
		}

		want := "--print-request is this safe --state ls"
		if got := strings.Join(gotArgs, " "); got != want {
			t.Errorf("args = %q, want %q", got, want)
		}

		if result.ExitCode != 2 || result.Stderr != "onesie: bad" || result.Skipped != "" {
			t.Errorf("DryRun(...) = %+v, want exit 2 with stderr and no skip", result)
		}
	})

	t.Run("should check the answer file flags it strips before running anything", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			command    string
			wantRun    bool
			wantStderr string
		}{
			{
				name:       "should fail --offline on the root, which pairs it with nothing",
				command:    "onesie 'q' -i jsonl --offline",
				wantStderr: "skillcheck: --offline needs --out and --resume",
			},
			{
				name:       "should fail --offline on the root, whose help does not list it",
				command:    "onesie 'q' -i jsonl --out a.jsonl --resume --offline",
				wantStderr: "skillcheck: --offline is not a flag in onesie --help",
			},
			{
				name:    "should run --offline under calibrate, whose help lists it",
				command: "onesie calibrate --ask a=x -i jsonl --id .id --out a.jsonl --resume --offline",
				wantRun: true,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var ran bool

				runner := &Runner{
					Binary:  "onesie",
					catalog: testCatalog(t),
					exec: func(context.Context, string, []string) (int, string, error) {
						ran = true

						return 0, "", nil
					},
					help: func(_ context.Context, _ string, args []string) (string, error) {
						if args[0] == "calibrate" {
							return "Flags:\n      --ask string   q\n      --id string   id\n  -i, --input string   i\n" +
								"      --offline   o\n      --out string   f\n      --resume   r\n", nil
						}

						return "Available Commands:\n  calibrate   c\n\nFlags:\n  -i, --input string   i\n" +
							"      --out string   f\n      --resume   r\n", nil
					},
				}

				result, err := runner.DryRun(context.Background(), tc.command)
				if err != nil {
					t.Fatalf("DryRun(...) error = %v", err)
				}

				if ran != tc.wantRun || result.Stderr != tc.wantStderr {
					t.Errorf("ran = %v, stderr = %q, want %v and %q", ran, result.Stderr, tc.wantRun, tc.wantStderr)
				}

				if !tc.wantRun && result.ExitCode != 2 {
					t.Errorf("ExitCode = %d, want 2", result.ExitCode)
				}
			})
		}
	})

	t.Run("should skip a command it cannot parse without running anything", func(t *testing.T) {
		t.Parallel()

		runner := &Runner{Binary: "onesie", catalog: testCatalog(t), exec: neverCalled(t)}

		result, err := runner.DryRun(context.Background(), "onesie 'is this safe' | jq .")
		if err != nil {
			t.Fatalf("DryRun(...) error = %v", err)
		}

		if result.Skipped != "contains a pipe" {
			t.Errorf("Skipped = %q, want contains a pipe", result.Skipped)
		}
	})
}

func TestRunnerHelp(t *testing.T) {
	t.Parallel()

	t.Run("should run the command path with --help and return its stdout", func(t *testing.T) {
		t.Parallel()

		var gotArgs []string

		runner := &Runner{Binary: "onesie", help: func(_ context.Context, _ string, args []string) (string, error) {
			gotArgs = args

			return "Usage:", nil
		}}

		got, err := runner.Help(context.Background(), []string{"profile", "create"})
		if err != nil || got != "Usage:" {
			t.Fatalf("Help(...) = %q, %v, want Usage:", got, err)
		}

		if want := []string{"profile", "create", "--help"}; !slices.Equal(gotArgs, want) {
			t.Errorf("args = %q, want %q", gotArgs, want)
		}
	})

	t.Run("should report an error when the help exits non zero", func(t *testing.T) {
		t.Parallel()

		if _, err := runHelp(context.Background(), os.Args[0], []string{"-test.run=^$", "-no-such-flag"}, os.Environ()); err == nil {
			t.Fatalf("runHelp(...) error = nil, want an error")
		}
	})
}
