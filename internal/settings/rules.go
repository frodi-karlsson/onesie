package settings

import (
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	assertName     Name = "assert"
	abstainIfName  Name = "abstain_if"
	imageName      Name = "image"
	imageFieldName Name = "image_field"
)

// Rules returns every cross setting rule, in the order Validate runs them.
func Rules() []Rule {
	// Calibrate's needs lead, so a calibration is told what it lacks first. The mode rules come next,
	// so a flag a mode makes meaningless is named against the mode, and a dry run is refused before
	// the output rules. The rules on one record and on a stream come before the plan rules, so a
	// streaming -q reports the streaming message, and a second state is refused last.
	return append(calibrateRules(),
		noQuestionRule(), listModelsRule(), resumeRule(), pruneRule(), dryRunAndRequestRule(), delimiterRule(),
		abstainNeedsAssertRule(), rawAndOutputRule(), quietWithMarkdownRule(), stateAndStateFileRule(),
		replaceNeedsFileRule(), mergeOutputRule(), mergeKeyWrapsRule(), delimitedOutputRule(), quietWithUsageRule(),
		usageWritesAnswersRule(), singleRecordRule(), streamStateRule(), quietOnStreamRule(),
		tableOnStreamRule(), stopOnAssertNeedsAssertRule(), fallbackColumnsRule(), lineFieldRule(),
		bodyPolicyRule(), orphansRule(), questionsValidRule(), quietNeedsAGateRule(), rawNeedsOneQuestionRule(),
		bodyStateRule(),
	)
}

// Validate runs rules in order on v. It returns the warnings and notices of the rules it reached, and
// the first refusal.
func Validate(v View, rules []Rule) (warnings []string, notices []Notice, err error) {
	for _, rule := range rules {
		if rule.Warn != nil {
			warnings = append(warnings, rule.Warn(v)...)
		}

		if rule.Inspect != nil {
			found, refusal := rule.Inspect(v)
			if warnings = append(warnings, found...); refusal != nil {
				return warnings, notices, refusal
			}
		}

		if rule.Check != nil {
			if err = rule.Check(v); err != nil {
				return warnings, notices, err
			}
		}

		if rule.Notice != nil {
			notices = append(notices, rule.Notice(v)...)
		}
	}

	return warnings, notices, nil
}

// Rule is one cross setting check. Reads names the rows it reads, Check refuses, Warn and Notice
// report without refusing, and Inspect warns and refuses in one pass.
type Rule struct {
	ID      string
	Reads   []Name
	Check   func(View) error
	Notice  func(View) []Notice
	Warn    func(View) []string
	Inspect func(View) ([]string, error)
}

// Notice is a line a reporting rule writes to stderr once validation passes.
type Notice struct {
	Setting Name
	Text    string
}

// NewView returns what the rules read about one run. built is nil for a run that builds no plan,
// such as --list-models and -i request.
func NewView(merged *Merged, built *plan.Plan, facts Facts) View {
	return View{Merged: merged, Plan: built, Facts: facts}
}

// View is what a rule or a derived default reads: the merged settings, the plan and the facts of a
// run.
type View struct {
	Merged *Merged
	Plan   *plan.Plan
	Facts  Facts
}

// Facts are what a rule or a derived default reads about a run that no setting holds: the command,
// the question file's gates and state, the group flags in the order typed, the positional question,
// calibrate's -o and whether it was typed, and whether stdout is a terminal.
type Facts struct {
	Command                   Command
	FileAssert, FileAbstainIf string
	BodyState                 bool
	Events                    []argv.Event
	Positional                string
	Report                    string
	ReportSet                 bool
	StdoutTTY                 bool
}

func typed(v View, name Name) bool {
	return v.Merged.Source(name) == SourceFlag
}

func listOf(v View, name Name) []string {
	if v.Merged == nil {
		return nil
	}

	if list, ok := v.Merged.values[name].([]string); ok {
		return list
	}

	return nil
}

func inputModeOf(v View) input.Mode {
	mode, err := input.ParseMode(Get(v.Merged, Input))
	if err != nil {
		return input.Text
	}

	return mode
}

func inputName(v View) string {
	// Reachable only by passing -i with an empty value, since the flag defaults to text. Every
	// message that names the mode would otherwise read -i with nothing after it.
	if name := Get(v.Merged, Input); name != "" {
		return name
	}

	return "text"
}

func merging(v View) bool {
	return Get(v.Merged, MergeInput) || Get(v.Merged, MergeKey) != ""
}

func mergeName(v View) string {
	switch {
	case Get(v.Merged, MergeInput):
		return "--merge"
	case Get(v.Merged, MergeKey) != "":
		return "--merge-key"
	default:
		return ""
	}
}

func hasAssert(v View) bool {
	return len(listOf(v, assertName)) > 0 || v.Facts.FileAssert != ""
}

func hasAbstainIf(v View) bool {
	return len(listOf(v, abstainIfName)) > 0 || v.Facts.FileAbstainIf != ""
}

func assertFlag(v View) string {
	if len(listOf(v, assertName)) == 0 && v.Facts.FileAssert != "" {
		return plan.Spelling(plan.OriginFile, "--assert")
	}

	return "--assert"
}

func abstainFlag(v View) string {
	if len(listOf(v, abstainIfName)) == 0 && v.Facts.FileAbstainIf != "" {
		return plan.Spelling(plan.OriginFile, "--abstain-if")
	}

	return "--abstain-if"
}

func eventGiven(v View, flag string) bool {
	for _, event := range v.Facts.Events {
		if event.Name == flag {
			return true
		}
	}

	return false
}

func mockSpelled(v View) string {
	switch v.Merged.Source(Mock.Name()) {
	case SourceFlag:
		return "--mock"
	case SourceEnv:
		return "ONESIE_MOCK"
	default:
		return ""
	}
}

func mockAdvice(v View, flag string) string {
	if mockSpelled(v) == "--mock" {
		return "Drop one"
	}

	return "Unset it or drop " + flag
}
