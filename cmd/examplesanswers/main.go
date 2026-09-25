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

	"github.com/frodi-karlsson/onesie/examples"
	"github.com/frodi-karlsson/onesie/internal/cli"
)

const (
	dir     = "examples"
	keyName = "TYPESAFE_API_KEY"
	jobs    = "4"
)

var errNoKeychain = errors.New("examplesanswers never reads the keychain")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code, err := run(ctx, os.LookupEnv, os.Stdout, os.Stderr)

	stop()

	if err != nil {
		if _, printErr := fmt.Fprintln(os.Stderr, "examplesanswers:", err); printErr != nil {
			code = cli.ExitUsage
		}
	}

	os.Exit(code)
}

func run(
	ctx context.Context, lookup func(string) (string, bool), stdout, stderr io.Writer,
) (code int, err error) {
	if value, _ := lookup("ONESIE_MOCK"); value != "" {
		return cli.ExitUsage, errors.New("ONESIE_MOCK is set, and the committed answers must come from the API. Unset it")
	}

	key, _ := lookup(keyName)
	if key == "" {
		return cli.ExitUsage, errors.New("needs " + keyName + ", since it asks the live API")
	}

	sets, err := examples.Sets(dir)
	if err != nil {
		return cli.ExitUsage, err
	}

	// A scratch config dir and home, so no saved key, question or credential file of the user's is read.
	scratch, err := os.MkdirTemp("", "examplesanswers-")
	if err != nil {
		return cli.ExitUsage, err
	}

	defer func() {
		err = errors.Join(err, os.RemoveAll(scratch))
	}()

	env := map[string]string{keyName: key, "ONESIE_CONFIG_DIR": scratch}

	for _, set := range sets {
		if _, err := fmt.Fprintf(stdout, "--- %s ---\n", set.Name); err != nil {
			return cli.ExitUsage, err
		}

		code, err := answer(ctx, set, env, scratch, stdout, stderr)
		if err != nil {
			return code, err
		}

		if code != cli.ExitOK {
			return code, fmt.Errorf("%s exited %d", set.Name, code)
		}
	}

	return cli.ExitOK, nil
}

func answer(
	ctx context.Context, set examples.Set, env map[string]string, home string, stdout, stderr io.Writer,
) (int, error) {
	records, err := os.ReadFile(set.Data(dir))
	if err != nil {
		return cli.ExitUsage, err
	}

	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "examplesanswers"},
		cli.WithKeychain(noKeychain{}),
		cli.WithLookupEnv(func(name string) (string, bool) {
			value, found := env[name]

			return value, found
		}),
		cli.WithHomeDir(func() (string, error) { return home, nil }),
		cli.WithStdin(bytes.NewReader(records)),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
	)

	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append(set.CalibrateArgs(dir), "-j", jobs, "--out", set.Answers(dir), "--resume"))

	return cli.Execute(ctx, root), nil
}

type noKeychain struct{}

func (noKeychain) Get(string) (string, error) { return "", errNoKeychain }

func (noKeychain) Set(string, string) error { return errNoKeychain }

func (noKeychain) Delete(string) error { return errNoKeychain }
