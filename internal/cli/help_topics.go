package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/delimiter"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/providers"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

const helpLabelledRecord = `{"id":"a","text":"The server is down","urgent":true}`

func helpTopics(command settingrow.Command) []helpTopic {
	middle := helpTopic{name: "decisions", title: "Decisions", body: helpDecisions(), overview: 4}
	output := helpTopic{name: "output", title: "Output", body: helpOutput(command), overview: 3}
	codes := rootExitCodes
	if command == settingrow.CommandCalibrate {
		middle = helpTopic{name: "evaluation", title: "Evaluation", body: helpEvaluation(), overview: 3}
		output.overview = 4
		codes = calibrateExitCodes
	}
	return []helpTopic{
		{name: "asking", title: "Asking", body: helpAsking(command), overview: 1},
		{name: "input", title: "Input", body: helpInput(command), overview: 2},
		middle,
		output,
		{name: "connections", title: "Connections", body: helpConnections(), overview: 5},
		{name: "execution", title: "Execution", body: helpExecution(command), overview: 6},
		{name: "exit-codes", codes: codes},
	}
}

type helpTopic struct {
	name     string
	title    string
	body     string
	overview int
	codes    []exitCodeLine
}

func helpAsking(command settingrow.Command) string {
	body := "A positional question has the id answer. By default a question asks for a yes or no probability. " +
		"Use --pick for a choice, --rate for ordered levels, or --box for image edges. A rate score is the level index " +
		"averaged over p, from 0, and norm is score over the top index, from 0 to 1. Box edges are left, top, right and bottom, each from 0 to 1. " +
		"Box is experimental on TypeSafe's side. See Input for image restrictions.\n\n" +
		"Question flags apply to the preceding --ask or --for. Flags before any group apply to the one question asked, whether positional, a lone --ask or a file with one question. Beside several questions they need --for ID. " +
		"--ask NAME=QUESTION opens a named group and may repeat. --for ID selects a question from --file. " +
		"Root runs issue a notice for each setting overriding a selected file question. " +
		"Repeated --pick adds options and repeated --rate adds levels after earlier ones. Together they replace the file question's options or levels. " +
		"--desc KEY=TEXT replaces only that option, level, yes or no description. A rate needs every level described or none. --sep changes the separator for following --pick and --rate. " +
		"--for may repeat and keeps the wording and settings that no flag changes. It needs a file and an existing id and refuses --replace. Flags cannot change a file question shape.\n\n" +
		"--file accepts a question file, a request body, a path or a saved name. Saved names are looked up in .onesie/questions, " +
		"then the config directory, then built in starter sets. --ask needs --replace to replace an id from the file. " +
		"This replaces the question, while --for changes selected settings.\n\n" +
		"A question that begins with a dash needs -- before it, with all flags first, as in onesie -o json -- '-is this urgent'. " +
		"Use the same escape for a one word question close to a command name, since onesie questoins exits 2 and suggests questions. "
	if command == settingrow.CommandRoot {
		body += "--print-questions exports a question file whose first line names the schema from --print-schema. Exports send no requests. On a request body, --for accepts only threshold, minimum confidence and fallback settings."
	} else {
		body += "On calibration, --for accepts only --pick, --rate, --desc and --sep. Box questions cannot be calibrated."
	}
	return body
}

func helpInput(command settingrow.Command) string {
	intro := "Text and JSON read one state. JSONL, lines, CSV and TSV stream records from stdin. Request input sends API shaped bodies as written. " +
		"--state supplies text instead of stdin, --state-file reads a file, and --state - explicitly reads stdin. "
	original := "The original record remains available for merged output."
	separator := "--delimiter auto needs CSV input, detects its separator and uses it for CSV output. "
	images := "For one record, stdin is read beside an image only through --state -. Text can also join it through --state or --state-file. Images cannot be mixed with request input. "
	lineImage := "Under lines input the line itself is the path. "
	if command == settingrow.CommandCalibrate {
		intro = "Calibration requires JSONL, CSV or TSV records on stdin. "
		original = "Labels are selected from the original record."
		separator = "--delimiter auto detects the separator of CSV input. "
		images = ""
		lineImage = ""
	}
	return intro +
		"--map runs a jq expression on each original record and sends its result as the state. Map only the text a person would read. " +
		"Object keys come out sorted. The result may nest max-map-depth levels deep and encode to max-line-bytes, as version lists. " + original + "\n\n" +
		"--id runs one record at a time as input is read. Its result must be a string or number. Keep it cheap, such as a field lookup. " +
		"version lists max-id-bytes. --skip-blank drops blank lines without emitting a result. " +
		"The default field separator is " + strconv.QuoteRune(delimiter.DefaultComma) + ". " + separator +
		"An explicit delimiter takes one character. TSV uses tabs.\n\n" +
		"--image NAME=PATH adds a PNG or JPEG under NAME. " + images + "Streams read stdin and add the fixed image to every record. " +
		"--image-field reads a PNG or JPEG path from a state key per record. " + lineImage +
		"Image support is experimental on the TypeSafe side. See version for image, file, token and model limits."
}

