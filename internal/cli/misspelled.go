package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/spelling"
)

var bareWord = regexp.MustCompile(`^[A-Za-z]+$`)

func refuseMisspelledSubcommand(cmd *cobra.Command, args []string, events []argv.Event) error {
	if len(args) != 1 || cmd.ArgsLenAtDash() == 0 || asked(events) || !bareWord.MatchString(args[0]) {
		return nil
	}

	near := nearSubcommands(cmd, args[0])
	if len(near) == 0 {
		return nil
	}

	return fmt.Errorf("%s is not a subcommand. The closest is %s. "+
		"To ask it as a question, put it after --, as in onesie -- %s",
		args[0], strings.Join(near, " or "), args[0])
}

func nearSubcommands(cmd *cobra.Command, word string) []string {
	word = strings.ToLower(word)

	var near []string

	for _, sub := range cmd.Commands() {
		if sub.Hidden {
			continue
		}

		for _, name := range append([]string{sub.Name()}, sub.Aliases...) {
			name = strings.ToLower(name)
			if nearMiss(word, name) {
				near = append(near, sub.Name())

				break
			}
		}
	}

	return near
}

func nearMiss(word, name string) bool {
	distance := spelling.Distance(word, name)

	// Real words crowd around a short name, such as heap and hell around help, and around version,
	// such as person and vision two edits away. So the allowance grows with the name.
	switch {
	case distance == 0:
		return true
	case len(name) <= 5:
		return distance == 1 && len(word) == len(name) && swapsTwoLetters(word, name)
	case len(name) <= 7:
		return distance <= 1
	default:
		return distance <= 2
	}
}

func swapsTwoLetters(word, name string) bool {
	for i := 0; i+1 < len(name); i++ {
		if word[i] != name[i] {
			return word[i] == name[i+1] && word[i+1] == name[i] && word[i+2:] == name[i+2:]
		}
	}

	return false
}
