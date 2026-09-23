// Package skillcheck runs every rule's bad and good example in a skill through a jev dry run, per
// spec section 6.1. A good must pass, a bad must fail, and a good marked good_fails must fail.
package skillcheck

import (
	"context"
	"fmt"

	"github.com/frodi-karlsson/jev-cli/internal/skillgen"
)

// Check runs every rule's bad and good example under root through runner. An example that cannot
// be parsed as a jev invocation is skipped rather than counted as a failure, since a jq filter or
// a rule about model behaviour carries no command a dry run can replay.
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
					ctx, runner, f.Skill.Name, rule.ID, "bad", rule.Bad, false, &report,
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
	Skipped  int
	Failures []string
}

// Passed reports whether every checked example behaved as its rule declared.
func (r Report) Passed() bool {
	return len(r.Failures) == 0
}

func checkRule(
	ctx context.Context, runner *Runner, skill, ruleID, kind, command string, wantPass bool,
	report *Report,
) error {
	args, ok := prepare(command)
	if !ok {
		report.Skipped++

		return nil
	}

	exitCode, err := runner.run(ctx, args)
	if err != nil {
		return fmt.Errorf("jev: skill '%s' rule '%s': %w", skill, ruleID, err)
	}

	report.Checked++

	if passed := exitCode == 0; passed != wantPass {
		report.Failures = append(report.Failures, outcomeMessage(skill, ruleID, kind, wantPass, exitCode))
	}

	return nil
}

func prepare(command string) ([]string, bool) {
	tokens, ok := tokenize(command)
	if !ok {
		return nil, false
	}

	return dryRunArgs(tokens)
}

func outcomeMessage(skill, ruleID, kind string, wantPass bool, exitCode int) string {
	if wantPass {
		return fmt.Sprintf(
			"jev: skill '%s' rule '%s': good example failed the dry run with exit %d",
			skill, ruleID, exitCode)
	}

	if kind == "bad" {
		return fmt.Sprintf(
			"jev: skill '%s' rule '%s': bad example was supposed to fail the dry run but exited 0",
			skill, ruleID)
	}

	return fmt.Sprintf(
		"jev: skill '%s' rule '%s': good example marked good_fails was supposed to fail "+
			"the dry run but exited 0",
		skill, ruleID)
}