func helpDecisions() string {
	return "--threshold adds a boolean decision to a yes or no probability. A false decision alone does not change the exit code. " +
		"Without --assert, quiet mode judges one question on one record. Yes or no uses the threshold, defaulting to " + strconv.FormatFloat(quietDefaultThreshold, 'f', -1, 64) + ". " +
		"Pick and rate need both --min-confidence and --fallback. Quiet mode refuses streams.\n\n" +
		"--min-confidence applies only to pick and rate, needs --fallback and takes a value between 0 and 1. " +
		"Confidence is about 1 when p sits on one answer. Yes or no fallback values must be boolean spellings. " +
		"Pick and rate can fall back to a review destination outside their options. JSON retains the model answer in answer.value when available, " +
		"with the policy result in answer.decision. Low confidence has fallback reason low_confidence and an error has reason error. " +
		"A fallback does not turn an error into an answer.\n\n" +
		"Each record ends in one verdict: answered with no gate, pass, fail, unsure or error. --assert is a boolean expression over the record. Repeated assertions are combined with and and replace the file's assert. " +
		"Acceptance is checked first. Only when it fails is --abstain-if checked. Repeated abstain expressions combine with and, " +
		"need --assert and replace the file's abstain_if. A false assertion is a fail and exits 1. When --abstain-if holds instead, the record is unsure and exits 7. " +
		"Quiet beside --assert only suppresses output.\n\n" +
		"An --assert path is a question id, a dot and a field, as in urgent.value. Compare with ==, !=, <, <=, > and >=, " +
		`test a string with in ["a", "b"], and combine with and, or, not and parentheses. Strings take double quotes. ` +
		"An id written bare holds only letters, digits and _, starts with a letter or _, and is not and, or, not, in, true or false. " +
		`Write any other id in brackets and double quotes, as in ["needs review"].value, and an option the same way after p, as in team.p["on call"]. ` +
		"min, max, sum and avg take numbers and nest, as in max(a.value, b.value) < 0.2. " +
		"There are no arithmetic operators, so weight answers with jq. " +
		"A box has no value, so gate it on its edges or its confidence, as in mug.confidence >= 0.6.\n\n" +
		"Streams print every fail and unsure too. --stop-on=fail prints the first fail before stopping, and --stop-on=unsure the first unsure. " +
		"Both need --assert, and unsure also needs --abstain-if. " +
		"Earlier errors still affect the aggregate exit code. See Asking for question setting scope. " +
		"Use onesie help calibrate to choose cuts from labelled evidence. The export example uses an illustrative cut."
}

func helpOutput(command settingrow.Command) string {
	if command == settingrow.CommandCalibrate {
		return "--output selects the report format, table, json or auto. Auto means table. " +
			"--out selects a separate answer file, written as JSON lines, and does not redirect the report. " +
			"--usage adds API usage to both the answer file and JSON report. --stats writes a one line run summary to stderr at exit. " +
			"See Execution for answer file resume and pruning."
	}
	return "Auto uses a table only for one result on a terminal, without --merge or --out. " +
		"It uses JSON when redirected, for streams, for merged records and for --out files. --raw selects raw output. " +
		"Raw prints the policy decision when present, otherwise the answer value, and does not preserve the original record.\n\n" +
		"--merge keeps the input fields and places answer metadata under answers by default. --merge-key selects another location and implies --merge. " +
		"JSON preserves model values, decisions and errors. A request error remains in the output even when a fallback supplies a decision. " +
		"--out writes answers to a file instead of stdout. See Execution for resume and pruning. " +
		"--usage adds API usage to JSON output. --stats writes a one line run summary to stderr at exit. " +
		"--version prints the version, commit, build date and built in limits, as one JSON object with -o json."
}

