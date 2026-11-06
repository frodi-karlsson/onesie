package settings

import (
	"fmt"
	"strconv"
	"time"

	"github.com/frodi-karlsson/onesie-go"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/providers"
)

const (
	allowHTTPHelp = "send the request, and any API key, over a plain http base URL to a host other than this " +
		"machine. Without it such a base URL exits 2 before any request"
	experimental    = "Experimental on TypeSafe's side"
	providerTS      = "typesafe"
	providerCF      = "cloudflare"
	providerEnvHelp = "runs without a profile on this provider when neither `--profile` nor `--provider` is given. " +
		"Set beside `ONESIE_PROFILE`, it exits 2"
	profileEnvHelp       = "the profile a run uses when `--profile` is not given. Beside `--provider`, it exits 2"
	profileModelHelp     = "the model a run uses when it names none"
	profileBaseURLHelp   = "the api root the profile's runs use"
	profileAllowHTTPHelp = "store a plain http --base-url to a host other than this machine. Every run on the " +
		"profile then needs --allow-http too"
)

// NoAliasTTL is the cache_ttl a run takes from ONESIE_CACHE_TTL=0, a lifetime under which an entry for
// a model alias is neither read nor stored. It equals the cache's own.
const NoAliasTTL time.Duration = -1

// Table returns every setting, grouped by topic with the mode switches first, so --version is the
// first flag root registers.
func Table() []Row {
	var rows []Row

	for _, group := range [][]Row{
		modeRows(), questionRows(), outputRows(), inputRows(), choiceRows(), requestRows(),
		calibrateRows(), profileRows(), envRows(),
	} {
		rows = append(rows, group...)
	}

	return rows
}

// Lookup returns the row called name, and whether there is one.
func Lookup(name Name) (Row, bool) {
	for _, row := range Table() {
		if row.Name == name {
			return row, true
		}
	}

	return Row{}, false
}

func modeRows() []Row {
	return []Row{
		{
			Name: "version", Kind: Bool(), Modes: Modes(ModeVersion),
			Flag: FlagSpec{Long: "version", Short: "V", AsksNothing: true, Commands: onRoot()},
			Help: "print the version, commit, build date and the built in limits, as one JSON object under -o json",
		},
		{
			Name: "print_questions", Kind: Bool(), Modes: Modes(ModePrintQuestions),
			Flag: FlagSpec{Long: "print-questions", Commands: onRoot()},
			Help: "write a question file to stdout and exit, its first line naming the schema --print-schema prints",
		},
		{
			Name: "print_schema", Kind: Bool(), Modes: Modes(ModePrintSchema),
			Flag: FlagSpec{Long: "print-schema", AsksNothing: true, Commands: onRoot()},
			Help: "write the JSON Schema for a question file to stdout and exit",
		},
		{
			Name: "list_models", Kind: Bool(), Modes: Modes(ModeListModels),
			Flag: FlagSpec{Long: "list-models", AsksNothing: true, Commands: onRoot()},
			Help: "write the available models to stdout and exit",
		},
		{
			Name: "print_request", Kind: Bool(), Modes: Modes(ModePrintRequest),
			Flag: FlagSpec{Long: "print-request", Commands: onRuns()},
			Help: "write api shaped request bodies to stdout and exit",
		},
	}
}

