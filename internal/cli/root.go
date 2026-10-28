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
	"strconv"
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
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/providers"
	"github.com/frodi-karlsson/onesie/internal/release"
)

const annotationAsksNothing = "onesie-asks-nothing"

var (
	pastTag       = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+$`)
	errEmptyValue = errors.New("empty value")
)

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
)

var groupFlagHelp = map[string]string{
	"ask":  "NAME=QUESTION, repeatable, opens a question group",
	"pick": "comma separated options, making this a choice question. A repeated --pick adds to the options",
	"rate": "comma separated levels in ascending order, making this a score question. A repeated --rate " +
		"adds levels after the earlier ones. Its score is the " +
		"level index averaged over p, from 0, and norm is score over the top index, from 0 to 1",
	"desc":           "KEY=TEXT, describing one option, level, yes or no",
	"sep":            "separator for --pick and --rate that follow it",
	"threshold":      "yes or no only, cut the probability at this value",
	"min-confidence": "pick or rate only, requires --fallback. Confidence is about 1 when p sits on one answer",
	"fallback":       "value substituted on low confidence and on error",
	"box": "make this a box question, answered with the left, top, right and bottom edges of what it " +
		"finds in an image, each from 0 to 1. Takes no value. Experimental on TypeSafe's side",
}

// NewRootCmd builds a fresh command tree that reads and writes no global state.
func NewRootCmd(info BuildInfo, opts ...RootOption) *cobra.Command {
	flags := &runFlags{}

	settings := defaultSettings(info)

	for _, opt := range opts {
		opt(&settings)
	}

	// Installed after the options, since they read the environment lookup and home directory a test
	// may have replaced, and before the holder and the factory, which resolve the files through them.
	if settings.profilesPath == nil {
		settings.profilesPath = profilesLocation(settings.lookupEnv, settings.homeDir, settings.goos)
	}

	if settings.configDir == nil {
		settings.configDir = configDir(settings.lookupEnv, settings.homeDir, settings.goos)
	}

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

	addRootFlags(root, flags, recorder)

	root.AddCommand(newVersionCmd(info))
	root.AddCommand(newProfileCmd(settings, flags))
	calibrate := newCalibrateCmd(settings, flags)
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

	return root
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
		providers.AccountVariables() + " or a base URL that holds it.\n\n" +
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
	defer func() {
		err = withKeyHint(withDashQuestionHint(err, cmd.Flags(), settings.args, args), settings)
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

	// Ahead of run, since a dry run builds no client and would otherwise never look at it.
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
		err = alsoFailed(err, out.Release())
	}()

	if err != nil {
		return err
	}

	if out == nil {
		return run(cmd, settings, recorder.Events(), positional, flags, nil, resume)
	}

	cmd.SetOut(out)

	runErr := run(cmd, settings, recorder.Events(), positional, flags, out, resume)

	return alsoFailed(runErr, out.Finish(runErr))
}

func addRootFlags(root *cobra.Command, flags *runFlags, recorder *argv.Recorder) {
	// Cobra would claim the lowercase -v for --version, and onesie spells it -V. Registering it
	// here takes the name before cobra reaches for a shorthand of its own.
	root.Flags().BoolVarP(&flags.version, "version", "V", false,
		"print the version, commit, build date and the built in limits, as one JSON object under -o json")
	asksNothing(root.Flags(), "version")

	for name, help := range groupFlagHelp {
		root.Flags().Var(groupFlag(recorder, name), name, help)
	}

	root.Flags().Lookup("box").NoOptDefVal = argv.Bare

	root.Flags().StringVarP(&flags.output, "output", "o", "", "auto, json, values, table, raw, csv, tsv or markdown")
	refuseEmpty(root.Flags(), "output")
	root.Flags().BoolVarP(&flags.raw, "raw", "r", false, "print the bare scalar")
	root.Flags().BoolVarP(&flags.quiet, "quiet", "q", false,
		"suppress output, the exit code carries the answer")
	// Not a group flag. It is global, so it binds to no --ask group and may appear anywhere in
	// argv, and StringArrayVar keeps the order it was given in.
	root.Flags().StringArrayVar(&flags.assert, "assert", nil,
		"boolean expression over the record, repeatable, combined with and")
	root.Flags().StringArrayVar(&flags.abstainIf, "abstain-if", nil,
		"boolean expression that turns a no into an unsure, repeatable, needs --assert")
	root.Flags().StringVarP(&flags.input, flagInput, "i", "text",
		"text, json, jsonl, lines, csv, tsv or request")
	refuseEmpty(root.Flags(), flagInput)
	root.Flags().StringVar(&flags.state, flagState, "", "state to evaluate, or - to read stdin")
	root.Flags().StringVar(&flags.stateFile, flagStateFile, "", "read the state from this file")
	refuseEmpty(root.Flags(), flagStateFile)
	root.PersistentFlags().Var(choiceValue{value: &flags.provider, set: &flags.providerSet}, flagProvider,
		"run on this provider without a profile, "+providers.Names()+". Overrides ONESIE_PROVIDER")
	root.PersistentFlags().Var(choiceValue{value: &flags.profile, set: &flags.profileSet}, flagProfile,
		"the profile a run uses, from profiles.json in the config dir. Also ONESIE_PROFILE, and otherwise "+
			"the default profile, which onesie profile use sets")
	root.PersistentFlags().StringVar(&flags.mock, "mock", "",
		"answer from this file instead of the API. Also ONESIE_MOCK. The file is an object keyed by question id, "+
			`as {"answer":0.9,"team":"billing"}, or one line per record in the shape -o json writes. `+
			"A lone line that carries id, line, model, usage, assert, abstain or an error object is one record")
	refuseEmpty(root.PersistentFlags(), "mock")
	root.Flags().BoolVar(&flags.printQuestions, "print-questions", false,
		"write a question file to stdout and exit, its first line naming the schema --print-schema prints")
	root.Flags().BoolVar(&flags.printSchema, "print-schema", false,
		"write the JSON Schema for a question file to stdout and exit")
	asksNothing(root.Flags(), "print-schema")
	root.Flags().BoolVar(&flags.listModels, "list-models", false,
		"write the available models to stdout and exit")
	asksNothing(root.Flags(), "list-models")
	root.Flags().BoolVar(&flags.unordered, "unordered", false,
		"streaming only, emit records as they complete")
	root.Flags().BoolVar(&flags.stopOnError, "stop-on-error", false,
		"streaming only, end the run at the first failure, with its exit code, or 6 for a line onesie could not read")
	root.Flags().BoolVar(&flags.stopOnAssert, "stop-on-assert", false,
		"streaming only, end the run at the first false assertion")
	root.Flags().BoolVar(&flags.noDedup, "no-dedup", false,
		"streaming only, ask every record even when its request repeats an earlier one")
	root.Flags().BoolVar(&flags.merge, "merge", false, "fold the answers into the input record")
	root.Flags().StringVar(&flags.mergeKey, "merge-key", "",
		"where the answers land in the merged record, implies --merge")
	refuseEmpty(root.Flags(), "merge-key")

	addRunFlags(root.Flags(), flags, rootRunFlagHelp())
}

func rootRunFlagHelp() runFlagHelp {
	return runFlagHelp{
		image: "NAME=PATH, repeatable, puts a PNG or JPEG in the state under NAME, experimental on TypeSafe's side. " +
			"For one record stdin is read beside it only through --state -, so text joins it from --state, " +
			"--state-file or --state -. A stream reads its records from stdin and adds the image to each",
		imageField: "streaming only, repeatable, a state key whose value is a path to a PNG or JPEG, read per record. " +
			"Under -i lines the line is the path. Experimental on TypeSafe's side",
		mapSource: "jq expression run on each record, whose result is the state sent. " +
			"Object keys come out sorted. The result may nest max-map-depth levels deep and encode to max-line-bytes, as -V lists",
		idSource: "streaming only, jq expression run on each record, whose string or number result names it " +
			"on every output line. It runs one record at a time as the input is read, so keep it cheap, " +
			"such as a field lookup, and -V lists the cap on its length",
		out: "write the answers to this file rather than stdout",
		resume: "streaming only, with --out, carry on after the last complete line in the file. " +
			"Under --id it skips the ids the file answers, so a record whose content changed but whose " +
			"id did not keeps its old answer. It then rewrites the file in input order, with the answered " +
			"ids the input no longer has kept after them, which --prune drops",
		prune:     "with --resume and --id, drop the answered ids the input no longer has",
		skipBlank: "streaming only, drop blank lines with no output line",
		usage:     "add the api usage object to json output",
	}
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
		Path:     flags.out,
		Resume:   flags.resume,
		Output:   flags.output,
		ByID:     resumesByID(flags),
		ReadOnly: readOnly,
	})

	return out, resume, wordAnswersError(err)
}

func wordAnswersError(err error) error {
	var (
		notRegular *answersfile.NotRegularError
		directory  *answersfile.DirectoryError
		stale      *answersfile.StaleAnswersError
	)

	switch {
	case errors.As(err, &notRegular):
		return &plainError{cause: err, message: fmt.Sprintf(
			"--resume needs --out to name a regular file, and %s is not one", notRegular.Path)}
	case errors.As(err, &directory):
		return &plainError{cause: err, message: fmt.Sprintf("--out %s is a directory", directory.Path)}
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
	}
}

func addRunFlags(set *pflag.FlagSet, flags *runFlags, help runFlagHelp) {
	set.StringArrayVar(&flags.images, flagImage, nil, help.image)
	set.Var(nonEmptyList{values: &flags.imageFields}, flagImageField, help.imageField)
	set.StringVar(&flags.mapSource, flagMap, "", help.mapSource)
	refuseEmpty(set, flagMap)
	set.StringVar(&flags.idSource, flagID, "", help.idSource)
	refuseEmpty(set, flagID)
	set.StringVarP(&flags.model, flagModel, "m", "", "model override")
	refuseEmpty(set, flagModel)
	set.StringVar(&flags.baseURL, flagBaseURL, "", "api root override")
	refuseEmpty(set, flagBaseURL)
	set.BoolVar(&flags.allowHTTP, flagAllowHTTP, false, allowHTTPHelp)
	set.BoolVar(&flags.longContext, flagLongContext, false,
		"raise the context limits to 262k tokens, slower and less accurate. Also ONESIE_LONG_CONTEXT=1. "+
			"Experimental on TypeSafe's side")
	set.StringVarP(&flags.file, "file", "f", "",
		"question file or request body, or the name of one saved in .onesie/questions or the config dir, "+
			"or of a built in starter set")
	refuseEmpty(set, "file")
	set.BoolVar(&flags.replace, "replace", false, "--ask overrides an id from -f")
	set.IntVarP(&flags.jobs, flagJobs, "j", 1, "records in flight at once")
	set.IntVar(&flags.timeout, flagTimeout, int(limits.DefaultAttemptTimeout.Seconds()),
		"seconds per attempt, "+strconv.Itoa(int(limits.AssetAttemptTimeout.Seconds()))+
			" for a request with images unless given")
	set.IntVar(&flags.retries, flagRetries, limits.DefaultRetries, "retries after a failed attempt")
	set.IntVar(&flags.maxRetryAfter, flagMaxRetryAfter, int(limits.DefaultMaxRetryAfter.Seconds()),
		"most seconds to wait out a server Retry-After")
	set.StringVar(&flags.out, "out", "", help.out)
	refuseEmpty(set, "out")
	set.BoolVar(&flags.resume, "resume", false, help.resume)
	set.BoolVar(&flags.prune, "prune", false, help.prune)
	set.BoolVar(&flags.skipBlank, "skip-blank", false, help.skipBlank)
	set.BoolVar(&flags.stats, "stats", false, "write a one line summary of the run to stderr at exit")
	set.BoolVar(&flags.usage, "usage", false, help.usage)
	set.BoolVar(&flags.printRequest, "print-request", false, "write api shaped request bodies to stdout and exit")
	set.BoolVar(&flags.cache, flagCache, false, cacheHelp)
}

type runFlagHelp struct {
	image      string
	imageField string
	mapSource  string
	idSource   string
	out        string
	resume     string
	prune      string
	skipBlank  string
	usage      string
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
	case errors.As(err, &invalid) && errors.Is(err, errEmptyValue):
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

func groupFlag(recorder *argv.Recorder, name string) pflag.Value {
	switch name {
	case "box":
		return recorder.Switch(name)
	case "sep":
		return nonEmpty{Value: recorder.Flag(name), spaceAllowed: true}
	}

	return recorder.Flag(name)
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

		if _, refuses := known.Annotations[annotationAsksNothing]; refuses {
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
		// Stripped, since a message can quote the server or the input to a terminal.
		if printErr := output.Notice(root.ErrOrStderr(), output.Printable(text(err))); printErr != nil {
			return ExitUsage
		}
	}

	return Classify(err)
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

// WithStdinClock replaces the clock that times the hint printed while stdin stays silent.
func WithStdinClock(clock input.Clock) RootOption {
	return func(s *rootSettings) {
		s.stdinClock = clock
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

// WithProfilesPath replaces how the profiles file's location is resolved, so a test needs no real
// home directory. Its directory also holds the credentials.json a first read converts.
func WithProfilesPath(path func() (string, error)) RootOption {
	return func(s *rootSettings) {
		s.profilesPath = path
	}
}

// WithCredentialStore sets the store that reads and writes profiles.json and converts a
// credentials.json.
func WithCredentialStore(store creds.Store) RootOption {
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

// WithSecretReader replaces the hidden prompt, which needs a real terminal a test does not have.
func WithSecretReader(read func() (string, error)) RootOption {
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
	wrapAnswerer  func(answerer) answerer
	schemaURL     string
	dedupLimit    int
	readBuiltIn   func(name string) ([]byte, bool)
	builtIns      func() []string
	transport     http.RoundTripper
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

func isTerminal(f *os.File) bool {
	// A character device is not enough, since /dev/null is one and a hidden prompt cannot read it.
	return term.IsTerminal(int(f.Fd()))
}

type choiceValue struct {
	value *string
	set   *bool
}

func (c choiceValue) String() string {
	if c.value == nil {
		return ""
	}

	return *c.value
}

func (c choiceValue) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errEmptyValue
	}

	*c.value, *c.set = value, true

	return nil
}

func (choiceValue) Type() string {
	return "string"
}

func refuseEmpty(set *pflag.FlagSet, name string) {
	// A flag given is a choice made, so an empty value is refused rather than read as unset. An unset
	// variable in a script would otherwise drop a --mock and send a paid request.
	flag := set.Lookup(name)
	flag.Value = nonEmpty{Value: flag.Value}
}

type nonEmpty struct {
	pflag.Value
	spaceAllowed bool
}

func (v nonEmpty) Set(value string) error {
	if value == "" || (!v.spaceAllowed && strings.TrimSpace(value) == "") {
		return errEmptyValue
	}

	return v.Value.Set(value)
}

type nonEmptyList struct {
	values *[]string
}

func (l nonEmptyList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errEmptyValue
	}

	*l.values = append(*l.values, value)

	return nil
}

func (l nonEmptyList) String() string {
	return strings.Join(*l.values, ",")
}

func (nonEmptyList) Type() string {
	return "stringArray"
}