func helpConnections() string {
	profileEnv, providerEnv := settingEnv(setting.Profile.Name()), settingEnv(setting.Provider.Name())
	baseURLEnv, modelEnv := settingEnv(setting.APIEndpoint.Name()), settingEnv(setting.Model.Name())
	return "onesie is a Unix filter over the System One API, served by " + providers.Names() + ". " +
		"A profile in profiles.json holds a provider, a key in the OS keychain or file, and optionally a model and base URL or " + providers.WithAccounts() + " account. " +
		"onesie profile create stores one and onesie profile use makes it the default.\n\n" +
		"A run uses the profile named by --profile, then " + profileEnv + ", then the default profile. --profile overrides " + providerEnv + ". " +
		"--provider runs without a profile and overrides " + providerEnv + " and the default profile. " +
		"It is refused beside --profile or " + profileEnv + ". " + providerEnv + " beside " + profileEnv + " is also refused. " +
		"With no profiles, typesafe runs directly. Provider keys come from " + providers.KeyVariables() + ". " +
		"The chosen profile key and endpoint beat the environment. Provider variables beat the default profile. " +
		"--base-url wins over a chosen profile, then " + baseURLEnv + ", then the provider base URL or account variable, then the default profile. " +
		baseURLPrecedence(settingrow.Lookup(setting.APIEndpoint.Name())) + "\n\n" +
		"A key goes only with its own endpoint. A custom profile endpoint takes only the key from that profile. A custom URL from a variable takes only the provider key variable. " +
		"A typed --base-url takes the chosen profile key or provider variable, never the default profile key. A keyless profile reads no key variable. " +
		"No provider uses another provider key. The base URL and account are one setting, taken whole from the highest source.\n\n" +
		"On " + providers.WithAccounts() + " an account comes from the profile, " + providers.AccountVariables() + " or a base URL holding it. " +
		"--model overrides the chosen profile or request body. " + modelEnv + " applies when none names a model and beats the model in the default profile. " +
		"A custom base URL needs no key and sends none when none resolves. Plain HTTP to a host other than this machine needs --allow-http, " +
		"which permits sending the request and any API key over it. Without that flag the run exits 2 before a request. " +
		"A profile storing such a URL needs --allow-http on every run too."
}

func baseURLPrecedence(endpoint settingrow.Row, found bool) string {
	if !found {
		return ""
	}
	var order []string
	for _, variable := range endpoint.Env {
		if variable.Half == settingrow.HalfBaseURL && variable.Provider != "" && !slices.Contains(order, variable.Provider) {
			order = append(order, variable.Provider)
		}
	}
	sentences := make([]string, 0, len(order))
	for _, provider := range order {
		var names []string
		for _, variable := range endpoint.Env {
			if variable.Half == settingrow.HalfBaseURL && (variable.Provider == "" || variable.Provider == provider) {
				names = append(names, variable.Name)
			}
		}
		sentences = append(sentences, "On "+provider+", "+strings.Join(names, " beats ")+".")
	}
	return strings.Join(sentences, " ")
}

func helpExecution(command settingrow.Command) string {
	common := fmt.Sprintf("--jobs bounds records in flight. --timeout is seconds per attempt. Requests with images default to %s seconds unless given. ", strconv.FormatFloat(limits.AssetAttemptTimeout.Seconds(), 'f', -1, 64)) +
		"Retries happen after failed attempts, bounded by --retries. --max-retry-after caps seconds spent waiting for a server Retry-After. " +
		"--long-context raises context limits, is slower and less accurate, and is experimental on TypeSafe's side. version reports the built in limits.\n\n" +
		"--cache, or " + settingEnv(setting.Cache.Name()) + "=1, stores successful responses on disk and reuses repeated requests. onesie cache reports entries and onesie cache clear empties them. "
	if command == settingrow.CommandRoot {
		common += "Streams normally preserve input order. --unordered emits completed records immediately. " +
			"--no-dedup asks every record even when its request repeats, independently of the disk cache. " +
			"--stop-on=error ends at the first error with its exit code, or 6 for an unreadable record. "
	}
	resume := "--resume needs --out. Without --id, a stream continues after the last complete line. With --id it skips answered ids. "
	if command == settingrow.CommandCalibrate {
		resume = "Calibration resume needs --out and --id, and skips answered ids. "
	}
	common += "\n\n" + resume +
		"A record whose content changes but whose id does not keeps its old answer. The file is rewritten in input order. " +
		"Answered ids absent from input stay after them unless --prune drops them.\n\n" +
		"--mock FILE, or " + settingEnv(setting.Mock.Name()) + "=FILE, answers from fixtures without a key or network, so gate scripts can be tested for free. " +
		`A mock file is one JSON object keyed by question id, which answers every record, as in {"urgent":0.9}, or one line per record in the shape -o json writes, so the --out file of a real run replays. ` +
		"Lines match records by id under --id and by position otherwise. Blank lines are refused, apart from those at the end. " +
		`An entry {"error":503} makes the record an error, as that status does, {"error":"timeout"} as a timeout and {"error":"connection"} as a dropped connection. ` +
		"Each is retried as a live failure is, without the wait. " +
		"--print-request writes API shaped request bodies and sends none."
	if command == settingrow.CommandRoot {
		common += " Question export writes configuration rather than answers. Mocks exercise execution and policy behavior. Request previews and question export do not execute those policies. Mocks are skipped beside --list-models, request input, --print-request and --print-questions."
	}
	if command == settingrow.CommandCalibrate {
		common += "\n\nChanging a label or --cuts reuses stored answers. " +
			"A plain stream can resume the file with the same questions, model, input format, --map and --id, with -o json and no gate or merge. " +
			"Offline reads the saved answers without rewriting them. It validates recorded evidence and does not detect new live model behavior."
	}
	return common
}

