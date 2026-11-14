package cli

import (
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/qfile"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

const noFiles = cobra.ShellCompDirectiveNoFileComp

func addFlagCompletions(root, calibrate *cobra.Command, settings rootSettings) error {
	files := func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return savedNames(settings), cobra.ShellCompDirectiveDefault
	}

	shared := map[string]cobra.CompletionFunc{
		"provider": fixedCompletion(providerNames()),
		"profile": func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return profileNamesIn(settings), noFiles
		},
	}

	for _, name := range slices.Sorted(maps.Keys(shared)) {
		if err := root.RegisterFlagCompletionFunc(name, shared[name]); err != nil {
			return err
		}
	}

	// profile create and profile update take a separate --provider, the provider the profile stores.
	for _, path := range [][]string{{"profile", "create"}, {"profile", "update"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			return err
		}

		if err := cmd.RegisterFlagCompletionFunc(flagProvider, shared[flagProvider]); err != nil {
			return err
		}
	}

	for command, cmd := range map[settingrow.Command]*cobra.Command{settingrow.CommandRoot: root, settingrow.CommandCalibrate: calibrate} {
		if err := addLocalCompletions(cmd, command, files); err != nil {
			return err
		}
	}

	return nil
}

func addLocalCompletions(cmd *cobra.Command, command settingrow.Command, files cobra.CompletionFunc) error {
	completions := map[string]cobra.CompletionFunc{"file": files}

	for _, row := range settingrow.Table() {
		bound, onCommand := row.FlagOn(command)
		if choices := settingrow.ChoicesOf(bound.Kind); onCommand && len(choices) > 0 {
			completions[bound.Long] = fixedCompletion(choices)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(completions)) {
		if err := cmd.RegisterFlagCompletionFunc(name, completions[name]); err != nil {
			return err
		}
	}

	return nil
}

func completeProfileName(settings rootSettings) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFiles
		}

		return profileNamesIn(settings), noFiles
	}
}

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
