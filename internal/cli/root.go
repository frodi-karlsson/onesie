// Package cli assembles the onesie command tree. It sits outside package main so the tree can be
// built and exercised in tests without spawning a process.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/limits"
)

const annotationAsksNothing = "onesie-asks-nothing"

// Changed reports false for a name pflag does not know, and reports no error, so a name that has
// to survive a rename is spelled once. A flag nothing passes to Changed needs no constant.
const (
	flagState         = "state"
	flagStateFile     = "state-file"
	flagModel         = "model"
	flagInput         = "input"
	flagJobs          = "jobs"
	flagTimeout       = "timeout"
	flagRetries       = "retries"
	flagMaxRetryAfter = "max-retry-after"
	flagBaseURL       = "base-url"
	flagMap           = "map"
	flagID            = "id"
)

// NewRootCmd builds a fresh command tree that reads and writes no global state.
func NewRootCmd(info BuildInfo, opts ...RootOption) *cobra.Command {
	flags := &runFlags{}

	settings := rootSettings{
		stdin:         os.Stdin,
		stdinTTY:      isTerminal(os.Stdin),
		stdoutTTY:     isTerminal(os.Stdout),
		readFile:      os.ReadFile,
		readDir:       os.ReadDir,
		stat:          os.Stat,
		getwd:         os.Getwd,
		openFile:      os.OpenFile,
		rename:        os.Rename,
		remove:        os.Remove,
		resolve:       filepath.EvalSymlinks,
		readlink:      os.Readlink,
		goos:          runtime.GOOS,
		lock:          newLocker(runtime.GOOS).lockAnswers,
		lookupEnv:     os.LookupEnv,
		homeDir:       os.UserHomeDir,
		now:           time.Now,
		terminalWidth: widthOf(os.Stdout),
		credStore:     creds.NewStore(),
		keychain:      creds.NewKeychain(),
		readSecret:    readHiddenSecret,
	}

	for _, opt := range opts {
		opt(&settings)
	}

	// Installed after the options, since they read the environment lookup and home directory a test
	// may have replaced, and before the factory, which resolves the credential file through them.
	if settings.credPath == nil {
		settings.credPath = credentialPath(settings.lookupEnv, settings.homeDir)
	}

	if settings.configDir == nil {
		settings.configDir = configDir(settings.lookupEnv, settings.homeDir)
	}

	// The factory reads flags, which are parsed after this returns, so it closes over the pointer.
	// Installing it only when absent keeps an injected factory winning.
	if settings.newClient == nil {
		settings.newClient = defaultClientFactory(info, flags, settings)
	}

	recorder := argv.New()

	root := &cobra.Command{
		Use:   "onesie [question]",
		Short: "Ask Jev typed questions about state on stdin",
		Long: "onesie is a Unix filter over the System One API, served by TypeSafe or OpenRouter.\n\n" +
			"State arrives on stdin, typed answers leave on stdout, and the exit status is " +
			"usable in a conditional.\n\n" +
			"A question that begins with a dash needs -- before it, with any flags placed " +
			"first, as in onesie -o json -- '-is this urgent'.",
		Version: info.Version,
		Args:    cobra.MaximumNArgs(1),
		// Cobra otherwise buries every returned error under the full help text. Execute owns the
		// reporting instead.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}

			// A bare onesie is a request for help. Anything else is a real invocation, and help on
			// stdout would corrupt the caller's pipe. Whether it needs a question at all is left
			// to run, since -i request carries its own.
			if cmd.Flags().NFlag() == 0 && len(args) == 0 {
				return cmd.Help()
			}

			// Ahead of run, since a dry run builds no client and would otherwise never look at it.
			if _, providerErr := resolveProvider(settings, flags); providerErr != nil {
				return providerErr
			}

			out, err := openOut(settings, flags)
			if err != nil {
				return err
			}

			defer func() {
				err = errors.Join(err, out.release())
			}()

			if out == nil {
				return run(cmd, settings, recorder.Events(), positional, flags, nil)
			}

			cmd.SetOut(out)

			runErr := run(cmd, settings, recorder.Events(), positional, flags, out)

			return errors.Join(runErr, out.finish(runErr))
		},
	}

	// Cobra would claim the lowercase -v for --version, and the spec asks for -V. Registering it
	// here takes the name before cobra reaches for a shorthand of its own.
	root.Flags().BoolP("version", "V", false, "print the version and the built in limits")
	asksNothing(root.Flags(), "version")
	root.SetVersionTemplate("{{.Name}} {{.Version}}\n" + limitsBlock())

	for name, help := range groupFlagHelp {
		root.Flags().Var(recorder.Flag(name), name, help)
	}

	root.Flags().StringVarP(&flags.output, "output", "o", "", "auto, json, values, table, raw, csv, tsv or markdown")
	root.Flags().BoolVarP(&flags.raw, "raw", "r", false, "print the bare scalar")
	root.Flags().BoolVarP(&flags.quiet, "quiet", "q", false,
		"suppress output, the exit code carries the answer")
	root.Flags().BoolVar(&flags.usage, "usage", false, "add the api usage object to json output")
	// Not a group flag. §17.5 makes it global, so it binds to no --ask group and may appear
	// anywhere in argv, and StringArrayVar keeps the order it was given in.
	root.Flags().StringArrayVar(&flags.assert, "assert", nil,
		"boolean expression over the record, repeatable, combined with and")
	root.Flags().StringArrayVar(&flags.abstainIf, "abstain-if", nil,
		"boolean expression that turns a no into an unsure, repeatable, needs --assert")
	root.Flags().StringVarP(&flags.input, flagInput, "i", "text",
		"text, json, jsonl, lines, csv, tsv or request")
	root.Flags().StringVar(&flags.state, flagState, "", "state to evaluate, or - to read stdin")
	root.Flags().StringVar(&flags.stateFile, flagStateFile, "", "read the state from this file")
	root.Flags().StringVar(&flags.mapSource, flagMap, "",
		"jq expression run on each record, whose result is the state sent. "+
			"Object keys come out sorted, and -V lists the depth and size caps on the result")
	root.Flags().StringVar(&flags.idSource, flagID, "",
		"streaming only, jq expression run on each record, whose string or number result names it "+
			"on every output line. It runs one record at a time as the input is read, so keep it cheap, "+
			"such as a field lookup, and -V lists the cap on its length")
	root.Flags().StringVarP(&flags.model, flagModel, "m", "", "model override")
	root.PersistentFlags().StringVar(&flags.provider, "provider", "",
		"typesafe or openrouter. Defaults to ONESIE_PROVIDER, then typesafe")
	root.Flags().StringVar(&flags.apiKey, "api-key", "",
		"api key. Prefer TYPESAFE_API_KEY, OPENROUTER_API_KEY or onesie auth set, since argv is visible in ps")
	root.Flags().StringVar(&flags.baseURL, flagBaseURL, "", "api root override")
	root.Flags().StringVarP(&flags.file, "file", "f", "",
		"question file or request body, or the name of one saved in .onesie/questions or the config dir")
	root.Flags().BoolVar(&flags.replace, "replace", false, "--ask overrides an id from -f")
	root.Flags().BoolVar(&flags.printQuestions, "print-questions", false,
		"write a question file to stdout and exit")
	root.Flags().BoolVar(&flags.printRequest, "print-request", false,
		"write api shaped request bodies to stdout and exit")
	root.Flags().BoolVar(&flags.listModels, "list-models", false,
		"write the available models to stdout and exit")
	asksNothing(root.Flags(), "list-models")
	root.Flags().BoolVar(&flags.stats, "stats", false,
		"write a one line summary of the run to stderr at exit")
	root.Flags().IntVarP(&flags.jobs, flagJobs, "j", 1, "records in flight at once")
	root.Flags().IntVar(&flags.timeout, flagTimeout, int(limits.DefaultAttemptTimeout.Seconds()),
		"seconds per attempt")
	root.Flags().IntVar(&flags.retries, flagRetries, limits.DefaultRetries,
		"retries after a failed attempt")
	root.Flags().IntVar(&flags.maxRetryAfter, flagMaxRetryAfter,
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
	root.Flags().StringVar(&flags.out, "out", "", "write the answers to this file rather than stdout")
	root.Flags().BoolVar(&flags.resume, "resume", false,
		"streaming only, with --out, carry on after the last complete line in the file. "+
			"Under --id it skips the ids the file answers, so a record whose content changed but whose "+
			"id did not keeps its old answer. It then rewrites the file in input order, with the answered "+
			"ids the input no longer has kept after them, which --prune drops")
	root.Flags().BoolVar(&flags.prune, "prune", false,
		"with --resume and --id, drop the answered ids the input no longer has")
	root.Flags().BoolVar(&flags.merge, "merge", false, "fold the answers into the input record")
	root.Flags().StringVar(&flags.mergeKey, "merge-key", "",
		"where the answers land in the merged record, implies --merge")

	root.AddCommand(newVersionCmd(info))
	root.AddCommand(newAuthCmd(settings, flags))
	root.AddCommand(newCalibrateCmd(settings, flags))
	root.AddCommand(newQuestionsCmd(settings))

	// Cobra hands every subcommand the nearest parent's flag error function, so one hook covers the
	// subcommands cobra adds itself, such as completion.
	root.SetFlagErrorFunc(subcommandHint(root))

	// Cobra adds its help flag when the tree runs. Adding it now lets it be marked like the others.
	root.InitDefaultHelpFlag()
	asksNothing(root.Flags(), "help")

	return root
}

