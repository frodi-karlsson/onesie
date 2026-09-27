package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./=:,@%+-]+$`)

func atMostOneQuestion(cmd *cobra.Command, args []string) error {
	err := cobra.MaximumNArgs(1)(cmd, args)
	if err == nil {
		return nil
	}

	dash := cmd.ArgsLenAtDash()
	if dash < 0 || len(args)-dash < 2 || !anyFlagLike(args[dash+1:]) {
		return err
	}

	return fmt.Errorf("%w. Everything after -- is the question, so flags go before --, as in %s %s -- %s",
		err, cmd.CommandPath(), shellJoin(args[dash+1:]), shellQuote(args[dash]))
}

func anyFlagLike(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && arg != "-" {
			return true
		}
	}

	return false
}

func withDashQuestionHint(err error, set *pflag.FlagSet, raw []string, positional []string) error {
	if err == nil || len(positional) > 0 {
		return err
	}

	question := dashQuestion(set, raw)
	if question == "" {
		return err
	}

	return &dashQuestionError{cause: err, question: question}
}

func dashQuestion(set *pflag.FlagSet, raw []string) string {
	for i, arg := range raw {
		if arg == "--" {
			return ""
		}

		if !looksLikeDashQuestion(arg) {
			continue
		}

		if i > 0 && takesNextAsValue(set, raw[i-1]) {
			continue
		}

		return arg
	}

	return ""
}

func looksLikeDashQuestion(arg string) bool {
	return strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsAny(arg, " \t\n")
}

func takesNextAsValue(set *pflag.FlagSet, arg string) bool {
	if strings.HasPrefix(arg, "--") {
		if strings.Contains(arg, "=") {
			return false
		}

		flag := set.Lookup(strings.TrimPrefix(arg, "--"))

		return flag != nil && flag.NoOptDefVal == ""
	}

	if !strings.HasPrefix(arg, "-") || looksLikeDashQuestion(arg) {
		return false
	}

	shorthands := strings.TrimPrefix(arg, "-")
	for i := range len(shorthands) {
		flag := set.ShorthandLookup(shorthands[i : i+1])
		if flag == nil {
			return false
		}

		if flag.NoOptDefVal == "" {
			return i == len(shorthands)-1
		}
	}

	return false
}

type dashQuestionError struct {
	cause    error
	question string
}

func (e *dashQuestionError) Error() string {
	return fmt.Sprintf("%s. %s was read as flags. A question that starts with - needs -- before it, "+
		"with the flags placed first, as in onesie -- %s",
		strings.TrimSuffix(e.cause.Error(), "."), shellQuote(e.question), shellQuote(e.question))
}

func (e *dashQuestionError) Unwrap() error {
	return e.cause
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}

	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if shellSafe.MatchString(arg) {
		return arg
	}

	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
