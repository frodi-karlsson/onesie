package cli

import (
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/qfile"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func addFlagCompletions(root, calibrate *cobra.Command, settings rootSettings) error {
	files := func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return savedNames(settings), cobra.ShellCompDirectiveDefault
	}

	completions := map[string]cobra.CompletionFunc{
		"provider": fixedCompletion(providerNames()),
		"profile": func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return profileNamesIn(settings), noFiles
		},
		"file": files,
	}

	for _, row := range settingrow.Table() {
		bound, onRoot := row.FlagOn(settingrow.CommandRoot)
		if choices := settingrow.ChoicesOf(row.Kind); onRoot && len(choices) > 0 {
			completions[bound.Long] = fixedCompletion(choices)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(completions)) {
		if err := root.RegisterFlagCompletionFunc(name, completions[name]); err != nil {
			return err
		}
	}

	return calibrate.RegisterFlagCompletionFunc("file", files)
}

const noFiles = cobra.ShellCompDirectiveNoFileComp

func fixedCompletion(values []string) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, noFiles
	}
}

func providerNames() []string {
	names := make([]string, 0, len(onesie.Providers()))
	for _, provider := range onesie.Providers() {
		names = append(names, provider.Name())
	}

	return names
}

func profileNamesIn(settings rootSettings) []string {
	// Read without opening, since opening converts an old credentials.json and completion must not write.
	if settings.profilesPath == nil {
		return nil
	}

	path, err := settings.profilesPath()
	if err != nil {
		return nil
	}

	profiles, found, err := settings.credStore.LoadProfiles(path)
	if err != nil || !found {
		return nil
	}

	return slices.Sorted(maps.Keys(profiles.Profiles))
}

func savedNames(settings rootSettings) []string {
	env, err := findEnv(settings)
	if err != nil {
		return nil
	}

	listed, err := qfile.List(env)
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(listed))
	for _, found := range listed {
		names = append(names, found.Name)
	}

	return names
}
