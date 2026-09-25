// Package skilleval dry runs every onesie command an agent wrote during a claude plugin eval run,
// since the eval has no grader that can run code.
package skilleval

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/cli"
	"github.com/frodi-karlsson/onesie/internal/skillcheck"
)

// Named here rather than looked up, since onesie keeps --api-key only to refuse it and the grader
// has to keep reading its value once the flag is gone.
const removedAPIKey = "api-key"

var (
	subcommands = []string{"auth", "completion", "help", "version"}

	fileFlags = []string{"file", "state-file"}

	apiKeyValue = regexp.MustCompile(`(--api-key(?:=|\s+))('[^']*'|"(?:[^"\\]|\\.)*"|[^\s'"]+)`)
)

// NewGrader returns a Grader that dry runs every command through runner, reads result and trace
// files from disk and parses argv with the flags of the onesie built from this tree.
func NewGrader(runner DryRunner) *Grader {
	return &Grader{
		runner:   runner,
		readFile: os.ReadFile,
		flags:    cli.NewRootCmd(cli.BuildInfo{}).Flags(),
	}
}

// Grader dry runs the onesie commands found in the traces an eval result points at.
type Grader struct {
	runner   DryRunner
	readFile func(name string) ([]byte, error)
	flags    FlagLookup
}

// DryRunner dry runs one command line without a shell.
type DryRunner interface {
	DryRun(ctx context.Context, command string) (skillcheck.DryRunResult, error)
}

// Grade reads the aggregate-result.json at resultPath and dry runs every onesie command in every
// run's trace, arm by arm. A run whose trace is gone is counted, not failed.
func (g *Grader) Grade(ctx context.Context, resultPath string) (Report, error) {
	data, err := g.readFile(resultPath)
	if err != nil {
		return Report{}, fmt.Errorf("onesie: reading the eval result: %w", err)
	}

	var result aggregateResult
	if err := json.Unmarshal(data, &result); err != nil {
		return Report{}, fmt.Errorf("onesie: parsing %s: %w", resultPath, err)
	}

	var report Report

	for _, c := range result.Cases {
		caseReport := CaseReport{Name: c.Name}

		for _, arm := range armOrder(c.Arms) {
			armReport, err := g.gradeArm(ctx, arm, c.Arms[arm])
			if err != nil {
				return Report{}, fmt.Errorf("onesie: case '%s': %w", c.Name, err)
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

type aggregateResult struct {
	Cases []struct {
		Name string                `json:"name"`
		Arms map[string][]runEntry `json:"arms"`
	} `json:"cases"`
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
		if run.TracePath == "" {
			report.MissingTraces++

			continue
		}

		trace, err := g.readFile(run.TracePath)
		if errors.Is(err, fs.ErrNotExist) {
			report.MissingTraces++

			continue
		}

		if err != nil {
			return ArmReport{}, fmt.Errorf("%s run %d: reading the trace: %w", arm, i+1, err)
		}

		scripts, err := traceScripts(trace)
		if err != nil {
			return ArmReport{}, fmt.Errorf("%s run %d: %w", arm, i+1, err)
		}

		for _, script := range scripts {
			for _, command := range onesieCommands(script) {
				if err := g.dryRun(ctx, i+1, command, &report); err != nil {
					return ArmReport{}, err
				}
			}
		}
	}

	return report, nil
}

type runEntry struct {
	TracePath string `json:"tracePath"`
}

func (g *Grader) dryRun(ctx context.Context, run int, command string, report *ArmReport) error {
	report.Commands++

	shown := redactAPIKey(command)
	args := g.parse(command)

	if args.has(removedAPIKey) {
		report.Failed = append(report.Failed, Finding{
			Run: run, Command: shown,
			Detail: "passes --api-key, which onesie removed since argv is visible to other processes",
		})

		return nil
	}

	if reason := args.skipReason(); reason != "" {
		report.Skipped = append(report.Skipped, Finding{Run: run, Command: shown, Detail: reason})

		return nil
	}

	result, err := g.runner.DryRun(ctx, command)
	if err != nil {
		return fmt.Errorf("dry running %s: %w", shown, err)
	}

	switch {
	case result.Skipped != "":
		report.Skipped = append(report.Skipped, Finding{Run: run, Command: shown, Detail: result.Skipped})
	case result.ExitCode != 0:
		report.Failed = append(report.Failed, Finding{
			Run: run, Command: shown, Detail: fmt.Sprintf("exit %d: %s", result.ExitCode, result.Stderr),
		})
	default:
		report.Clean++
	}

	return nil
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

// Finding is one command that failed its dry run or could not be run, and why. Command has any
// --api-key value redacted.
type Finding struct {
	Run     int
	Command string
	Detail  string
}

func redactAPIKey(command string) string {
	return apiKeyValue.ReplaceAllString(command, "${1}REDACTED")
}

func (g *Grader) parse(command string) parsedArgs {
	tokens, reason := skillcheck.Tokenize(command)
	if reason != "" || len(tokens) == 0 {
		return parsedArgs{}
	}

	return parseArgs(tokens[1:], g.flags)
}

func parseArgs(args []string, flags FlagLookup) parsedArgs {
	var parsed parsedArgs

	positionalSeen := false

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			return parsed

		case len(arg) > 2 && strings.HasPrefix(arg, "--"):
			name, _, joined := strings.Cut(arg[2:], "=")
			parsed.flags = append(parsed.flags, name)

			if !joined && (name == removedAPIKey || takesValue(flags.Lookup(name))) {
				i++
			}

		case len(arg) > 1 && arg[0] == '-':
			if shorthandTakesNextArg(arg[1:], flags, &parsed) {
				i++
			}

		case !positionalSeen:
			positionalSeen = true

			if slices.Contains(subcommands, arg) {
				parsed.subcommand = arg
			}
		}
	}

	return parsed
}

func shorthandTakesNextArg(cluster string, flags FlagLookup, parsed *parsedArgs) bool {
	for i := range len(cluster) {
		flag := flags.ShorthandLookup(cluster[i : i+1])
		if flag == nil {
			return false
		}

		parsed.flags = append(parsed.flags, flag.Name)

		if takesValue(flag) {
			return i == len(cluster)-1
		}
	}

	return false
}

// FlagLookup finds a onesie flag by its long name or its one letter shorthand.
type FlagLookup interface {
	Lookup(name string) *pflag.Flag
	ShorthandLookup(name string) *pflag.Flag
}

type parsedArgs struct {
	flags      []string
	subcommand string
}

func takesValue(flag *pflag.Flag) bool {
	return flag != nil && flag.NoOptDefVal == ""
}

func (p parsedArgs) skipReason() string {
	if p.subcommand != "" {
		return "runs the " + p.subcommand + " subcommand"
	}

	for _, name := range fileFlags {
		if p.has(name) {
			return "reads a file through --" + name
		}
	}

	return ""
}

func (p parsedArgs) has(long string) bool {
	return slices.Contains(p.flags, long)
}
