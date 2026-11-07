package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func installHelp(root, calibrate *cobra.Command) {
	helpShowsSwitches(root)
	legacy := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		command, custom := helpCommand(cmd)
		if !custom {
			legacy(cmd, args)
			return
		}
		//nolint:errcheck // Cobra help callbacks cannot return writer failures, as with the default handler.
		_ = writeHelp(cmd.OutOrStdout(), cmd, command, "")
	})
	root.InitDefaultHelpCmd()
	for _, cmd := range []*cobra.Command{root, calibrate} {
		command, _ := helpCommand(cmd)
		for _, topic := range append(helpTopics(command), helpTopic{name: "all"}) {
			for _, sub := range cmd.Commands() {
				if sub.Name() == topic.name || slices.Contains(sub.Aliases, topic.name) {
					panic("help topic collides with command " + topic.name)
				}
			}
		}
	}
	for _, sub := range root.Commands() {
		if sub.Name() != "help" {
			continue
		}
		sub.Run = nil
		sub.RunE = func(help *cobra.Command, args []string) error {
			cmd, topic, err := resolveHelp(root, args)
			if err != nil {
				return err
			}
			if cmd.Context() == nil {
				cmd.SetContext(help.Context())
			}
			cmd.InitDefaultHelpFlag()
			cmd.InitDefaultVersionFlag()
			if command, custom := helpCommand(cmd); custom {
				return writeHelp(cmd.OutOrStdout(), cmd, command, topic)
			}
			return cmd.Help()
		}
		sub.ValidArgsFunction = completeHelp
	}
}

func resolveHelp(root *cobra.Command, args []string) (*cobra.Command, string, error) {
	cmd, rest, err := root.Find(args)
	if err == nil && len(rest) == 0 {
		return cmd, "", nil
	}
	if cmd == nil {
		cmd = root
	}
	command, custom := helpCommand(cmd)
	if err == nil && custom && len(rest) == 1 && (rest[0] == "all" || slices.ContainsFunc(helpTopics(command), func(topic helpTopic) bool { return topic.name == rest[0] })) {
		return cmd, rest[0], nil
	}
	hint := "onesie help"
	if cmd.Name() == "calibrate" {
		hint += " calibrate"
	}
	return nil, "", fmt.Errorf("unknown help topic '%s'. Run %s to see the topics and subcommands", strings.Join(args, " "), hint)
}

func completeHelp(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	root := cmd.Root()
	target, rest, err := root.Find(args)
	if err != nil || len(rest) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var suggestions []string
	for _, sub := range target.Commands() {
		if !sub.IsAvailableCommand() && sub.Name() != "help" {
			continue
		}
		if strings.HasPrefix(sub.Name(), prefix) {
			suggestions = append(suggestions, sub.Name())
		}
		for _, alias := range sub.Aliases {
			if strings.HasPrefix(alias, prefix) {
				suggestions = append(suggestions, alias)
			}
		}
	}
	if command, custom := helpCommand(target); custom {
		for _, topic := range append(helpTopics(command), helpTopic{name: "all"}) {
			if strings.HasPrefix(topic.name, prefix) {
				suggestions = append(suggestions, topic.name)
			}
		}
	}
	return suggestions, cobra.ShellCompDirectiveNoFileComp
}

func helpCommand(cmd *cobra.Command) (settingrow.Command, bool) {
	if cmd.Parent() == nil {
		return settingrow.CommandRoot, true
	}
	if cmd.Name() == "calibrate" && cmd.Parent().Parent() == nil {
		return settingrow.CommandCalibrate, true
	}
	return 0, false
}
