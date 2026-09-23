// Package cli assembles the jev command tree. It sits outside package main so the tree can be
// built and exercised in tests without spawning a process.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/frodi-karlsson/jev-cli/internal/argv"
	"github.com/frodi-karlsson/jev-cli/internal/creds"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/limits"
)

// NewRootCmd builds a fresh command tree that reads and writes no global state.
func NewRootCmd(info BuildInfo, opts ...RootOption) *cobra.Command {
	flags := &runFlags{}

	settings := rootSettings{
		stdin:      os.Stdin,
		stdinTTY:   isTerminal(os.Stdin),
		stdoutTTY:  isTerminal(os.Stdout),
		readFile:   os.ReadFile,
		lookupEnv:  os.LookupEnv,
		credStore:  creds.NewStore(),
		readSecret: readHiddenSecret,
	}

	for _, opt := range opts {
		opt(&settings)
	}

	// Installed after the options, since it reads the environment lookup a test may have replaced,
	// and before the factory, which resolves the credential file through it.
	if settings.credPath == nil {
		settings.credPath = credentialPath(settings.lookupEnv)
	}

	// The factory reads flags, which are parsed after this returns, so it closes over the pointer.
	// Installing it only when absent keeps an injected factory winning.
	if settings.newClient == nil {
		settings.newClient = defaultClientFactory(info, flags, settings)
	}

	recorder := argv.New()

	root := &cobra.Command{
		Use:   "jev [question]",
		Short: "Ask Jev typed questions about state on stdin",
		Long: "jev is a Unix filter over the TypeSafe System One API.\n\n" +
			"State arrives on stdin, typed answers leave on stdout, and the exit status is " +
			"usable in a conditional.\n\n" +
			"A question that begins with a dash needs -- before it, with any flags placed " +
			"first, as in jev -o json -- '-is this urgent'.",
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

			// A bare jev is a request for help. Anything else is a real invocation, and help on
			// stdout would corrupt the caller's pipe. Whether it needs a question at all is left
			// to run, since -i request carries its own.
			if cmd.Flags().NFlag() == 0 && len(args) == 0 {
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
	// Not a group flag. §17.5 makes it global, so it binds to no --ask group and may appear
	// anywhere in argv, and StringArrayVar keeps the order it was given in.
	root.Flags().StringArrayVar(&flags.assert, "assert", nil,
		"boolean expression over the record, repeatable, combined with and")
	root.Flags().StringVarP(&flags.input, "input", "i", "text",
		"text, json, jsonl, lines or request")
	root.Flags().StringVar(&flags.state, "state", "", "state to evaluate, or - to read stdin")
	root.Flags().StringVar(&flags.stateFile, "state-file", "", "read the state from this file")
	root.Flags().StringVarP(&flags.model, "model", "m", "", "model override")
	root.Flags().StringVar(&flags.apiKey, "api-key", "",
		"api key. Prefer TYPESAFE_API_KEY or jev auth set, since argv is visible in ps")
	root.Flags().StringVar(&flags.baseURL, "base-url", "", "api root override")
	root.Flags().StringVarP(&flags.file, "file", "f", "", "question file or request body")
	root.Flags().BoolVar(&flags.replace, "replace", false, "--ask overrides an id from -f")
	root.Flags().BoolVar(&flags.printQuestions, "print-questions", false,
		"write a question file to stdout and exit")
	root.Flags().BoolVar(&flags.printRequest, "print-request", false,
		"write api shaped request bodies to stdout and exit")
	root.Flags().BoolVar(&flags.listModels, "list-models", false,
		"write the available models to stdout and exit")
	root.Flags().BoolVar(&flags.stats, "stats", false,
		"write a one line summary of the run to stderr at exit")
	root.Flags().IntVarP(&flags.jobs, "jobs", "j", 1, "records in flight at once")
	root.Flags().IntVar(&flags.timeout, "timeout", int(limits.DefaultAttemptTimeout.Seconds()),
		"seconds per attempt")
	root.Flags().IntVar(&flags.retries, "retries", limits.DefaultRetries,
		"retries after a failed attempt")
	root.Flags().IntVar(&flags.maxRetryAfter, "max-retry-after",
		int(limits.DefaultMaxRetryAfter.Seconds()),
		"honour a server Retry-After up to this many seconds")
	root.Flags().BoolVar(&flags.unordered, "unordered", false,
		"streaming only, emit records as they complete")
	root.Flags().BoolVar(&flags.stopOnError, "stop-on-error", false,
		"streaming only, end the run at the first failure")
	root.Flags().BoolVar(&flags.stopOnAssert, "stop-on-assert", false,
		"streaming only, end the run at the first false assertion")
	root.Flags().BoolVar(&flags.skipBlank, "skip-blank", false,
		"streaming only, drop blank lines with no output line")
	root.Flags().BoolVar(&flags.merge, "merge", false, "fold the answers into the input record")
	root.Flags().StringVar(&flags.mergeKey, "merge-key", "",
		"where the answers land in the merged record, implies --merge")

	root.AddCommand(newVersionCmd(info))
	root.AddCommand(newAuthCmd(settings, flags))

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
		message := err.Error()
		// Cobra's own parse errors are the only ones that do not already carry the prefix, and a
		// consumer filtering stderr should not have to know which layer produced a line.
		if !strings.HasPrefix(message, "jev: ") {
			message = "jev: " + message
		}

		if _, printErr := fmt.Fprintln(root.ErrOrStderr(), message); printErr != nil {
			return ExitUsage
		}
	}

	return Classify(err)
}

// RootOption customises the command tree. It exists so tests inject a client, stdin and the
// terminal answers.
type RootOption func(*rootSettings)

// WithClientFactory replaces how commands build their API client, so a test can point one at a
// stub server rather than steering the real constructor through flags. The options are the ones
// the run itself needs on the client, so a replacement has to pass them on.
func WithClientFactory(
	newClient func(ctx context.Context, opts ...jev.Option) (*jev.Client, error),
) RootOption {
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

// WithCredentialPath replaces how the credential file's location is resolved, so a test needs no
// real home directory.
func WithCredentialPath(path func() (string, error)) RootOption {
	return func(s *rootSettings) {
		s.credPath = path
	}
}

// WithCredentialStore replaces the store the auth subcommands read and write through.
func WithCredentialStore(store creds.Store) RootOption {
	return func(s *rootSettings) {
		s.credStore = store
	}
}

// WithSecretReader replaces the hidden prompt, which needs a real terminal a test does not have.
func WithSecretReader(read func() (string, error)) RootOption {
	return func(s *rootSettings) {
		s.readSecret = read
	}
}

// BuildInfo carries the build metadata stamped into the binary at link time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

type rootSettings struct {
	newClient  clientFactory
	stdin      io.Reader
	stdinTTY   bool
	stdoutTTY  bool
	readFile   func(string) ([]byte, error)
	lookupEnv  func(string) (string, bool)
	credPath   func() (string, error)
	credStore  creds.Store
	readSecret func() (string, error)
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

func terminalWidth() (int, bool) {
	columns, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || columns <= 0 {
		return 0, false
	}

	return columns, true
}

func credentialPath(lookupEnv func(string) (string, bool)) func() (string, error) {
	return func() (string, error) {
		return creds.Path(creds.Env{
			Lookup: lookupEnv,
			GOOS:   runtime.GOOS,
			Home:   os.UserHomeDir,
		})
	}
}

func readHiddenSecret() (string, error) {
	secret, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", fmt.Errorf("jev: reading the key from the terminal: %w", err)
	}

	return string(secret), nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}
