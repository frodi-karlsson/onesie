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
		&cobra.Command{Use: "secret", Hidden: true},
	)

	tests := []struct {
		name string
		word string
		want []string
	}{
		{name: "should name the subcommand an alias belongs to", word: "qs", want: []string{"questions"}},
		{name: "should match an alias in any case", word: "QS", want: []string{"questions"}},
		{name: "should count a swap of neighbouring letters as one edit", word: "questoins", want: []string{"questions"}},
		{name: "should allow two edits from a long name", word: "calbirat", want: []string{"calibrate"}},
		{name: "should allow one edit from a short name", word: "cached", want: []string{"cache"}},
		{name: "should allow only one edit from a short name", word: "cash", want: nil},
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