func questionRows() []Row {
	return append([]Row{
		{
			Name: "ask", Kind: Text(), Modes: shaping(),
			Flag: FlagSpec{Long: "ask", Group: true, Repeat: true, Commands: onRuns()},
			Help: "NAME=QUESTION, repeatable, opens a question group",
		},
		{
			Name: "for", Kind: NonEmptyText(), Modes: shaping(),
			Flag: FlagSpec{Long: "for", Group: true, Repeat: true, Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {
					Help: "ID, repeatable, selects a question from -f, so the --pick, --rate, --desc and --sep " +
						"after it apply to that question",
				},
			}},
			Help: "ID, repeatable, selects a question from -f, so the question flags after it apply to that " +
				"question, each with a notice",
		},
		{
			Name: "pick", Scope: ScopeQuestion, Kind: Options(), Modes: shaping(), Shapes: []plan.Shape{plan.Pick},
			FileKey:  "pick",
			FileHelp: "The options, as a sequence of names or a mapping of name to description.",
			Flag:     FlagSpec{Long: "pick", Group: true, Commands: onRuns()},
			Help:     "comma separated options, making this a choice question. A repeated --pick adds to the options",
		},
		{
			Name: "rate", Scope: ScopeQuestion, Kind: Levels(), Modes: shaping(), Shapes: []plan.Shape{plan.Rate},
			FileKey: "rate",
			FileHelp: "The levels from lowest to highest, as names, one key mappings of name to description, or " +
				"a mapping of name to description.",
			Flag: FlagSpec{Long: "rate", Group: true, Commands: onRuns()},
			Help: "comma separated levels in ascending order, making this a score question. A repeated --rate " +
				"adds levels after the earlier ones. Its score is the level index averaged over p, from 0, and " +
				"norm is score over the top index, from 0 to 1",
		},
		{
			Name: "desc", Scope: ScopeQuestion, Kind: Text(), Modes: shaping(),
			Shapes: []plan.Shape{plan.Noul, plan.Pick, plan.Rate},
			Flag:   FlagSpec{Long: "desc", Group: true, Commands: onRuns()},
			Help:   "KEY=TEXT, describing one option, level, yes or no",
		},
		{
			Name: "sep", Scope: ScopeQuestion, Kind: Separator(), Modes: shaping(),
			Shapes: []plan.Shape{plan.Pick, plan.Rate},
			Flag:   FlagSpec{Long: "sep", Group: true, Commands: onRuns()},
			Help:   "separator for --pick and --rate that follow it",
		},
		{
			Name: "box", Scope: ScopeQuestion, Kind: TrueOnly(), Modes: shaping(), Shapes: []plan.Shape{plan.Box},
			FileKey:  "box",
			FileHelp: "Makes this a box question, answered with the edges of what it finds in an image.",
			Flag:     FlagSpec{Long: "box", NoOptDefVal: argv.Bare, Group: true, Commands: onRuns()},
			Help: "make this a box question, answered with the left, top, right and bottom edges of what it " +
				"finds in an image, each from 0 to 1. Takes no value. " + experimental,
		},
	}, policyRows()...)
}

func policyRows() []Row {
	return []Row{
		{
			Name: "threshold", Scope: ScopeQuestion, Kind: Probability(), Modes: policing(),
			Shapes:   []plan.Shape{plan.Noul},
			FileKey:  "threshold",
			FileHelp: "The yes probability a yes or no answer must reach, between 0 and 1.",
			Flag:     FlagSpec{Long: "threshold", Group: true, Commands: onRoot()},
			Help:     "yes or no only, cut the probability at this value",
		},
		{
			Name: "min_confidence", Scope: ScopeQuestion, Kind: Probability(), Modes: policing(),
			Shapes:   []plan.Shape{plan.Pick, plan.Rate},
			FileKey:  "min_confidence",
			FileHelp: "The confidence a pick or rate answer must reach, between 0 and 1, or the fallback stands in.",
			Flag:     FlagSpec{Long: "min-confidence", Group: true, Commands: onRoot()},
			Help:     "pick or rate only, requires --fallback. Confidence is about 1 when p sits on one answer",
		},
		{
			Name: "fallback", Scope: ScopeQuestion, Kind: Fallback(), Modes: policing(),
			Shapes:   []plan.Shape{plan.Noul, plan.Pick, plan.Rate},
			FileKey:  "fallback",
			FileHelp: "The answer that stands in for one that falls short.",
			Flag:     FlagSpec{Long: "fallback", Group: true, Commands: onRoot()},
			Help:     "value substituted on low confidence and on error",
		},
		{
			Name: "assert", Kind: Expr(), Modes: Modes(ModeAsk, ModeStream),
			FileKey:  "assert",
			FileHelp: "A gate over every answer, as --assert takes it.",
			Flag:     FlagSpec{Long: "assert", Repeat: true, Commands: onRoot()},
			Help:     "boolean expression over the record, repeatable, combined with and",
		},
		{
			Name: "abstain_if", Kind: Expr(), Modes: Modes(ModeAsk, ModeStream),
			FileKey:  "abstain_if",
			FileHelp: "An abstain expression, as --abstain-if takes it.",
			Flag:     FlagSpec{Long: "abstain-if", Repeat: true, Commands: onRoot()},
			Help:     "boolean expression that turns a no into an unsure, repeatable, needs --assert",
		},
	}
}

