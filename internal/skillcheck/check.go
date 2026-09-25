// Package skillcheck dry runs every rule's bad and good example in a skill. The good_fails,
// bad_passes and unverifiable markers invert or skip an example.
package skillcheck

import (
	"context"
	"fmt"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/skillgen"
)

// Check runs every rule's bad and good example under root through runner. An example that is not a
// onesie invocation is skipped, and every skip is reported with its reason.
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
					ctx, runner, f.Skill.Name, rule.ID, "bad", rule.Bad, rule.BadPasses,
					rule.BadUnverifiable, &report,
				); err != nil {
					return Report{}, err
				}
			}

			if rule.Good != "" {
				if err := checkRule(
					ctx, runner, f.Skill.Name, rule.ID, "good", rule.Good, !rule.GoodFails,
					rule.GoodUnverifiable, &report,
				); err != nil {
					return Report{}, err
				}
			}
		}
	}

	return report, nil
}

func checkRule(
	ctx context.Context, runner *Runner, skill, ruleID, kind, command string, wantPass bool,
	unverifiable string, report *Report,
) error {
	if unverifiable != "" {
		report.Skipped = append(report.Skipped, Skip{
			Skill: skill, RuleID: ruleID, Kind: kind, Command: command, Reason: unverifiable,
		})

		return nil
	}

	result, err := runner.DryRun(ctx, command)
	if err != nil {
		return fmt.Errorf("onesie: skill '%s' rule '%s' %s example: %w", skill, ruleID, kind, err)
	}

	if result.Skipped != "" {
		report.Skipped = append(report.Skipped, Skip{
			Skill: skill, RuleID: ruleID, Kind: kind, Command: command, Reason: result.Skipped,
		})

		return nil
	}

	report.Checked++

	if passed := result.ExitCode == 0; passed != wantPass {
		report.Failures = append(report.Failures,
			outcomeMessage(skill, ruleID, kind, wantPass, result.ExitCode, result.Args, result.Stderr))
	}

	return nil
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

	message := fmt.Sprintf("onesie: skill '%s' rule '%s': %s\n  ran: onesie %s",
		skill, ruleID, clause, strings.Join(args, " "))

	if stderr = strings.TrimSpace(stderr); stderr != "" {
		message += "\n  " + stderr
	}

	return message
}
