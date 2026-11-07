package cli

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func writeHelp(w io.Writer, cmd *cobra.Command, command settingrow.Command, topic string) error {
	flags, err := helpFlags(cmd, command, settingrow.Table())
	if err != nil {
		return err
	}
	topics := helpTopics(command)
	examples := helpExamples(command)
	if err := validateHelpExamples(examples, topics); err != nil {
		return err
	}
	if topic != "" && topic != "all" && !slices.ContainsFunc(topics, func(t helpTopic) bool { return t.name == topic }) {
		return fmt.Errorf("unknown help topic %s", topic)
	}
	var b bytes.Buffer
	if topic == "" || topic == "all" {
		intro := "Ask questions about text and use the answers in scripts."
		if command == settingrow.CommandCalibrate {
			intro = "Compare answers with labelled examples. Calibrate never chooses a threshold."
		}
		writeHelpText(&b, intro, "", "")
		fmt.Fprintf(&b, "\nUsage:\n  %s [question] [flags]\n", cmd.CommandPath())
		if cmd.HasAvailableSubCommands() {
			fmt.Fprintf(&b, "  %s [command]\n", cmd.CommandPath())
		}
	}
	if topic == "" {
		b.WriteString("\nExamples:\n")
		if command == settingrow.CommandCalibrate {
			b.WriteString("  A labelled record in labelled.jsonl:\n    {\"id\":\"a\",\"text\":\"The server is down\",\"urgent\":true}\n\n")
			writeHelpText(&b, "The first command creates the answer file used by the second. The requirement is illustrative.", "  ", "  ")
			b.WriteString("\n")
		}
		writeHelpExamples(&b, examples, "overview")
		overviewTopics := append([]helpTopic{}, topics...)
		if command == settingrow.CommandRoot {
			overviewTopics[2], overviewTopics[3] = overviewTopics[3], overviewTopics[2]
		}
		for _, section := range overviewTopics {
			selected := selectHelpFlags(flags, section.name, true)
			if len(selected) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n%s:\n", section.title)
			for _, flag := range selected {
				writeHelpFlag(&b, flag)
			}
		}
		writeHelpCommands(&b, cmd)
		writeHelpNavigation(&b, cmd, topics)
		if slices.ContainsFunc(examples, func(e helpExample) bool { return slices.Contains(e.places, "footer") }) {
			b.WriteString("\nConnection setup:\n")
			writeHelpExamples(&b, examples, "footer")
			b.WriteString("  See onesie help profile create and onesie help connections.\n")
		}
	} else {
		if topic == "all" {
			writeHelpCommands(&b, cmd)
		}
		for _, section := range topics {
			if topic != "all" && topic != section.name {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "%s\n\nFlags:\n", section.title)
			for _, flag := range selectHelpFlags(flags, section.name, false) {
				writeHelpFlag(&b, flag)
			}
			b.WriteString("\nHow they work together:\n")
			writeHelpText(&b, section.body, "  ", "  ")
			writeHelpExamples(&b, examples, section.name)
		}
		if topic == "all" {
			b.WriteString("\nExit codes:\n")
			codes := rootExitCodes
			if command == settingrow.CommandCalibrate {
				codes = calibrateExitCodes
			}
			for _, line := range codes {
				writeHelpText(&b, line.meaning, fmt.Sprintf("  %-5d", line.code), "       ")
			}
		} else {
			writeHelpNavigation(&b, cmd, topics)
		}
	}
	_, err = w.Write(b.Bytes())
	return err
}

func helpFlags(cmd *cobra.Command, command settingrow.Command, rows []settingrow.Row) ([]helpFlag, error) {
	var flags []helpFlag
	seen := map[*pflag.Flag]bool{}
	local, inherited := cmd.LocalFlags(), cmd.InheritedFlags()
	for _, row := range rows {
		bound, available := row.FlagOn(command)
		var flag *pflag.Flag
		if available {
			flag = local.Lookup(bound.Long)
		} else {
			bound, available = row.FlagOn(settingrow.CommandRoot)
			if available && bound.Persistent {
				flag = inherited.Lookup(bound.Long)
			}
		}
		if flag == nil || flag.Hidden || seen[flag] {
			continue
		}
		if !slices.ContainsFunc(helpTopics(command), func(topic helpTopic) bool { return topic.name == bound.HelpTopic }) {
			return nil, fmt.Errorf("flag --%s has no help topic", flag.Name)
		}
		seen[flag] = true
		flags = append(flags, helpFlag{flag: flag, bound: bound, commonRank: bound.HelpCommon[command]})
	}
	if flag := local.Lookup("help"); flag != nil && !flag.Hidden {
		flags = append(flags, helpFlag{flag: flag, bound: settingrow.BoundFlag{HelpTopic: "output", Kind: settingrow.Bool()}})
		seen[flag] = true
	}
	var missing error
	for _, set := range []*pflag.FlagSet{local, inherited} {
		set.VisitAll(func(flag *pflag.Flag) {
			if !flag.Hidden && !seen[flag] {
				missing = fmt.Errorf("flag --%s has no help metadata", flag.Name)
			}
		})
	}
	return flags, missing
}