func subcommandHint(root *cobra.Command) func(*cobra.Command, error) error {
	return func(cmd *cobra.Command, err error) error {
		var unknown *pflag.NotExistError
		if cmd == root || !errors.As(err, &unknown) {
			return err
		}

		// A flag that asks no question would refuse the question the hint tells the user to ask.
		known := rootFlag(root, unknown)
		if known == nil {
			return err
		}

		if _, refuses := known.Annotations[annotationAsksNothing]; refuses {
			return err
		}

		top := cmd
		for top.HasParent() && top.Parent() != root {
			top = top.Parent()
		}

		return fmt.Errorf("%w. '%s' is a subcommand. To ask it as a question, put it after --", err, top.Name())
	}
}

func rootFlag(root *cobra.Command, unknown *pflag.NotExistError) *pflag.Flag {
	if unknown.GetSpecifiedShortnames() != "" {
		return root.Flags().ShorthandLookup(unknown.GetSpecifiedName())
	}

	return root.Flags().Lookup(unknown.GetSpecifiedName())
}

func asksNothing(set *pflag.FlagSet, name string) {
	flag := set.Lookup(name)
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}

	flag.Annotations[annotationAsksNothing] = nil
}

// Execute runs a built command tree and returns the process exit code, so main never has to
// classify an error itself.
func Execute(ctx context.Context, root *cobra.Command) int {
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}

	if worthReporting(err) {
		message := err.Error()
		// Cobra's own parse errors are the only ones that do not already carry the prefix, and a
		// consumer filtering stderr should not have to know which layer produced a line.
		if !strings.HasPrefix(message, "onesie: ") {
			message = "onesie: " + message
		}

		if _, printErr := fmt.Fprintln(root.ErrOrStderr(), message); printErr != nil {
			return ExitUsage
		}
	}

	return Classify(err)
}