func outputRows() []Row {
	return append([]Row{
		{
			Name: "output", Kind: outputModes(), Modes: Modes(ModeAsk, ModeStream, ModeVersion, ModeCalibrate),
			Flag: FlagSpec{Long: "output", Short: "o", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {
					Help: "the report, table, json or auto, which means table",
					Kind: Enum([]string{"table", "json", "auto"}, nil),
				},
			}},
			Derive: autoOutput,
			Help:   "auto, json, values, table, raw, csv, tsv or markdown",
		},
		{
			Name: "raw", Kind: Bool(), Modes: Modes(ModeAsk, ModeStream),
			Flag: FlagSpec{Long: "raw", Short: "r", Commands: onRoot()},
			Help: "print the bare scalar",
		},
		{
			Name: "quiet", Kind: Bool(), Modes: Modes(ModeAsk, ModeStream),
			Flag: FlagSpec{Long: "quiet", Short: "q", Commands: onRoot()},
			Help: "suppress output, the exit code carries the answer",
		},
		{
			Name: "out", Kind: NonEmptyText(), Modes: answering(),
			Flag: FlagSpec{Long: "out", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "write the answers to this file as -o json lines"},
			}},
			Help: "write the answers to this file rather than stdout",
		},
		{
			Name: "resume", Kind: Bool(), Modes: Modes(ModeStream, ModeCalibrate),
			Flag: FlagSpec{Long: "resume", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "with --out and --id, skip the ids the file already answers"},
			}},
			Help: "streaming only, with --out, carry on after the last complete line in the file. Under --id it " +
				"skips the ids the file answers, so a record whose content changed but whose id did not keeps " +
				"its old answer. It then rewrites the file in input order, with the answered ids the input no " +
				"longer has kept after them, which --prune drops",
		},
		{
			Name: "prune", Kind: Bool(), Modes: Modes(ModeStream, ModeCalibrate),
			Flag: FlagSpec{Long: "prune", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "with --resume, drop the answered ids the input no longer has"},
			}},
			Help: "with --resume and --id, drop the answered ids the input no longer has",
		},
		{
			Name: "merge", Kind: Bool(), Modes: Modes(ModeAsk, ModeStream),
			Flag: FlagSpec{Long: "merge", Commands: onRoot()},
			Help: "fold the answers into the input record",
		},
		{
			Name: "merge_key", Kind: NonEmptyText(), Modes: Modes(ModeAsk, ModeStream),
			Flag: FlagSpec{Long: "merge-key", Commands: onRoot()},
			Help: "where the answers land in the merged record, implies --merge",
		},
	}, reportRows()...)
}

func autoOutput(v View) (any, bool) {
	if Get(v.Merged, Raw) {
		return "raw", true
	}

	if name := Get(v.Merged, Output); name != "" && name != "auto" {
		return nil, false
	}

	if v.Facts.StdoutTTY && Get(v.Merged, Out) == "" && !inputModeOf(v).Streaming() && !merging(v) {
		return "table", true
	}

	return "json", true
}

func outputModes() Kind {
	return Enum(
		[]string{"auto", "json", "values", "table", "raw", "csv", "tsv", "markdown"},
		map[string]string{"md": "markdown"},
	)
}

func reportRows() []Row {
	return []Row{
		{
			Name: "usage", Kind: Bool(), Modes: answering(),
			Flag: FlagSpec{Long: "usage", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "add the api usage object to the answers file and the json report"},
			}},
			Help: "add the api usage object to json output",
		},
		{
			Name: "stats", Kind: Bool(), Modes: Modes(ModeAsk, ModeStream, ModeRequest, ModeCalibrate),
			Flag: FlagSpec{Long: "stats", Commands: onRuns()},
			Help: "write a one line summary of the run to stderr at exit",
		},
		{
			Name: "delimiter", Kind: Text(), Modes: Modes(ModeStream, ModeCalibrate), Check: delimiterParses,
			Flag: FlagSpec{Long: "delimiter", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "field separator for -i csv: auto, or one character. Defaults to a comma"},
			}},
			Help: "field separator that -i csv reads and -o csv writes: auto, which needs -i csv and writes the " +
				"one it detects, or one character. Defaults to a comma",
		},
	}
}

