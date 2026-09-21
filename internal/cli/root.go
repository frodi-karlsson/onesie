// Package cli assembles the jev command tree. It sits outside package main so the tree can be
// built and exercised in tests without spawning a process.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/argv"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/limits"
)

// NewRootCmd builds a fresh command tree that reads and writes no global state.
func NewRootCmd(info BuildInfo, opts ...RootOption) *cobra.Command {
	flags := &runFlags{}

	settings := rootSettings{
		stdin:     os.Stdin,
		stdinTTY:  isTerminal(os.Stdin),
		stdoutTTY: isTerminal(os.Stdout),
		readFile:  os.ReadFile,
		lookupEnv: os.LookupEnv,
	}

	for _, opt := range opts {
		opt(&settings)
	}

	// The factory reads flags, which are parsed after this returns, so it closes over the pointer.
	// Installing it only when absent keeps an injected factory winning.
	if settings.newClient == nil {
		settings.newClient = defaultClientFactory(info, flags)
	}

	recorder := argv.New()

	root := &cobra.Command{
		Use:   "jev [question]",
		Short: "Ask Jev typed questions about state on stdin",
		Long: "jev is a Unix filter over the TypeSafe System One API.\n\n" +
			"State arrives on stdin, typed answers leave on stdout, and the exit status is " +
			"usable in a conditional.",
		Version: info.Version,
		Args:    cobra.MaximumNArgs(1),
		// Cobra otherwise buries every returned error under the full help text. Execute owns the
		// reporting instead.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}

			if positional == "" && len(recorder.Events()) == 0 {
				return cmd.Help()
			}

			return run(cmd, settings, recorder.Events(), positional, flags)
		},
	}

	// Cobra would claim the lowercase -v for --version, and the spec asks for -V. Registering it
	// here takes the name before cobra reaches for a shorthand of its own.
	root.Flags().BoolP("version", "V", false, "print the version and the built in limits")
	root.SetVersionTemplate("{{.Name}} {{.Version}}\n" + limitsBlock())

	for name, help := range groupFlagHelp {
		root.Flags().Var(recorder.Flag(name), name, help)
	}

	root.Flags().StringVarP(&flags.output, "output", "o", "", "auto, json, values, table or raw")
	root.Flags().BoolVarP(&flags.raw, "raw", "r", false, "print the bare scalar")
	root.Flags().BoolVarP(&flags.quiet, "quiet", "q", false,
		"suppress output, the exit code carries the answer")
	root.Flags().BoolVar(&flags.usage, "usage", false, "add the api usage object to json output")
	root.Flags().StringVarP(&flags.input, "input", "i", "text", "text or json")
	root.Flags().StringVar(&flags.state, "state", "", "state to evaluate, or - to read stdin")
	root.Flags().StringVar(&flags.stateFile, "state-file", "", "read the state from this file")
	root.Flags().StringVarP(&flags.model, "model", "m", "", "model override")
	root.Flags().StringVar(&flags.apiKey, "api-key", "",
		"api key. Prefer TYPESAFE_API_KEY, since argv is visible in ps")
	root.Flags().StringVar(&flags.baseURL, "base-url", "", "api root override")

	root.AddCommand(newVersionCmd(info))

	return root
}

// Execute runs a built command tree and returns the process exit code, so main never has to
// classify an error itself.
func Execute(ctx context.Context, root *cobra.Command) int {
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}

	var rejected *rejectedError
	if errors.As(err, &rejected) {
		return ExitRejected
	}

	if worthReporting(err) {
		if _, printErr := fmt.Fprintln(root.ErrOrStderr(), err); printErr != nil {
			return ExitUsage
		}
	}

	return Classify(err)
}

// RootOption customises the command tree. It exists so tests inject a client, stdin and the
// terminal answers.
type RootOption func(*rootSettings)

// WithClientFactory replaces how commands build their API client.
func WithClientFactory(newClient func(ctx context.Context) (*jev.Client, error)) RootOption {
	return func(s *rootSettings) {
		s.newClient = newClient
	}
}

// WithStdin replaces the reader the state is read from.
func WithStdin(r io.Reader) RootOption {
	return func(s *rootSettings) {
		s.stdin = r
	}
}

// WithStdinTTY overrides the stdin terminal detection, which a test cannot otherwise control.
func WithStdinTTY(tty bool) RootOption {
	return func(s *rootSettings) {
		s.stdinTTY = tty
	}
}

// WithStdoutTTY overrides the stdout terminal detection, so a test does not depend on how it was
// launched.
func WithStdoutTTY(tty bool) RootOption {
	return func(s *rootSettings) {
		s.stdoutTTY = tty
	}
}

// WithReadFile replaces how @FILE references and --state-file are resolved.
func WithReadFile(read func(string) ([]byte, error)) RootOption {
	return func(s *rootSettings) {
		s.readFile = read
	}
}

// WithLookupEnv replaces the environment lookup.
func WithLookupEnv(lookup func(string) (string, bool)) RootOption {
	return func(s *rootSettings) {
		s.lookupEnv = lookup
	}
}

// BuildInfo carries the build metadata stamped into the binary at link time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

type rootSettings struct {
	newClient clientFactory
	stdin     io.Reader
	stdinTTY  bool
	stdoutTTY bool
	readFile  func(string) ([]byte, error)
	lookupEnv func(string) (string, bool)
}

var groupFlagHelp = map[string]string{
	"ask":            "NAME=QUESTION, repeatable, opens a question group",
	"pick":           "comma separated options, making this a choice question",
	"rate":           "comma separated levels in ascending order, making this a score question",
	"desc":           "KEY=TEXT, describing one option, level, yes or no",
	"sep":            "separator for --pick and --rate that follow it",
	"threshold":      "yes/no only, cut the probability at this value",
	"min-confidence": "pick or rate only, requires --fallback",
	"fallback":       "value substituted on low confidence and on error",
}

func limitsBlock() string {
	var out strings.Builder

	for _, entry := range limits.Report() {
		out.WriteString(entry.Name)
		out.WriteString(" ")
		out.WriteString(entry.Value)
		out.WriteString("\n")
	}

	return out.String()
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}
