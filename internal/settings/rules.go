package settings

import (
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

const (
	assertName     settingrow.Name = "assert"
	abstainIfName  settingrow.Name = "abstain_if"
	imageName      settingrow.Name = "image"
	imageFieldName settingrow.Name = "image_field"
)

// Rules returns every cross setting rule, in the order Validate runs them.
func Rules() []Rule {
	// Calibrate's needs lead, so a calibration is told what it lacks first. The mode rules come next,
	// so a flag a mode makes meaningless is named against the mode, and a dry run is refused before
	// the output rules. The rules on one record and on a stream come before the plan rules, so a
	// streaming -q reports the streaming message. A second state and the output -q drops are refused
	// last, so a command line refused for another reason keeps that reason.
	return append(calibrateRules(),
		noQuestionRule(), listModelsRule(), resumeRule(), pruneRule(), dryRunAndRequestRule(), cacheBesideMockRule(), delimiterRule(),
		abstainNeedsAssertRule(), quietWithMarkdownRule(),
		replaceNeedsFileRule(), forSelectsRule(), fileQuestionShapeRule(), mergeOutputRule(), mergeKeyWrapsRule(), delimitedOutputRule(), quietWithUsageRule(),
		usageWritesAnswersRule(), singleRecordRule(), streamStateRule(), quietOnStreamRule(),
		tableOnStreamRule(), stopOnVerdictNeedsGateRule(), fallbackColumnsRule(), lineFieldRule(),
		policyBindingRule(), questionsValidRule(), quietNeedsAGateRule(), rawNeedsOneQuestionRule(),
		bodyStateRule(), quietWritesNothingRule(), givenTwiceRule(),
	)
}

// Validate runs rules in order on v. It returns the warnings and notices of the rules it reached, and
// the first refusal.
func Validate(v View, rules []Rule) (warnings []string, notices []Notice, err error) {
	for _, rule := range rules {
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

// Rule is one cross setting check. Check refuses, Notice reports without refusing,
// and Inspect warns and refuses in one pass.
type Rule struct {
	ID      string
	Check   func(View) error
	Notice  func(View) []Notice
	Inspect func(View) ([]string, error)
}

// NewView returns what the rules read about one run. built is nil for a run that builds no plan,
// such as --list-models and -i request.
func NewView(merged *Merged, built *plan.Plan, facts Facts) View {
	view := View{Merged: merged, Plan: built, Facts: facts}
	if facts.Command == settingrow.CommandCalibrate {
		cuts, cutsErr := calibrationCuts(view)
		requirements, requirementsErr := calibrationRequirements(view)
		budgets, budgetsErr := calibrationBudgets(view)
		view.cutsError, view.requirementsError, view.budgetsError = cutsErr, requirementsErr, budgetsErr
		if cutsErr == nil && requirementsErr == nil && budgetsErr == nil {
			view.Calibration = CalibrationArguments{
				Cuts: cuts, Requirements: requirements, Compare: listOf(view, compareName), Budgets: budgets,
			}
		}
	}
	return view
}

// View is what a rule or a derived default reads: the merged settings, the plan and the facts of a
// run.
type View struct {
	Merged            *Merged
	Plan              *plan.Plan
	Facts             Facts
	Calibration       CalibrationArguments
	cutsError         error
	requirementsError error
	budgetsError      error
}

// CalibrationArguments holds the syntax results prepared for one calibration invocation.
type CalibrationArguments struct {
	Cuts         []float64
	Requirements []calibrate.Requirement
	// Compare names the answers files to compare, the first being the baseline, and Budgets the
	// false alarm budgets their report reads catches at.
	Compare []string
	Budgets []float64
}

// Facts are what a rule or a derived default reads about a run that no setting holds: the command,
// the question file's path as messages give it, whether that file holds a state, the group flags in
// the order typed, the positional question, whether stdout is a terminal, and the command line as
// typed, Args, with the flags Given counts.
type Facts struct {
	Command    settingrow.Command
	FileName   string
	BodyState  bool
	Events     []argv.Event
	Positional string
	StdoutTTY  bool
	Given      *Given
	Args       []string
}

func typed(v View, name settingrow.Name) bool {
	return v.Merged.Source(name) == SourceFlag
}

func listOf(v View, name settingrow.Name) []string {
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
	return len(listOf(v, assertName)) > 0
}

func hasAbstainIf(v View) bool {
	return len(listOf(v, abstainIfName)) > 0
}

func abstainFlag(v View) string {
	if v.Merged.Source(abstainIfName) == SourceFile {
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