// WithClientFactory replaces how commands build their API client, so a test can point one at a
// stub. The options passed in are ones the run needs, so a replacement has to pass them on.
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

// WithReadFile replaces how @FILE references, -f and --state-file are resolved.
func WithReadFile(read func(string) ([]byte, error)) RootOption {
	return func(s *rootSettings) {
		s.readFile = read
	}
}

// WithReadDir replaces how the directories an -f name is looked up in are listed.
func WithReadDir(read func(string) ([]fs.DirEntry, error)) RootOption {
	return func(s *rootSettings) {
		s.readDir = read
	}
}

// WithWorkingDir replaces how the working directory an -f name is looked up from is found.
func WithWorkingDir(getwd func() (string, error)) RootOption {
	return func(s *rootSettings) {
		s.getwd = getwd
	}
}

// WithLookupEnv replaces the environment lookup.
func WithLookupEnv(lookup func(string) (string, bool)) RootOption {
	return func(s *rootSettings) {
		s.lookupEnv = lookup
	}
}

// WithHomeDir replaces how the home directory is found, which the credential file falls back to
// when no environment variable names its location.
func WithHomeDir(home func() (string, error)) RootOption {
	return func(s *rootSettings) {
		s.homeDir = home
	}
}

// WithNow replaces the clock the --stats line measures elapsed time with.
func WithNow(now func() time.Time) RootOption {
	return func(s *rootSettings) {
		s.now = now
	}
}

