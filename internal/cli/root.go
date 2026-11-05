// Package cli assembles the onesie command tree. It sits outside package main so the tree can be
// built and exercised in tests without spawning a process.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/examples/questions"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/mock"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/providers"
	"github.com/frodi-karlsson/onesie/internal/release"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

var pastTag = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+$`)

// pflag's Changed reports false for a name it does not know, with no error, so a name that has to
// survive a rename is spelled once here. A flag nothing passes to Changed needs no constant.
const (
	flagState         = "state"
	flagStateFile     = "state-file"
	flagImage         = "image"
	flagImageField    = "image-field"
	flagLongContext   = "long-context"
	flagModel         = "model"
	flagInput         = "input"
	flagJobs          = "jobs"
	flagTimeout       = "timeout"
	flagRetries       = "retries"
	flagMaxRetryAfter = "max-retry-after"
	flagBaseURL       = "base-url"
	flagAllowHTTP     = "allow-http"
	flagMap           = "map"
	flagID            = "id"
	flagDelimiter     = "delimiter"
)

// NewRootCmd builds a fresh command tree that reads and writes no global state.
func NewRootCmd(info BuildInfo, opts ...RootOption) *Root {
	flags := &runFlags{}

	settings := defaultSettings(info)

	for _, opt := range opts {
		opt(&settings)
	}

	// Installed after the options, since they read the environment lookup and home directory a test
	// may have replaced, and before the holder and the factory, which resolve the files through them.
	settings.profilesPath = profilesLocation(settings.lookupEnv, settings.homeDir, settings.goos)
	settings.configDir = configDir(settings.lookupEnv, settings.homeDir, settings.goos)

	// The holder and the factory read flags, which are parsed after this returns, so both close over
	// the pointer. The holder comes first, since the factory reads through it.
	var root *cobra.Command

	settings.holder = newHolder(settings, flags, func() io.Writer {
		return root.ErrOrStderr()
	})

	// Installed only when absent, so an injected factory wins, for profile test too.
	if settings.newClient == nil {
		settings.newClient = clientBuilder(info, flags, settings)
	}

	recorder := argv.New()

	root = &cobra.Command{
		Use:   "onesie [question]",
		Short: "Ask a System One model typed questions about state on stdin",
		Long:  rootLong(),
		Args:  rootArgs(flags),
		// Cobra otherwise buries every returned error under the full help text. Execute owns the
		// reporting instead.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(cmd, args, info, settings, flags, recorder)
		},
	}

	flags.given = setting.Register(root.Flags(), root.PersistentFlags(), setting.Table(), recorder,
		setting.CommandRoot, runTargets(flags))

	root.AddCommand(newVersionCmd(info))
	root.AddCommand(newProfileCmd(settings, flags))
	calibrate := newCalibrateCmd(settings, flags, root.PersistentFlags())
	root.AddCommand(calibrate)
	root.AddCommand(newQuestionsCmd(settings))
	root.AddCommand(newCacheCmd(settings))
	refuseUnknownHelpTopics(root)
	helpShowsSwitches(root)

	// Cobra hands every subcommand the nearest parent's flag error function, so one hook covers the
	// subcommands cobra adds itself, such as completion.
	explain := flagError(root, calibrate)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return withDashQuestionHint(explain(cmd, err), cmd.Flags(), settings.args, nil)
	})
	addCompletionTakingNoArguments(root)

	if settings.args != nil {
		root.SetArgs(settings.args)
	}

	// Cobra adds its help flag when the tree runs. Adding it now lets it be marked like the others.
	root.InitDefaultHelpFlag()
	asksNothing(root.Flags(), "help")

	return &Root{Command: root, laterFailures: settings.laterFailures}
}

func runTargets(flags *runFlags) map[setting.Name]any {
	return map[setting.Name]any{
		setting.Version.Name(): &flags.version, setting.PrintQuestions.Name(): &flags.printQuestions,
		setting.PrintSchema.Name(): &flags.printSchema, setting.ListModels.Name(): &flags.listModels,
		setting.PrintRequest.Name(): &flags.printRequest,
		"assert":                    &flags.assert, "abstain_if": &flags.abstainIf,
		setting.Output.Name(): &flags.output, setting.Raw.Name(): &flags.raw, setting.Quiet.Name(): &flags.quiet,
		setting.Out.Name(): &flags.out, setting.Resume.Name(): &flags.resume, setting.Prune.Name(): &flags.prune,
		setting.MergeInput.Name(): &flags.merge, setting.MergeKey.Name(): &flags.mergeKey,
		setting.Usage.Name(): &flags.usage, setting.Stats.Name(): &flags.stats,
		setting.Delimiter.Name(): &flags.delimiter, setting.Input.Name(): &flags.input,
		setting.State.Name(): &flags.state, setting.StateFile.Name(): &flags.stateFile,
		"image": &flags.images, "image_field": &flags.imageFields,
		setting.Map.Name(): &flags.mapSource, setting.ID.Name(): &flags.idSource,
		setting.SkipBlank.Name(): &flags.skipBlank, setting.Unordered.Name(): &flags.unordered,
		setting.StopOnError.Name(): &flags.stopOnError, setting.StopOnAssert.Name(): &flags.stopOnAssert,
		setting.NoDedup.Name(): &flags.noDedup, setting.File.Name(): &flags.file,
		setting.Replace.Name(): &flags.replace, setting.Provider.Name(): &flags.provider,
		setting.Profile.Name(): &flags.profile, setting.Model.Name(): &flags.model,
		setting.APIEndpoint.Name(): &flags.baseURL, setting.AllowHTTP.Name(): &flags.allowHTTP,
		setting.LongContext.Name(): &flags.longContext, setting.Mock.Name(): &flags.mock,
		setting.Cache.Name(): &flags.cache, setting.Jobs.Name(): &flags.jobs, setting.Timeout.Name(): &flags.timeout,
		setting.Retries.Name(): &flags.retries, setting.MaxRetryAfter.Name(): &flags.maxRetryAfter,
	}
}

func rootLong() string {
	return "onesie is a Unix filter over the System One API, served by " + providers.Names() + ".\n\n" +
		"State arrives on stdin, typed answers leave on stdout, and the exit status is " +
		"usable in a conditional.\n\n" +
		"A run uses a profile from profiles.json in the config dir: the one --profile or ONESIE_PROFILE " +
		"names, or the default one. A profile holds the provider, its key, in the OS keychain or the " +
		"file, and optionally a model and a base URL or " + providers.WithAccounts() + " account. " +
		"onesie profile create stores one, and onesie profile use makes it the default. --provider or " +
		"ONESIE_PROVIDER runs without a profile, with the key from the provider's variable, " +
		providers.KeyVariables() + ", and so does a run on typesafe when there are no profiles. On " +
		providers.WithAccounts() + " a run also needs an account, from the profile, " +
		providers.AccountVariables() + " or a base URL that holds it. A run on a base URL other than the " +
		"provider's own needs no key, and sends none when none resolves.\n\n" +
		"--mock FILE, or ONESIE_MOCK=FILE, answers from a file instead of the API, with no key and no " +
		"network, so a gate script can be tested for free.\n\n" +
		"--cache, or ONESIE_CACHE=1, stores each successful response on disk and answers a repeated " +
		"request from it. onesie cache reports what it holds, and onesie cache clear empties it.\n\n" +
		"A question that begins with a dash needs -- before it, with any flags placed " +
		"first, as in onesie -o json -- '-is this urgent'. So does a one word question close to " +
		"a subcommand name, since onesie questoins exits 2 and names questions as the closest.\n\n" +
		exitCodesSection(rootExitCodes)
}

func runRoot(
	cmd *cobra.Command, args []string, info BuildInfo, settings rootSettings, flags *runFlags,
	recorder *argv.Recorder,
) (err error) {
	var ready staged

	defer func() {
		err = withKeyHint(withDashQuestionHint(err, cmd.Flags(), settings.args, args), settings, ready.config)
	}()

	// Not cobra's own version flag, which prints before any argument is checked.
	if flags.version {
		return printVersion(cmd, info, flags.output)
	}

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

	if refused := refuseMisspelledSubcommand(cmd, args, recorder.Events()); refused != nil {
		return refused
	}

	// Ahead of the provider, since the schema is the same for every provider and a bad
	// ONESIE_PROVIDER should not stand between an editor and it.
	if flags.printSchema {
		if refused := refuseBesideSchema(cmd.Flags(), args); refused != nil {
			return refused
		}

		return printSchema(cmd.OutOrStdout())
	}

	ready, err = validated(cmd, settings, recorder.Events(), positional, flags)
	if err != nil {
		return err
	}

	// After validation, so a flag clash is named before a provider or an --out that would fail. Ahead
	// of run, since a dry run builds no client and would otherwise never look at the choice.
	// --print-questions prints no provider and no model, so it checks the choice without the file.
	if flags.printQuestions {
		if refused := checkChoice(flags, settings.lookupEnv); refused != nil {
			return refused
		}
	} else if _, resolveErr := settings.holder.resolved(); resolveErr != nil {
		return resolveErr
	}

	out, resume, err := openOut(settings, *flags, false)

	defer func() {
		err = settings.laterFailures.keepFirst(err, out.Release())
	}()

	if err != nil {
		return err
	}

	if out == nil {
		return run(cmd, settings, ready, nil, resume)
	}

	cmd.SetOut(out)

	runErr := run(cmd, settings, ready, out, resume)

	return settings.laterFailures.keepFirst(runErr, out.Finish(runErr))
}

func openOut(settings rootSettings, flags runFlags, readOnly bool) (*answersfile.File, answersfile.Resume, error) {
	out, resume, err := answersfile.Open(answersfile.Options{
		OpenFile: settings.openFile,
		Stat:     settings.stat,
		Rename:   settings.rename,
		Remove:   settings.remove,
		Resolve:  settings.resolve,
		Readlink: settings.readlink,
		Lock:     settings.lock,
		GOOS:     settings.goos,
	}, answersfile.Config{
		Path:      flags.out,
		Resume:    flags.resume,
		Output:    flags.output,
		ByID:      resumesByID(flags),
		ReadOnly:  readOnly,
		Delimiter: answersDelimiter(flags),
	})

	return out, resume, wordAnswersError(err)
}

func answersDelimiter(flags runFlags) input.Delimiter {
	outputMode, outputErr := output.ParseMode(flags.output)
	delimiter, delimiterErr := input.ParseDelimiter(flags.delimiter)

	// Commas, for no --delimiter at all and for -o auto, which never resolves to csv.
	if errors.Join(outputErr, delimiterErr) != nil {
		return input.Delimiter{}
	}

	return answersFileDelimiter(outputMode, delimiter)
}

func answersFileDelimiter(out output.Mode, delimiter input.Delimiter) input.Delimiter {
	if out != output.CSV {
		return input.Delimiter{}
	}

	return delimiter
}

func wordAnswersError(err error) error {
	var (
		notRegular *answersfile.NotRegularError
		directory  *answersfile.DirectoryError
		stale      *answersfile.StaleAnswersError
		separator  *answersfile.SeparatorError
	)

	switch {
	case errors.As(err, &notRegular):
		return &plainError{cause: err, message: fmt.Sprintf(
			"--resume needs --out to name a regular file, and %s is not one", notRegular.Path)}
	case errors.As(err, &directory):
		return &plainError{cause: err, message: fmt.Sprintf("--out %s is a directory", directory.Path)}
	case errors.As(err, &separator):
		given := escaped(string(separator.Given))

		return &plainError{cause: err, message: fmt.Sprintf(
			"--%s '%s' writes '%s', and %s uses '%s'. Drop --resume to start over",
			flagDelimiter, given, given, separator.Path, escaped(string(separator.Found)))}
	case errors.As(err, &stale) && stale.Reason == answersfile.OlderColumns:
		return &plainError{cause: err, message: text(err) + ". Regenerate it without --resume, or write to a new --out"}
	case errors.As(err, &stale), errors.Is(err, answersfile.ErrNoLock):
		return &plainError{cause: err, message: text(err) + ". Drop --resume to start over"}
	default:
		return err
	}
}

func defaultSettings(info BuildInfo) rootSettings {
	return rootSettings{
		stdin:         os.Stdin,
		stdinTTY:      isTerminal(os.Stdin),
		stdoutTTY:     isTerminal(os.Stdout),
		sharedTTY:     sharedTerminal(os.Stdout, os.Stderr),
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
		lookupEnv:     os.LookupEnv,
		homeDir:       os.UserHomeDir,
		now:           time.Now,
		terminalWidth: widthOf(os.Stdout),
		credStore:     creds.NewStore(),
		keychain:      creds.NewKeychain(),
		readSecret:    func() (string, error) { return readHiddenSecret(os.Stdin) },
		schemaURL:     schemaURLOf(info),
		dedupLimit:    limits.MaxDedupRequests,
		readBuiltIn:   questions.Read,
		builtIns:      questions.Names,
		transport:     http.DefaultTransport,
		clock:         onesie.SystemClock(),
		mockTransport: func(provider onesie.Provider) http.RoundTripper { return mock.NewTransport(provider) },
		laterFailures: &failureLog{},
	}
}

func flagError(root, calibrate *cobra.Command) func(*cobra.Command, error) error {
	hint := subcommandHint(root)

	return func(cmd *cobra.Command, err error) error {
		if cmd == calibrate {
			if refused := refusedOnCalibrate(root, err); refused != nil {
				return refused
			}
		}

		return hint(cmd, plainFlagError(cmd, err))
	}
}

func plainFlagError(cmd *cobra.Command, err error) error {
	var (
		invalid  *pflag.InvalidValueError
		required *pflag.ValueRequiredError
		unknown  *pflag.NotExistError
	)

	switch {
	case errors.As(err, &unknown):
		return &plainError{cause: err, message: unknownFlag(cmd, unknown)}
	case errors.As(err, &invalid) && errors.Is(err, argv.ErrSwitchValue):
		return &plainError{cause: err, message: fmt.Sprintf("--%s takes no value, got '%s'",
			invalid.GetFlag().Name, invalid.GetValue())}
	case errors.As(err, &invalid) && errors.Is(err, setting.ErrEmptyValue):
		return &plainError{cause: err, message: fmt.Sprintf("--%s is empty. Name one, or leave the flag out",
			invalid.GetFlag().Name)}
	case errors.As(err, &invalid):
		return &plainError{cause: err, message: fmt.Sprintf("--%s takes %s, got '%s'",
			invalid.GetFlag().Name, valueKind(invalid.GetFlag()), invalid.GetValue())}
	case errors.As(err, &required):
		return &plainError{cause: err, message: fmt.Sprintf("--%s needs a value", required.GetFlag().Name)}
	default:
		return err
	}
}

func unknownFlag(cmd *cobra.Command, unknown *pflag.NotExistError) string {
	name, word := unknown.GetSpecifiedName(), unknown.GetSpecifiedShortnames()
	if word == "" {
		return noSuchFlag(cmd, "--"+name) + closestFlag(cmd, name)
	}

	// A word that holds a space is a question the dash question hint speaks to.
	message := noSuchFlag(cmd, "-"+name)
	if word == name || strings.ContainsAny(word, " \t\n") {
		return message
	}

	return message + " in -" + word + closestFlag(cmd, word)
}

func noSuchFlag(cmd *cobra.Command, spelled string) string {
	if !cmd.HasParent() {
		return "there is no flag " + spelled
	}

	return fmt.Sprintf("%s has no flag %s", strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" "), spelled)
}

func closestFlag(cmd *cobra.Command, name string) string {
	near := nearFlag(cmd, name)
	if near == "" {
		return ""
	}

	return ". The closest is " + near
}

func nearFlag(cmd *cobra.Command, name string) string {
	// Every short name sits one or two edits from many unrelated words, so only a longer name earns
	// a second edit.
	allowance := 1
	if len(name) > 5 {
		allowance = 2
	}

	var near []string

	best := allowance

	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		distance := editDistance(strings.ToLower(name), flag.Name)

		switch {
		case flag.Hidden || distance > best:
		case distance < best:
			near, best = []string{"--" + flag.Name}, distance
		default:
			near = append(near, "--"+flag.Name)
		}
	})

	return strings.Join(near, " or ")
}

func valueKind(flag *pflag.Flag) string {
	switch flag.Value.Type() {
	case "int", "int64", "uint", "uint64":
		return "a whole number"
	case "float64":
		return "a number"
	case "bool":
		return "true or false"
	case "duration":
		return "a duration, such as 30s"
	default:
		return "a value of type " + flag.Value.Type()
	}
}

type plainError struct {
	cause   error
	message string
}

func (e *plainError) Error() string {
	return e.message
}

func (e *plainError) Unwrap() error {
	return e.cause
}

func addCompletionTakingNoArguments(root *cobra.Command) {
	root.InitDefaultCompletionCmd()

	completion, _, err := root.Find([]string{"completion"})
	if err != nil {
		return
	}

	completion.Args = noArguments
	for _, shell := range completion.Commands() {
		shell.Args = noArguments
	}
}

func noArguments(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}

	name := strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")

	var subcommands []string

	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() {
			subcommands = append(subcommands, sub.Name())
		}
	}

	if len(subcommands) > 0 {
		return fmt.Errorf("%s has no subcommand '%s'. Its subcommands: %s",
			name, args[0], strings.Join(subcommands, ", "))
	}

	return fmt.Errorf("%s takes no argument, got '%s'", name, args[0])
}

func printVersion(cmd *cobra.Command, info BuildInfo, format string) error {
	asJSON, err := versionFormat("-V", format)
	if err != nil {
		return err
	}

	if asJSON {
		return writeVersionJSON(cmd.OutOrStdout(), info)
	}

	// The first line stays the name and the version alone, since the session hook reads it.
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\ncommit %s\nbuilt %s\n",
		cmd.Name(), info.Version, info.Commit, info.Date); err != nil {
		return err
	}

	return writeLimits(cmd.OutOrStdout())
}

func rootArgs(flags *runFlags) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		// -V answers before any question is read, so its own message names what it refuses.
		if flags.version && len(args) > 0 {
			return fmt.Errorf("-V takes no question, got '%s'", strings.Join(args, " "))
		}

		return atMostOneQuestion(cmd, args)
	}
}

func refuseUnknownHelpTopics(root *cobra.Command) {
	root.InitDefaultHelpCmd()

	for _, sub := range root.Commands() {
		if sub.Name() != "help" {
			continue
		}

		// Cobra's own help command finds the root for any word the root accepts as a question, and
		// prints the root help with exit 0.
		sub.Run = nil
		sub.RunE = func(help *cobra.Command, args []string) error {
			topic, rest, err := root.Find(args)
			if err != nil || len(rest) > 0 {
				return fmt.Errorf("unknown help topic '%s'. Run onesie --help to see the subcommands",
					strings.Join(args, " "))
			}

			if topic.Context() == nil {
				topic.SetContext(help.Context())
			}

			topic.InitDefaultHelpFlag()
			topic.InitDefaultVersionFlag()

			return topic.Help()
		}
	}
}

func helpShowsSwitches(root *cobra.Command) {
	help := root.HelpFunc()

	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		// pflag prints a NoOptDefVal other than true after the flag's name, and argv.Bare is one.
		var switches []*pflag.Flag

		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			if flag.NoOptDefVal == argv.Bare {
				flag.NoOptDefVal = "true"
				switches = append(switches, flag)
			}
		})

		defer func() {
			for _, flag := range switches {
				flag.NoOptDefVal = argv.Bare
			}
		}()

		help(cmd, args)
	})
}

func subcommandHint(root *cobra.Command) func(*cobra.Command, error) error {
	return func(cmd *cobra.Command, err error) error {
		var unknown *pflag.NotExistError
		if cmd == root || !errors.As(err, &unknown) {
			return err
		}

		// A flag that asks no question would be refused on a run too.
		known := rootFlag(root, unknown)
		if known == nil {
			return err
		}

		if _, refuses := known.Annotations[setting.AnnotationAsksNothing]; refuses {
			return err
		}

		return fmt.Errorf("%w. --%s belongs to a run, not to %s",
			err, known.Name, strings.TrimPrefix(cmd.CommandPath(), root.Name()+" "))
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

	flag.Annotations[setting.AnnotationAsksNothing] = nil
}

// WithClientFactory replaces how commands build their API client, so a test can point one at a
// stub. The options passed in are ones the run needs, so a replacement has to pass them on.
func WithClientFactory(
	newClient func(ctx context.Context, opts ...onesie.Option) (*onesie.Client, error),
) RootOption {
	return func(s *rootSettings) {
		s.newClient = func(ctx context.Context, _ resolution, opts ...onesie.Option) (*onesie.Client, error) {
			return newClient(ctx, opts...)
		}
	}
}

// WithArgs sets the command line the tree parses, so an error can quote a word the parser read as
// flags. Without it the tree parses os.Args and quotes nothing.
func WithArgs(args []string) RootOption {
	return func(s *rootSettings) {
		s.args = args
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

// WithClock replaces the clock a live client reads the time from and waits out a retry on.
func WithClock(clock onesie.Clock) RootOption {
	return func(s *rootSettings) {
		s.clock = clock
	}
}

// WithReadFile replaces how @FILE references, -f and --state-file are resolved.
func WithReadFile(read func(string) ([]byte, error)) RootOption {
	return func(s *rootSettings) {
		s.readFile = read
	}
}

func withReadDir(read func(string) ([]fs.DirEntry, error)) RootOption {
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

// WithLookupEnv replaces the environment lookup, so a test decides every variable a run reads.
func WithLookupEnv(lookup func(string) (string, bool)) RootOption {
	return func(s *rootSettings) {
		s.lookupEnv = lookup
	}
}

// WithHomeDir replaces how the home directory is found, which the config dir and profiles.json fall
// back to when no environment variable names their location.
func WithHomeDir(home func() (string, error)) RootOption {
	return func(s *rootSettings) {
		s.homeDir = home
	}
}

func withStdinClock(clock input.Clock) RootOption {
	return func(s *rootSettings) {
		s.stdinClock = clock
	}
}

func withNow(now func() time.Time) RootOption {
	return func(s *rootSettings) {
		s.now = now
	}
}

func withTerminalWidth(width func() (int, bool)) RootOption {
	return func(s *rootSettings) {
		s.terminalWidth = width
	}
}

func withSharedTTY(shared bool) RootOption {
	return func(s *rootSettings) {
		s.sharedTTY = shared
	}
}

func withCredentialStore(store creds.Store) RootOption {
	return func(s *rootSettings) {
		s.credStore = store
	}
}

// WithKeychain replaces the OS keychain profiles keep their keys in.
func WithKeychain(keychain *creds.Keychain) RootOption {
	return func(s *rootSettings) {
		s.keychain = keychain
	}
}

func withSecretReader(read func() (string, error)) RootOption {
	return func(s *rootSettings) {
		s.readSecret = read
	}
}

// RootOption customises the command tree. It exists so tests inject a client, stdin and the
// terminal answers.
type RootOption func(*rootSettings)

// BuildInfo carries the build metadata stamped into the binary at link time. Tag is the release tag
// the binary was built from exactly, empty for any other build.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
	Tag     string
}

func schemaURLOf(info BuildInfo) string {
	ref := "main"

	// A git describe string such as v0.1.0-3-gabc1234 reads as a prerelease, and names no tag.
	if _, err := release.ParseTag(info.Tag); err == nil && !pastTag.MatchString(info.Tag) {
		ref = info.Tag
	}

	return "https://raw.githubusercontent.com/frodi-karlsson/onesie/" + ref + "/schema/questions.json"
}

type rootSettings struct {
	args          []string
	holder        *holder
	newClient     clientFactory
	stdin         io.Reader
	stdinTTY      bool
	stdoutTTY     bool
	sharedTTY     bool
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
	stdinClock    input.Clock
	terminalWidth func() (int, bool)
	profilesPath  func() (string, error)
	credStore     creds.Store
	keychain      *creds.Keychain
	readSecret    func() (string, error)
	schemaURL     string
	dedupLimit    int
	readBuiltIn   func(name string) ([]byte, bool)
	builtIns      func() []string
	transport     http.RoundTripper
	clock         onesie.Clock
	mockTransport func(onesie.Provider) http.RoundTripper
	laterFailures *failureLog
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

func profilesLocation(
	lookupEnv func(string) (string, bool),
	homeDir func() (string, error),
	goos string,
) func() (string, error) {
	return func() (string, error) {
		return creds.ProfilesPath(configEnv(lookupEnv, homeDir, goos))
	}
}

func configDir(
	lookupEnv func(string) (string, bool),
	homeDir func() (string, error),
	goos string,
) func() (string, error) {
	return func() (string, error) {
		return creds.Dir(configEnv(lookupEnv, homeDir, goos))
	}
}

func configEnv(lookupEnv func(string) (string, bool), homeDir func() (string, error), goos string) creds.Env {
	return creds.Env{
		Lookup: lookupEnv,
		GOOS:   goos,
		Home:   homeDir,
	}
}

func readHiddenSecret(terminal *os.File) (string, error) {
	secret, err := term.ReadPassword(int(terminal.Fd()))
	if err != nil {
		return "", fmt.Errorf("reading the key from the terminal: %w", err)
	}

	return string(secret), nil
}

func sharedTerminal(stdout, stderr *os.File) bool {
	if !isTerminal(stdout) || !isTerminal(stderr) {
		return false
	}

	// On Windows a console handle has no file id, so SameFile holds for any two consoles. A process
	// has one console, so two terminals are the same one there.
	out, outErr := stdout.Stat()
	diagnostics, diagnosticsErr := stderr.Stat()

	return outErr == nil && diagnosticsErr == nil && os.SameFile(out, diagnostics)
}

func isTerminal(f *os.File) bool {
	// A character device is not enough, since /dev/null is one and a hidden prompt cannot read it.
	return term.IsTerminal(int(f.Fd()))
}
