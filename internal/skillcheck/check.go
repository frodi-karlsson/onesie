// Package skillcheck runs every rule's bad and good example in a skill through a jev dry run, per
// spec section 6.1. A good passes unless it carries good_fails, and a bad fails unless it carries
// bad_passes.
package skillcheck

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/frodi-karlsson/jev-cli/internal/skillgen"
)

// Bounds one example. A dry run makes no network call and should return instantaneously, so a
// jev that hangs is treated as a failure rather than left to stall CI.
const runTimeout = 10 * time.Second

// Check runs every rule's bad and good example under root through runner. An example that cannot
// be parsed as a jev invocation is skipped rather than counted as a failure, since a jq filter or
// a rule about model behaviour carries no command a dry run can replay. Every skip is reported
// with why, so a skill drifting to unverifiable examples is visible rather than merely countable.
func Check(ctx context.Context, root string, runner *Runner) (Report, error) {
	found, err := skillgen.Skills(root)
	if err != nil {
		return Report{}, err
	}

	report := Report{Skills: len(found)}

	for _, f := range found {
		for _, rule := range f.Skill.Rules {
			if rule.Bad != "" {
				if err := checkRule(
					ctx, runner, f.Skill.Name, rule.ID, "bad", rule.Bad, rule.BadPasses, &report,
				); err != nil {
					return Report{}, err
				}
			}

			if rule.Good != "" {
				if err := checkRule(
					ctx, runner, f.Skill.Name, rule.ID, "good", rule.Good, !rule.GoodFails, &report,
				); err != nil {
					return Report{}, err
				}
			}
		}
	}

	return report, nil
}

// Report tallies one run of Check across every skill.
type Report struct {
	Skills   int
	Checked  int
	Skipped  []Skip
	Failures []string
}

// Passed reports whether every checked example behaved as its rule declared.
func (r Report) Passed() bool {
	return len(r.Failures) == 0
}

// Skip records one example Check declined to run, and why, so a skill drifting to examples the
// checker cannot verify is visible rather than only countable.
type Skip struct {
	Skill   string
	RuleID  string
	Kind    string
	Command string
	Reason  string
}

func checkRule(
	ctx context.Context, runner *Runner, skill, ruleID, kind, command string, wantPass bool,
	report *Report,
) error {
	args, reason, err := prepare(command)
	if err != nil {
		return fmt.Errorf("jev: skill '%s' rule '%s' %s example: %w", skill, ruleID, kind, err)
	}

	if reason != "" {
		report.Skipped = append(report.Skipped, Skip{
			Skill: skill, RuleID: ruleID, Kind: kind, Command: command, Reason: reason,
		})

		return nil
	}

	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	exitCode, stderr, err := runner.run(runCtx, args)
	if err != nil {
		return fmt.Errorf("jev: skill '%s' rule '%s' %s example: %w", skill, ruleID, kind, err)
	}

	report.Checked++

	if passed := exitCode == 0; passed != wantPass {
		report.Failures = append(report.Failures,
			outcomeMessage(skill, ruleID, kind, wantPass, exitCode, args, stderr))
	}

	return nil
}

func prepare(command string) (args []string, reason string, err error) {
	tokens, reason := tokenize(command)
	if reason != "" {
		return nil, reason, nil
	}

	return dryRunArgs(tokens)
}

func outcomeMessage(skill, ruleID, kind string, wantPass bool, exitCode int, args []string, stderr string) string {
	var clause string

	switch {
	case kind == "good" && wantPass:
		clause = fmt.Sprintf("good example failed the dry run with exit %d", exitCode)
	case kind == "good":
		clause = "good example marked good_fails was supposed to fail the dry run but exited 0"
	case wantPass:
		clause = fmt.Sprintf("bad example marked bad_passes was supposed to pass the dry run but exited %d", exitCode)
	default:
		clause = "bad example was supposed to fail the dry run but exited 0"
	}

	message := fmt.Sprintf("jev: skill '%s' rule '%s': %s\n  ran: jev %s",
		skill, ruleID, clause, strings.Join(args, " "))

	if stderr = strings.TrimSpace(stderr); stderr != "" {
		message += "\n  " + stderr
	}

	return message
}