func helpEvaluation() string {
	cuts := calibrate.DefaultCuts()
	values := make([]string, len(cuts))
	for i, cut := range cuts {
		values[i] = strconv.FormatFloat(cut, 'f', -1, 64)
	}
	return "Calibration compares answers with labels from the original record, not the mapped state, and prints cut, agreement and confusion tables. " +
		"It never picks the cut. A --map selecting the label flatters the question. --label can omit ID= with one question. " +
		"Default cuts: " + strings.Join(values, ", ") + ".\n\n" +
		"A yes or no label is true, false, yes, no, y, n, 1 or 0 in any case. Pick and rate labels are declared option or level names, " +
		"with surrounding spaces ignored, or a number whose text names one, so 4 matches --rate 1,2,3,4,5. " +
		"null, no result or a string of only spaces leaves that record unlabelled for the question. A record no question labels is not asked. " +
		"A JSONL record can be:\n\n    " + helpLabelledRecord + "\n\nCSV can use:\n\n    id,text,urgent\n    a,The server is down,yes\n\n" +
		"A question with no --label is skipped, omitted from requests and the report, and named on stderr. At least one question needs a label. " +
		"A label naming no question or a requirement reading a skipped question exits 2. Resume into a file answering every question still asks skipped ones so the file stays whole. " +
		"Box questions cannot be calibrated. A question file's assert, abstain_if, threshold, min_confidence and fallback are ignored. The corresponding root flags are refused.\n\n" +
		"A yes or no cut flags a record when value is at least the cut. Pick and rate tables have a row per name. " +
		"found is the share labelled with a name that were picked as it. right when picked is the share picked as a name carrying it as their label.\n\n" +
		"Each --require reads [lower|upper](ID.MEASURE) OP NUMBER [at CUT]. MEASURE is catches, false_alarms, right_when_flagged or auc for yes or no, " +
		"agreement for pick and rate, and within_one for rate. OP is >=, >, <= or <. NUMBER is a fraction from 0 to 1. " +
		"lower and upper compare an end of the 95 percent interval instead of the value, so 16 of 16 fails lower(x.catches) >= 0.9. " +
		"A yes or no measure other than auc needs a cut. at CUT names it, at abstain takes it from the file's abstain_if, " +
		"otherwise the file's assert supplies it when comparing ID.value < X or ID.value >= X. A missing cut exits 2. " +
		"The report still prints when requirements fail, each failure is named on stderr and the run exits 1.\n\n" +
		"--offline needs --out and --resume, reads every answer from the file and asks nothing. It needs no key and never rewrites the file. " +
		"A missing answer, missing file or fingerprint mismatch exits 2. The first example creates the answer file used by the second. " +
		"The example requirement is illustrative. Offline replay checks saved evidence, not new live model behavior."
}

func helpExamples(command settingrow.Command) []helpExample {
	if command == settingrow.CommandCalibrate {
		return []helpExample{
			{id: "calibrate", title: "Compare answers with labelled examples", places: []string{"overview", "evaluation"}, args: []string{"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".text", "--label", "urgent=.urgent", "--id", ".id", "--out", "answers.jsonl", "--resume"}, inputFile: "labelled.jsonl"},
			{id: "offline", title: "Check the saved answers without asking again", places: []string{"overview", "evaluation"}, args: []string{"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".text", "--label", "urgent=.urgent", "--id", ".id", "--out", "answers.jsonl", "--resume", "--offline", "--require", "urgent.catches >= 0.9 at 0.5"}, inputFile: "labelled.jsonl"},
		}
	}
	return []helpExample{
		{id: "ask", title: "Get a yes or no probability", places: []string{"overview"}, args: []string{"is this urgent"}, stdin: "The server is down"},
		{id: "pick", title: "Choose a team", places: []string{"overview"}, args: []string{"which team owns this", "--pick", "billing,support", "--state", "Please refund the duplicate charge"}},
		{id: "enrich", title: "Label records while keeping their fields", places: []string{"overview"}, args: []string{"is this urgent", "-i", "jsonl", "--map", ".text", "--merge"}, inputFile: "tickets.jsonl"},
		{id: "question-export", title: "Inspect a policy without asking the model", places: []string{"decisions"}, args: []string{"--ask", "urgent=is this urgent", "--threshold", "0.5", "--print-questions"}},
		{id: "connection", title: "Create a connection", places: []string{"footer", "connections"}, args: []string{"profile", "create", "NAME", "--provider", "typesafe"}},
	}
}

type helpExample struct {
	id        string
	title     string
	places    []string
	args      []string
	stdin     string
	inputFile string
}