// WithTerminalWidth replaces how table output learns the terminal's width, which a test cannot
// otherwise control.
func WithTerminalWidth(width func() (int, bool)) RootOption {
	return func(s *rootSettings) {
		s.terminalWidth = width
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

// WithKeychain replaces the OS keychain the auth subcommands store keys in.
func WithKeychain(keychain Keychain) RootOption {
	return func(s *rootSettings) {
		s.keychain = keychain
	}
}

// WithSecretReader replaces the hidden prompt, which needs a real terminal a test does not have.
func WithSecretReader(read func() (string, error)) RootOption {
	return func(s *rootSettings) {
		s.readSecret = read
	}
}

// RootOption customises the command tree. It exists so tests inject a client, stdin and the
// terminal answers.
type RootOption func(*rootSettings)

// BuildInfo carries the build metadata stamped into the binary at link time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

type rootSettings struct {
	newClient     clientFactory
	stdin         io.Reader
	stdinTTY      bool
	stdoutTTY     bool
	readFile      func(string) ([]byte, error)
	readDir       func(string) ([]fs.DirEntry, error)
	stat          func(string) (fs.FileInfo, error)
	getwd         func() (string, error)
	configDir     func() (string, error)
	openFile      func(name string, flag int, perm os.FileMode) (*os.File, error)
	rename        func(oldpath, newpath string) error
	remove        func(name string) error
	resolve       func(path string) (string, error)
	readlink      func(name string) (string, error)
	goos          string
	lock          func(answers string) (release func() error, err error)
	lookupEnv     func(string) (string, bool)
	homeDir       func() (string, error)
	now           func() time.Time
	terminalWidth func() (int, bool)
	credPath      func() (string, error)
	credStore     creds.Store
	keychain      Keychain
	readSecret    func() (string, error)
}

// Keychain is where auth set stores a key when the OS has one, one item per account.
type Keychain interface {
	Get(account string) (string, error)
	Set(account, key string) error
	Delete(account string) error
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

func widthOf(f *os.File) func() (int, bool) {
	return func() (int, bool) {
		columns, _, err := term.GetSize(int(f.Fd()))
		if err != nil || columns <= 0 {
			return 0, false
		}

		return columns, true
	}
}

func credentialPath(
	lookupEnv func(string) (string, bool),
	homeDir func() (string, error),
) func() (string, error) {
	return func() (string, error) {
		return creds.Path(configEnv(lookupEnv, homeDir))
	}
}

func configDir(
	lookupEnv func(string) (string, bool),
	homeDir func() (string, error),
) func() (string, error) {
	return func() (string, error) {
		return creds.Dir(configEnv(lookupEnv, homeDir))
	}
}

func configEnv(lookupEnv func(string) (string, bool), homeDir func() (string, error)) creds.Env {
	return creds.Env{
		Lookup: lookupEnv,
		GOOS:   runtime.GOOS,
		Home:   homeDir,
	}
}

func readHiddenSecret() (string, error) {
	secret, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", fmt.Errorf("onesie: reading the key from the terminal: %w", err)
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
