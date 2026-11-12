// Command examplesanswers regenerates the starter sets' committed answers files from the live API,
// one per model in examples.Models. Run it from the repository root through make examples-answers.
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

	"github.com/frodi-karlsson/onesie-go"

	"github.com/frodi-karlsson/onesie/examples"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/cli"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

const jobs = "4"

var errNoKeychain = errors.New("examplesanswers never reads the keychain")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code, ended := run(ctx, options{lookup: os.LookupEnv, dir: "examples", stdout: os.Stdout, stderr: os.Stderr})

	stop()

	for _, line := range ended.Report() {
		if _, printErr := fmt.Fprintln(os.Stderr, "examplesanswers:", line); printErr != nil {
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

func run(ctx context.Context, opts options) (int, cli.Outcome) {
	if value, _ := opts.lookup("ONESIE_MOCK"); value != "" {
		return cli.ExitUsage, cli.Outcome{
			Failure: errors.New("ONESIE_MOCK is set, and the committed answers must come from the API. Unset it"),
		}
	}

	sets, err := examples.Sets(opts.dir)
	if err != nil {
		return cli.ExitUsage, cli.Outcome{Failure: err}
	}

	// A scratch config dir and home, so no saved key, question or profile of the user's is read.
	scratch, err := os.MkdirTemp("", "examplesanswers-")
	if err != nil {
		return cli.ExitUsage, cli.Outcome{Failure: err}
	}

	env := keysIn(opts.lookup)
	env["ONESIE_CONFIG_DIR"] = scratch
	code, err := answerEach(ctx, sets, opts, env, scratch)

	return code, settled(err, os.RemoveAll(scratch))
}

func keysIn(lookup func(string) (string, bool)) map[string]string {
	keys := map[string]string{}

	for _, name := range keyNames() {
		if key, _ := lookup(name); key != "" {
			keys[name] = key
		}
	}

	return keys
}

func keyNames() []string {
	var names []string

	for _, model := range examples.Models() {
		if key := keyFor(model); key != "" && !slices.Contains(names, key) {
			names = append(names, key)
		}
	}

	return names
}

func answerEach(
	ctx context.Context, sets []examples.Set, opts options, env map[string]string, home string,
) (int, error) {
	for _, set := range sets {
		for _, model := range examples.Models() {
			if _, err := fmt.Fprintf(opts.stdout, "--- %s on %s ---\n", set.Name, model); err != nil {
				return cli.ExitUsage, err
			}

			code, err := answer(ctx, set, model, opts, env, home)
			if err != nil {
				return code, err
			}

			if code != cli.ExitOK {
				return code, fmt.Errorf("%s on %s exited %d", set.Name, model, code)
			}
		}
	}

	return cli.ExitOK, nil
}

func answer(
	ctx context.Context, set examples.Set, model string, opts options, env map[string]string, home string,
) (int, error) {
	records, err := os.ReadFile(set.Data(opts.dir))
	if err != nil {
		return cli.ExitUsage, err
	}

	args := calibrateArgs(set, model, opts.dir)

	// An offline dry run asks nothing and says whether the file still matches the questions. A
	// file that does not is started afresh, and any other is resumed.
	probe := command(opts.root, env, home, records, append(slices.Clone(args), "--resume", "--offline"),
		io.Discard, io.Discard)
	probed := probe.Run(ctx)

	// The probe's own failure is its verdict on the file, so only what followed it is reported.
	if err := reportProbe(opts.stderr, set.Name, cli.Outcome{Later: probed.Later}.Report()); err != nil {
		return cli.ExitUsage, err
	}

	if probed.Failure == nil {
		_, err := fmt.Fprintln(opts.stdout, "complete, nothing to ask")

		return cli.ExitOK, err
	}

	// A key is needed only for a file with records to ask, so a stale Jev file needs no vendor key.
	if key := keyFor(model); env[key] == "" {
		return cli.ExitUsage, fmt.Errorf("%s on %s needs %s, since its answers file has records to ask", set.Name, model, key)
	}

	if !errors.Is(probed.Failure, answersfile.ErrStaleAnswers) {
		args = append(args, "--resume")
	}

	return command(opts.root, env, home, records, args, opts.stdout, opts.stderr).Execute(ctx), nil
}

func keyFor(model string) string {
	provider, err := onesie.ProviderNamed(examples.Provider(model))
	if err != nil {
		return ""
	}

	return provider.EnvAPIKey()
}

func reportProbe(stderr io.Writer, set string, lines []string) error {
	for _, line := range lines {
		if _, err := fmt.Fprintf(stderr, "examplesanswers: probing %s: %s\n", set, line); err != nil {
			return err
		}
	}

	return nil
}

func settled(failure, removal error) cli.Outcome {
	if removal == nil {
		return cli.Outcome{Failure: failure}
	}

	if failure == nil {
		return cli.Outcome{Failure: removal}
	}

	return cli.Outcome{Failure: failure, Later: []error{removal}}
}

func calibrateArgs(set examples.Set, model, dir string) []string {
	// The committed answers must come from the API, never from a cache a developer turned on.
	return append(set.CalibrateArgs(dir, model), "-j", jobs, "--out", set.Answers(dir, model), "--cache=false")
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
