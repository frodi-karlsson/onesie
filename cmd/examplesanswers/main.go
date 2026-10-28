// Command examplesanswers regenerates the starter sets' committed answers files from the live API,
// with the pinned model. Run it from the repository root through make examples-answers.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"

	"github.com/frodi-karlsson/onesie/examples"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/cli"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

const (
	keyName = "TYPESAFE_API_KEY"
	jobs    = "4"
)

var errNoKeychain = errors.New("examplesanswers never reads the keychain")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code, err := run(ctx, options{lookup: os.LookupEnv, dir: "examples", stdout: os.Stdout, stderr: os.Stderr})

	stop()

	if err != nil {
		if _, printErr := fmt.Fprintln(os.Stderr, "examplesanswers:", err); printErr != nil {
			code = cli.ExitUsage
		}
	}

	os.Exit(code)
}

type options struct {
	lookup         func(string) (string, bool)
	dir            string
	stdout, stderr io.Writer
	root           []cli.RootOption
}

func run(ctx context.Context, opts options) (code int, err error) {
	if value, _ := opts.lookup("ONESIE_MOCK"); value != "" {
		return cli.ExitUsage, errors.New("ONESIE_MOCK is set, and the committed answers must come from the API. Unset it")
	}

	key, _ := opts.lookup(keyName)
	if key == "" {
		return cli.ExitUsage, errors.New("needs " + keyName + ", since it asks the live API")
	}

	sets, err := examples.Sets(opts.dir)
	if err != nil {
		return cli.ExitUsage, err
	}

	// A scratch config dir and home, so no saved key, question or profile of the user's is read.
	scratch, err := os.MkdirTemp("", "examplesanswers-")
	if err != nil {
		return cli.ExitUsage, err
	}

	defer func() {
		err = errors.Join(err, os.RemoveAll(scratch))
	}()

	env := map[string]string{keyName: key, "ONESIE_CONFIG_DIR": scratch}

	for _, set := range sets {
		if _, err := fmt.Fprintf(opts.stdout, "--- %s ---\n", set.Name); err != nil {
			return cli.ExitUsage, err
		}

		code, err := answer(ctx, set, opts, env, scratch)
		if err != nil {
			return code, err
		}

		if code != cli.ExitOK {
			return code, fmt.Errorf("%s exited %d", set.Name, code)
		}
	}

	return cli.ExitOK, nil
}

func answer(ctx context.Context, set examples.Set, opts options, env map[string]string, home string) (int, error) {
	records, err := os.ReadFile(set.Data(opts.dir))
	if err != nil {
		return cli.ExitUsage, err
	}

	args := calibrateArgs(set, opts.dir)

	// An offline dry run asks nothing and says whether the file still matches the questions. A
	// file that does not is started afresh, and any other is resumed.
	probe := command(opts.root, env, home, records, append(slices.Clone(args), "--resume", "--offline"),
		io.Discard, io.Discard)
	probed := probe.Run(ctx)
	if err := reportLater(opts.stderr, set.Name, probed.Later); err != nil {
		return cli.ExitUsage, err
	}

	if !errors.Is(probed.Failure, answersfile.ErrStaleAnswers) {
		args = append(args, "--resume")
	}

	return command(opts.root, env, home, records, args, opts.stdout, opts.stderr).Execute(ctx), nil
}

func reportLater(stderr io.Writer, set string, later []error) error {
	for _, each := range later {
		if _, err := fmt.Fprintf(stderr, "examplesanswers: probing %s: %v\n", set, each); err != nil {
			return err
		}
	}

	return nil
}

func calibrateArgs(set examples.Set, dir string) []string {
	// The committed answers must come from the API, never from a cache a developer turned on.
	return append(set.CalibrateArgs(dir), "-j", jobs, "--out", set.Answers(dir), "--cache=false")
}

func command(
	extra []cli.RootOption, env map[string]string, home string, records []byte, args []string,
	stdout, stderr io.Writer,
) *cli.Root {
	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "examplesanswers"},
		append([]cli.RootOption{
			cli.WithKeychain(creds.NewKeychain(creds.WithBackend(noKeychain{}))),
			cli.WithLookupEnv(func(name string) (string, bool) {
				value, found := env[name]

				return value, found
			}),
			cli.WithHomeDir(func() (string, error) { return home, nil }),
			cli.WithStdin(bytes.NewReader(records)),
			cli.WithStdinTTY(false),
			cli.WithStdoutTTY(false),
		}, extra...)...,
	)

	root.Command.SetOut(stdout)
	root.Command.SetErr(stderr)
	root.Command.SetArgs(args)

	return root
}

type noKeychain struct{}

func (noKeychain) Get(string, string) (string, error) { return "", errNoKeychain }

func (noKeychain) Set(string, string, string) error { return errNoKeychain }

func (noKeychain) Delete(string, string) error { return errNoKeychain }