func delimiterParses(value any) error {
	text, isText := value.(string)
	if !isText {
		return nil
	}

	if _, err := input.ParseDelimiter(text); err != nil {
		return fmt.Errorf("--delimiter %w", err)
	}

	return nil
}

func inputRows() []Row {
	return append([]Row{
		{
			Name: "input", Kind: inputModes(), Default: "text",
			Modes: Modes(ModeAsk, ModeStream, ModeRequest, ModePrintRequest, ModeCalibrate),
			Flag: FlagSpec{Long: "input", Short: "i", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {
					Help:    "required, jsonl, csv or tsv",
					Kind:    Enum([]string{"jsonl", "csv", "tsv"}, nil),
					Default: "",
				},
			}},
			Help: "text, json, jsonl, lines, csv, tsv or request",
		},
		{
			Name: "state", Kind: Text(), Modes: Modes(ModeAsk, ModePrintRequest),
			Flag: FlagSpec{Long: "state", Commands: onRoot()},
			Help: "state to evaluate, or - to read stdin",
		},
		{
			Name: "state_file", Kind: NonEmptyText(), Modes: Modes(ModeAsk, ModePrintRequest),
			Flag: FlagSpec{Long: "state-file", Commands: onRoot()},
			Help: "read the state from this file",
		},
		{
			Name: "image", Kind: Text(), Modes: recordReading(),
			Flag: FlagSpec{Long: "image", Repeat: true, Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "NAME=PATH, repeatable, puts a PNG or JPEG in every record's state under NAME. " +
					experimental},
			}},
			Help: "NAME=PATH, repeatable, puts a PNG or JPEG in the state under NAME, experimental on TypeSafe's side. " +
				"For one record stdin is read beside it only through --state -, so text joins it from --state, " +
				"--state-file or --state -. A stream reads its records from stdin and adds the image to each",
		},
		{
			Name: "image_field", Kind: NonEmptyText(), Modes: Modes(ModeStream, ModePrintRequest, ModeCalibrate),
			Flag: FlagSpec{Long: "image-field", Repeat: true, Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "repeatable, a state key whose value is a path to a PNG or JPEG, read per " +
					"record. " + experimental},
			}},
			Help: "streaming only, repeatable, a state key whose value is a path to a PNG or JPEG, read per record. " +
				"Under -i lines the line is the path. " + experimental,
		},
		{
			Name: "map", Kind: NonEmptyText(), Modes: recordReading(),
			Flag: FlagSpec{Long: "map", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "jq expression run on each record, whose result is the state sent. Map only " +
					"the text a person would read"},
			}},
			Help: "jq expression run on each record, whose result is the state sent. Object keys come out sorted. " +
				"The result may nest max-map-depth levels deep and encode to max-line-bytes, as -V lists",
		},
		{
			Name: "id", Kind: NonEmptyText(), Modes: Modes(ModeStream, ModePrintRequest, ModeCalibrate),
			Flag: FlagSpec{Long: "id", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "jq expression run on each record, whose string or number result names it"},
			}},
			Help: "streaming only, jq expression run on each record, whose string or number result names it on " +
				"every output line. It runs one record at a time as the input is read, so keep it cheap, such as " +
				"a field lookup, and -V lists the cap on its length",
		},
	}, streamRows()...)
}

func inputModes() Kind {
	return Enum([]string{"text", "json", "jsonl", "lines", "csv", "tsv", "request"}, nil)
}