type helpFlag struct {
	flag       *pflag.Flag
	bound      settingrow.BoundFlag
	commonRank int
}

func selectHelpFlags(flags []helpFlag, topic string, common bool) []helpFlag {
	var selected []helpFlag
	for _, flag := range flags {
		if flag.bound.HelpTopic == topic && (!common || flag.commonRank > 0) {
			selected = append(selected, flag)
		}
	}
	slices.SortStableFunc(selected, func(a, b helpFlag) int {
		if a.commonRank > 0 && b.commonRank > 0 {
			return a.commonRank - b.commonRank
		}
		if a.commonRank > 0 {
			return -1
		}
		if b.commonRank > 0 {
			return 1
		}
		return 0
	})
	return selected
}

func writeHelpFlag(w *bytes.Buffer, flag helpFlag) {
	signature := "    "
	if flag.flag.Shorthand != "" && flag.flag.ShorthandDeprecated == "" {
		signature = "-" + flag.flag.Shorthand + ", "
	}
	signature += "--" + flag.flag.Name
	value := flag.bound.HelpValue
	if value == "" && flag.bound.Kind != nil {
		value = strings.ToUpper(flag.bound.Kind.ValueName())
	}
	if value != "" {
		signature += " " + value
	}
	description := flag.flag.Usage
	def := flag.flag.DefValue
	if def != "" && def != "false" && def != "[]" && def != argv.Bare {
		description += " Default: " + def + "."
	}
	prefix := "  " + signature
	if len(prefix) >= 30 {
		fmt.Fprintln(w, prefix)
		writeHelpText(w, description, strings.Repeat(" ", 32), strings.Repeat(" ", 32))
		return
	}
	writeHelpText(w, description, fmt.Sprintf("%-32s", prefix), strings.Repeat(" ", 32))
}

func writeHelpText(w *bytes.Buffer, text, first, next string) {
	for index, paragraph := range strings.Split(text, "\n\n") {
		if index > 0 {
			w.WriteString("\n")
		}
		line := first
		used := false
		for _, word := range strings.Fields(paragraph) {
			space := ""
			if used {
				space = " "
			}
			if used && len(line)+len(space)+len(word) > 88 {
				fmt.Fprintln(w, line)
				line = next
				space = ""
			}
			line += space + word
			used = true
		}
		fmt.Fprintln(w, line)
	}
}

func writeHelpCommands(w *bytes.Buffer, cmd *cobra.Command) {
	if !cmd.HasAvailableSubCommands() {
		return
	}
	w.WriteString("\nAvailable Commands:\n")
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() || sub.Name() == "help" {
			writeHelpText(w, sub.Short, fmt.Sprintf("  %-12s", sub.Name()), strings.Repeat(" ", 14))
		}
	}
}

func writeHelpNavigation(w *bytes.Buffer, cmd *cobra.Command, topics []helpTopic) {
	w.WriteString("\nMore help:\n")
	path := strings.TrimPrefix(cmd.CommandPath(), "onesie")
	for _, topic := range topics {
		fmt.Fprintf(w, "  onesie help%s %s\n", path, topic.name)
	}
	fmt.Fprintf(w, "  onesie help%s all\n", path)
}

func writeHelpExamples(w *bytes.Buffer, examples []helpExample, place string) {
	for _, example := range examples {
		if !slices.Contains(example.places, place) {
			continue
		}
		fmt.Fprintf(w, "\n  %s:\n    %s\n", example.title, helpExampleCommand(example))
	}
}

func helpExampleCommand(example helpExample) string {
	words := []string{"onesie"}
	for _, arg := range example.args {
		words = append(words, helpShellQuote(arg))
	}
	command := strings.Join(words, " ")
	if example.stdin != "" {
		command = "echo " + helpShellQuote(example.stdin) + " | " + command
	}
	if example.inputFile != "" {
		command += " < " + helpShellQuote(example.inputFile)
	}
	return command
}

func helpShellQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n'\"$`\\;|&<>()*?[]{}!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validateHelpExamples(examples []helpExample, topics []helpTopic) error {
	ids := map[string]bool{}
	for _, example := range examples {
		if example.id == "" || ids[example.id] || example.stdin != "" && example.inputFile != "" {
			return fmt.Errorf("invalid help example %s", example.id)
		}
		ids[example.id] = true
		if len(example.places) == 0 {
			return fmt.Errorf("help example %s has no destination", example.id)
		}
		for _, place := range example.places {
			if place != "overview" && place != "footer" && !slices.ContainsFunc(topics, func(t helpTopic) bool { return t.name == place }) {
				return fmt.Errorf("invalid help example destination %s", place)
			}
		}
	}
	return nil
}
