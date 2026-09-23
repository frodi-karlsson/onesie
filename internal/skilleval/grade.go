// Package skilleval dry runs every jev command an agent wrote during a claude plugin eval run,
// per spec section 6.2. The eval has no grader that can run code, so this reads the traces the
// eval kept and reports per case and per arm how many commands jev itself accepts.
package skilleval

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/frodi-karlsson/jev-cli/internal/skillcheck"
)

// Never passed to a dry run. Each one is a real subcommand rather than a question, and a dry run
// of a subcommand proves nothing while running it could change the user's credentials.
var subcommands = []string{"auth", "completion", "help", "version"}

// NewGrader returns a Grader that dry runs every command through runner and reads result and
// trace files from disk.
func NewGrader(runner DryRunner) *Grader {
	return &Grader{runner: runner, readFile: os.ReadFile}
}

// DryRunner dry runs one command line without a shell.
type DryRunner interface {
	DryRun(ctx context.Context, command string) (skillcheck.DryRunResult, error)
}

// Grader dry runs the jev commands found in the traces an eval result points at.
type Grader struct {
	runner   DryRunner
	readFile func(name string) ([]byte, error)
}

// Grade reads the aggregate-result.json at resultPath and dry runs every jev command in every
// run's trace, arm by arm. A run whose trace is gone is counted, not failed, since the eval only
// keeps traces under --keep-temp.
func (g *Grader) Grade(ctx context.Context, resultPath string) (Report, error) {
	data, err := g.readFile(resultPath)
	if err != nil {
		return Report{}, fmt.Errorf("jev: reading the eval result: %w", err)
	}

	var result aggregateResult
	if err := json.Unmarshal(data, &result); err != nil {
		return Report{}, fmt.Errorf("jev: parsing %s: %w", resultPath, err)
	}

	var report Report

	for _, c := range result.Cases {
		caseReport := CaseReport{Name: c.Name}

		for _, arm := range armOrder(c.Arms) {
			armReport, err := g.gradeArm(ctx, arm, c.Arms[arm])
			if err != nil {
				return Report{}, fmt.Errorf("jev: case '%s': %w", c.Name, err)
			}

			caseReport.Arms = append(caseReport.Arms, armReport)
		}

		report.Cases = append(report.Cases, caseReport)
	}

	return report, nil
}

// Report is one Grade across every case in an eval result.
type Report struct {
	Cases []CaseReport
}

// CaseReport is one case's tally, one entry per arm, the plugin arm first.
type CaseReport struct {
	Name string
	Arms []ArmReport
}

// ArmReport tallies every run of one case in one arm.
type ArmReport struct {
	Arm           string
	Runs          int
	MissingTraces int
	Commands      int
	Clean         int
	Failed        []Finding
	Skipped       []Finding
}

// Finding is one command that failed its dry run or could not be run, and why.
type Finding struct {
	Run     int
	Command string
	Detail  string
}

type aggregateResult struct {
	Cases []struct {
		Name string                `json:"name"`
		Arms map[string][]runEntry `json:"arms"`
	} `json:"cases"`
}

type runEntry struct {
	TracePath string `json:"tracePath"`
}

func armOrder(arms map[string][]runEntry) []string {
	names := make([]string, 0, len(arms))
	for name := range arms {
		names = append(names, name)
	}

	slices.SortFunc(names, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == "with":
			return -1
		case b == "with":
			return 1
		default:
			return cmp.Compare(a, b)
		}
	})

	return names
}

func (g *Grader) gradeArm(ctx context.Context, arm string, runs []runEntry) (ArmReport, error) {
	report := ArmReport{Arm: arm, Runs: len(runs)}

	for i, run := range runs {
		trace, err := g.readFile(run.TracePath)
		if run.TracePath == "" || err != nil {
			report.MissingTraces++

			continue
		}

		scripts, err := traceScripts(trace)
		if err != nil {
			return ArmReport{}, fmt.Errorf("%s run %d: %w", arm, i+1, err)
		}

		for _, script := range scripts {
			for _, command := range jevCommands(script) {
				if err := g.dryRun(ctx, i+1, command, &report); err != nil {
					return ArmReport{}, err
				}
			}
		}
	}

	return report, nil
}

func (g *Grader) dryRun(ctx context.Context, run int, command string, report *ArmReport) error {
	report.Commands++

	if sub := subcommand(command); sub != "" {
		report.Skipped = append(report.Skipped, Finding{
			Run: run, Command: command, Detail: "runs the " + sub + " subcommand",
		})

		return nil
	}

	result, err := g.runner.DryRun(ctx, command)
	if err != nil {
		return fmt.Errorf("dry running %s: %w", command, err)
	}

	switch {
	case result.Skipped != "":
		report.Skipped = append(report.Skipped, Finding{Run: run, Command: command, Detail: result.Skipped})
	case result.ExitCode != 0:
		report.Failed = append(report.Failed, Finding{
			Run: run, Command: command, Detail: fmt.Sprintf("exit %d: %s", result.ExitCode, result.Stderr),
		})
	default:
		report.Clean++
	}

	return nil
}

func subcommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return ""
	}

	if name := strings.Trim(fields[1], `'"`); slices.Contains(subcommands, name) {
		return name
	}

	return ""
}