func streamRows() []Row {
	return []Row{
		{
			Name: "skip_blank", Kind: Bool(), Modes: Modes(ModeStream, ModeCalibrate),
			Flag: FlagSpec{Long: "skip-blank", Commands: onRuns(), PerCommand: map[Command]CommandFlag{
				CommandCalibrate: {Help: "drop blank lines"},
			}},
			Help: "streaming only, drop blank lines with no output line",
		},
		{
			Name: "unordered", Kind: Bool(), Modes: Modes(ModeStream),
			Flag: FlagSpec{Long: "unordered", Commands: onRoot()},
			Help: "streaming only, emit records as they complete",
		},
		{
			Name: "stop_on_error", Kind: Bool(), Modes: Modes(ModeStream),
			Flag: FlagSpec{Long: "stop-on-error", Commands: onRoot()},
			Help: "streaming only, end the run at the first failure, with its exit code, or 6 for a line onesie " +
				"could not read",
		},
		{
			Name: "stop_on_assert", Kind: Bool(), Modes: Modes(ModeStream),
			Flag: FlagSpec{Long: "stop-on-assert", Commands: onRoot()},
			Help: "streaming only, end the run at the first false assertion",
		},
		{
			Name: "no_dedup", Kind: Bool(), Modes: Modes(ModeStream),
			Flag: FlagSpec{Long: "no-dedup", Commands: onRoot()},
			Help: "streaming only, ask every record even when its request repeats an earlier one",
		},
	}
}

func choiceRows() []Row {
	return append([]Row{
		{
			Name: "file", Kind: NonEmptyText(), Modes: shaping(),
			Flag: FlagSpec{Long: "file", Short: "f", Commands: onRuns()},
			Help: "question file or request body, or the name of one saved in .onesie/questions or the config dir, " +
				"or of a built in starter set",
		},
		{
			Name: "replace", Kind: Bool(), Modes: shaping(),
			Flag: FlagSpec{Long: "replace", Commands: onRuns()},
			Help: "--ask overrides an id from -f",
		},
		{
			Name: "provider", Kind: NonEmptyText(), Modes: choosing(),
			Env:     []EnvSpec{{Name: "ONESIE_PROVIDER", Order: 7, Help: providerEnvHelp}},
			Profile: []ProfileMember{{Key: "provider"}},
			Flag: FlagSpec{Long: "provider", Commands: onProfileWriters(CommandRoot), PerCommand: map[Command]CommandFlag{
				CommandRoot:          {Persistent: true},
				CommandProfileCreate: {Help: "the profile's provider, " + providers.Names() + ". Required", Kind: Text()},
				CommandProfileUpdate: {Help: "a new provider for the profile, which needs --key"},
			}},
			Help: "run on this provider without a profile, " + providers.Names() + ". Overrides ONESIE_PROVIDER and the " +
				"default profile. Refused beside --profile or ONESIE_PROFILE",
		},
		{
			Name: "profile", Kind: NonEmptyText(), Modes: choosing(),
			Env: []EnvSpec{{Name: "ONESIE_PROFILE", Order: 6, Help: profileEnvHelp}},
			Flag: FlagSpec{Long: "profile", Commands: onRoot(), PerCommand: map[Command]CommandFlag{
				CommandRoot: {Persistent: true},
			}},
			Help: "the profile a run uses, from profiles.json in the config dir. Overrides ONESIE_PROFILE and " +
				"ONESIE_PROVIDER, and otherwise the default profile runs, which onesie profile use sets",
		},
	}, endpointRows()...)
}

