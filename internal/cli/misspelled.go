package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
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

	return fmt.Errorf("onesie: %s is not a subcommand. The closest is %s. "+
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
	distance := editDistance(word, name)

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

func editDistance(a, b string) int {
	rows := make([][]int, len(a)+1)
	for i := range rows {
		rows[i] = make([]int, len(b)+1)
		rows[i][0] = i
	}

	for j := range rows[0] {
		rows[0][j] = j
	}

	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			rows[i][j] = min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)

			// A swap of two neighbouring letters counts as one edit, so questoins is one from questions.
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				rows[i][j] = min(rows[i][j], rows[i-2][j-2]+1)
			}
		}
	}

	return rows[len(a)][len(b)]
}
