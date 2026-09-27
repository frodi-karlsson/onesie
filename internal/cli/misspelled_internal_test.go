package cli

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

func TestNearSubcommands(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "onesie"}
	root.AddCommand(
		&cobra.Command{Use: "questions", Aliases: []string{"qs"}},
		&cobra.Command{Use: "cache"},
		&cobra.Command{Use: "calibrate"},
		&cobra.Command{Use: "version"},
		&cobra.Command{Use: "help"},
		&cobra.Command{Use: "auth"},
		&cobra.Command{Use: "completion"},
		&cobra.Command{Use: "secret", Hidden: true},
	)

	// Cobra sorts the subcommands on the first call, which the parallel cases would race on.
	root.Commands()

	tests := []struct {
		name string
		word string
		want []string
	}{
		{name: "should name the subcommand an alias belongs to", word: "qs", want: []string{"questions"}},
		{name: "should match an alias in any case", word: "QS", want: []string{"questions"}},
		{name: "should count a swap of neighbouring letters as one edit", word: "questoins", want: []string{"questions"}},
		{name: "should allow two edits from a long name", word: "calbirat", want: []string{"calibrate"}},
		{name: "should refuse a long name one letter short", word: "calibrat", want: []string{"calibrate"}},
		{name: "should refuse a transposition in completion", word: "completoin", want: []string{"completion"}},
		// Split, since misspell flags the whole word as a typo of version.
		{name: "should refuse a transposition in version", word: "ver" + "ison", want: []string{"version"}},
		{name: "should refuse a transposition in a short name", word: "hlep", want: []string{"help"}},
		{name: "should refuse a transposition in cache", word: "cahce", want: []string{"cache"}},
		{name: "should refuse a transposition in auth", word: "atuh", want: []string{"auth"}},
		{name: "should pass a word two edits from version", word: "person", want: nil},
		{name: "should pass session", word: "session", want: nil},
		{name: "should pass vision", word: "vision", want: nil},
		{name: "should pass a word one substitution from a short name", word: "hell", want: nil},
		{name: "should pass heap", word: "heap", want: nil},
		{name: "should pass auto", word: "auto", want: nil},
		{name: "should pass ache", word: "ache", want: nil},
		{name: "should pass a short name with a letter added", word: "cached", want: nil},
		{name: "should pass cash", word: "cash", want: nil},
		{name: "should skip a hidden subcommand", word: "secrets", want: nil},
		{name: "should match nothing for a real question", word: "urgent", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := nearSubcommands(root, tc.word); !slices.Equal(got, tc.want) {
				t.Errorf("nearSubcommands(%q) = %v, want %v", tc.word, got, tc.want)
			}
		})
	}
}