func endpointRows() []Row {
	return append([]Row{
		{
			Name: "model", Kind: NonEmptyText(),
			Modes:    Modes(ModeAsk, ModeStream, ModeRequest, ModePrintRequest, ModeCalibrate, ModeProfile),
			FileKey:  "model",
			InBody:   true,
			FileHelp: "The model a request body asks, unless -m names one.",
			Env: []EnvSpec{{
				Name: "TYPESAFE_DEFAULT_MODEL", Provider: providerTS, Order: 3,
				Help: "the model on typesafe when neither `-m`, a chosen profile nor a request body names one. It " +
					"beats the default profile's `model`",
			}},
			Profile: []ProfileMember{{Key: "model"}},
			Flag: FlagSpec{
				Long: "model", Short: "m", Commands: onProfileWriters(onRuns()...),
				PerCommand: map[Command]CommandFlag{
					CommandProfileCreate: {Help: profileModelHelp, NoShort: true},
					CommandProfileUpdate: {Help: profileModelHelp, Kind: Text(), NoShort: true},
				},
			},
			Help: "model override",
		},
		{
			Name: "endpoint", Kind: Endpoint(), Modes: connecting(),
			Env: []EnvSpec{
				{Name: "ONESIE_BASE_URL", Order: 4, Half: HalfBaseURL, Help: "the API root on every provider when " +
					"neither `--base-url` nor a chosen profile's `base_url` or `account_id` names one. It beats the " +
					"default profile's"},
				{Name: "TYPESAFE_BASE_URL", Provider: providerTS, Order: 5, Half: HalfBaseURL, Help: "the API root on " +
					"typesafe when neither `--base-url`, a chosen profile nor `ONESIE_BASE_URL` names one, so " +
					"`ONESIE_BASE_URL` wins. It beats the default profile's"},
				{Name: "CLOUDFLARE_ACCOUNT_ID", Provider: providerCF, Order: 2, Half: HalfAccount, Help: "the " +
					"Cloudflare account when no flag, chosen profile or base URL variable names one. It beats the default " +
					"profile's, as in What a run uses"},
			},
			Profile: []ProfileMember{{Key: "base_url", Half: HalfBaseURL}, {Key: "account_id", Half: HalfAccount}},
			Flag: FlagSpec{Long: "base-url", Commands: onProfileWriters(onRuns()...), PerCommand: map[Command]CommandFlag{
				CommandProfileCreate: {Help: profileBaseURLHelp, Kind: NonEmptyText()},
				CommandProfileUpdate: {Help: profileBaseURLHelp, Kind: Text()},
			}},
			Help: "api root override. Also ONESIE_BASE_URL",
		},
		{
			Name: "allow_http", Kind: Bool(), Modes: connecting(),
			Flag: FlagSpec{Long: "allow-http", Commands: onProfileWriters(onRuns()...), PerCommand: map[Command]CommandFlag{
				CommandProfileCreate: {Help: profileAllowHTTPHelp},
				CommandProfileUpdate: {Help: profileAllowHTTPHelp},
			}},
			Help: allowHTTPHelp,
		},
	}, answerSourceRows()...)
}

func answerSourceRows() []Row {
	return []Row{
		{
			Name: "long_context", Kind: Bool(), Modes: Modes(ModeAsk, ModeStream, ModeRequest, ModePrintRequest, ModeCalibrate),
			FileKey:  "long_context",
			InBody:   true,
			FileHelp: "Raises the context limits of the request body. Only true has an effect.",
			Env: []EnvSpec{{Name: "ONESIE_LONG_CONTEXT", Order: 13, Help: "`1` or `true` raises the context limits, " +
				"as `--long-context`. It takes the values the flag takes, and anything else exits 2 where it applies"}},
			Flag: FlagSpec{Long: "long-context", Commands: onRuns()},
			Help: "raise the context limits to 262k tokens, slower and less accurate. Also ONESIE_LONG_CONTEXT=1. " +
				experimental,
		},
		{
			Name: "mock", Kind: NonEmptyText(), Modes: answering(),
			Env: []EnvSpec{{Name: "ONESIE_MOCK", Order: 9, Help: "a file to answer from, as `--mock`. Skipped beside " +
				"`--list-models`, `-i request` and `--print-request`"}},
			Flag: FlagSpec{Long: "mock", Commands: onRuns()},
			Help: "answer from this file instead of the API. Also ONESIE_MOCK. The file is an object keyed by " +
				`question id, as {"answer":0.9,"team":"billing"}, or one line per record in the shape -o json ` +
				"writes. A lone line that carries id, line, model, usage, assert, abstain or an error object is " +
				"one record",
		},
		{
			Name: "cache", Kind: Bool(), Modes: answering(),
			Env:  []EnvSpec{{Name: "ONESIE_CACHE", Order: 10, Help: "`1` turns the response cache on, as `--cache`"}},
			Flag: FlagSpec{Long: "cache", Commands: onRuns()},
			Help: "store responses on disk and answer a repeated request from them. Also ONESIE_CACHE=1",
		},
	}
}

func requestRows() []Row {
	return []Row{
		{
			Name: "jobs", Kind: Int(1, limits.MaxJobs), Default: 1, Modes: Modes(ModeStream, ModeRequest, ModeCalibrate),
			Flag:  FlagSpec{Long: "jobs", Short: "j", Commands: onRuns()},
			Check: jobsInRange,
			Help:  "records in flight at once",
		},
		{
			Name: "timeout", Kind: Int(1, limits.MaxSeconds), Default: int(limits.DefaultAttemptTimeout.Seconds()),
			Modes: sending(),
			Flag:  FlagSpec{Long: "timeout", Commands: onRuns()},
			Check: secondsInRange("--timeout", "--timeout takes a positive number of seconds, got %d"),
			Help: "seconds per attempt, " + strconv.Itoa(int(limits.AssetAttemptTimeout.Seconds())) +
				" for a request with images unless given",
		},
		{
			Name: "retries", Kind: Int(0, limits.MaxRetries), Default: limits.DefaultRetries, Modes: sending(),
			Flag:  FlagSpec{Long: "retries", Commands: onRuns()},
			Check: retriesInRange,
			Help:  "retries after a failed attempt",
		},
		{
			Name: "max_retry_after", Kind: Int(1, limits.MaxSeconds), Default: int(limits.DefaultMaxRetryAfter.Seconds()),
			Modes: sending(),
			Flag:  FlagSpec{Long: "max-retry-after", Commands: onRuns()},
			Check: secondsInRange("--max-retry-after",
				// Zero would have to mean either never honour the header or honour it without bound, so it
				// is refused rather than given one of those meanings.
				"--max-retry-after must be a positive number of seconds, got %d"),
			Help: "most seconds to wait out a server Retry-After",
		},
	}
}

func jobsInRange(value any) error {
	jobs, isInt := value.(int)
	if !isInt {
		return nil
	}

	switch {
	case jobs < 1:
		return fmt.Errorf("-j takes a positive number of records in flight, got %d", jobs)
	case jobs > limits.MaxJobs:
		return fmt.Errorf("-j takes at most %d records in flight, got %d", limits.MaxJobs, jobs)
	default:
		return nil
	}
}

func secondsInRange(flag, notPositive string) func(any) error {
	return func(value any) error {
		seconds, isInt := value.(int)
		if !isInt {
			return nil
		}

		switch {
		case seconds < 1:
			return fmt.Errorf(notPositive, seconds)
		case seconds > limits.MaxSeconds:
			// Seconds become a time.Duration downstream, and past a point that multiplication wraps. A
			// wrap to a positive value would hand a user who asked for centuries a fraction of a second.
			return fmt.Errorf("%s takes at most %d seconds, got %d", flag, limits.MaxSeconds, seconds)
		default:
			return nil
		}
	}
}

func retriesInRange(value any) error {
	retries, isInt := value.(int)
	if !isInt {
		return nil
	}

	switch {
	case retries < 0:
		return fmt.Errorf("--retries takes a retry count of zero or more, got %d", retries)
	case retries > limits.MaxRetries:
		return fmt.Errorf("--retries takes at most %d, got %d", limits.MaxRetries, retries)
	default:
		return nil
	}
}

func calibrateRows() []Row {
	return []Row{
		{
			Name: "label", Kind: NonEmptyText(), Modes: Modes(ModeCalibrate),
			Flag: FlagSpec{Long: "label", Repeat: true, Commands: onCalibrate()},
			Help: "[ID=]EXPR, a jq expression run on the record, not on the mapped state, whose result is the " +
				"right answer for question ID. A question with none is skipped. With one question ID= may be left out",
		},
		{
			Name: "cuts", Kind: Text(), Modes: Modes(ModeCalibrate),
			Flag: FlagSpec{Long: "cuts", Commands: onCalibrate()},
			Help: "comma separated cuts between 0 and 1. Defaults to 0.05 to 0.95 in steps of 0.05",
		},
		{
			Name: "require", Kind: Text(), Modes: Modes(ModeCalibrate),
			Flag: FlagSpec{Long: "require", Repeat: true, Commands: onCalibrate()},
			Help: "[lower|upper](ID.MEASURE) OP NUMBER [at CUT], a requirement the report must meet, or calibrate " +
				"exits 1. Repeatable",
		},
		{
			Name: "offline", Kind: Bool(), Modes: Modes(ModeCalibrate),
			Flag: FlagSpec{Long: "offline", Commands: onCalibrate()},
			Help: "with --out and --resume, read every answer from the file and ask nothing, needing no key",
		},
	}
}

func profileRows() []Row {
	return []Row{
		{
			Name: "account_id", Kind: NonEmptyText(), Modes: Modes(ModeProfile),
			Flag: FlagSpec{Long: "account-id", Commands: onProfileWriters(), PerCommand: map[Command]CommandFlag{
				CommandProfileUpdate: {Kind: Text()},
			}},
			Help: providers.WithAccounts() + " only, the account id, 32 hexadecimal characters",
		},
		{
			Name: "key", Kind: Bool(), Modes: Modes(ModeProfile),
			Flag: FlagSpec{Long: "key", Commands: []Command{CommandProfileUpdate}},
			Help: "read a new key from a prompt or stdin",
		},
		{
			Name: "to_file", Kind: Bool(), Modes: Modes(ModeProfile),
			Flag: FlagSpec{Long: "file", Commands: onProfileWriters(), PerCommand: map[Command]CommandFlag{
				CommandProfileUpdate: {Help: "with --key, store the new key in profiles.json, not the OS keychain"},
			}},
			Help: "store the key in profiles.json, not the OS keychain",
		},
	}
}

func envRows() []Row {
	return []Row{
		{
			Name: "api_key", Kind: NonEmptyText(), Modes: connecting(), Env: keyVariables(),
			Profile: []ProfileMember{{Key: "api_key"}},
		},
		{
			Name: "cache_ttl", Kind: Duration(), Modes: answering(), Derive: aliasLifetime,
			Env: []EnvSpec{{Name: "ONESIE_CACHE_TTL", Order: 12, Help: "how long a cached answer for a model alias " +
				"lives, `24h` unless set"}},
			Help: "how long a cached answer for a model alias lives",
		},
		{
			Name: "config_dir", Kind: NonEmptyText(), Modes: choosing(),
			Env: []EnvSpec{{Name: "ONESIE_CONFIG_DIR", Order: 8, Help: "where `profiles.json` and the saved " +
				"question files live"}},
			Help: "where profiles.json and the saved question files live",
		},
		{
			Name: "cache_dir", Kind: NonEmptyText(), Modes: answering(),
			Env:  []EnvSpec{{Name: "ONESIE_CACHE_DIR", Order: 11, Help: "where the response cache lives"}},
			Help: "where the response cache lives",
		},
	}
}

func aliasLifetime(v View) (any, bool) {
	if v.Merged.Source(CacheTTL.Name()) == SourceDefault || Get(v.Merged, CacheTTL) != 0 {
		return nil, false
	}

	return NoAliasTTL, true
}

func keyVariables() []EnvSpec {
	var variables []EnvSpec

	for _, provider := range onesie.Providers() {
		variables = append(variables, EnvSpec{
			Name: provider.EnvAPIKey(), Provider: provider.Name(), Order: 1,
			Help: "the key for each provider. It beats the default profile's key, and a chosen profile's beats it, " +
				"as in What a run uses",
		})
	}

	return variables
}

func onRoot() []Command {
	return []Command{CommandRoot}
}

func onCalibrate() []Command {
	return []Command{CommandCalibrate}
}

func onRuns() []Command {
	return []Command{CommandRoot, CommandCalibrate}
}

func onProfileWriters(also ...Command) []Command {
	return append(also, CommandProfileCreate, CommandProfileUpdate)
}

func shaping() ModeSet {
	return Modes(ModeAsk, ModeStream, ModePrintRequest, ModePrintQuestions, ModeCalibrate)
}

func policing() ModeSet {
	return Modes(ModeAsk, ModeStream, ModePrintRequest, ModePrintQuestions)
}

func answering() ModeSet {
	return Modes(ModeAsk, ModeStream, ModeCalibrate)
}

func recordReading() ModeSet {
	return Modes(ModeAsk, ModeStream, ModePrintRequest, ModeCalibrate)
}

func choosing() ModeSet {
	return Modes(ModeAsk, ModeStream, ModeRequest, ModePrintRequest, ModePrintQuestions, ModeListModels,
		ModeCalibrate, ModeProfile)
}

func connecting() ModeSet {
	return Modes(ModeAsk, ModeStream, ModeRequest, ModeListModels, ModeCalibrate, ModeProfile)
}

func sending() ModeSet {
	return Modes(ModeAsk, ModeStream, ModeRequest, ModeListModels, ModeCalibrate)
}
