package cli

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

const annotationHelpValue = "onesie-help-value"

func writeHelp(w io.Writer, cmd *cobra.Command, command settingrow.Command, topic string) error {
	flags, err := helpFlags(cmd, command, settingrow.Table())
	if err != nil {
		return err
	}
	topics := helpTopics(command)
	examples := helpExamples(command)
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
		b.WriteString("\n")
		writeHelpUsage(&b, cmd, cmd.CommandPath()+" [question] [flags]")
	}
	if topic == "" {
		writeHelpOverview(&b, cmd, command, flags, topics, examples)
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
			if section.codes != nil {
				b.WriteString(exitCodesSection(section.codes) + "\n")
				continue
			}
			fmt.Fprintf(&b, "%s\n", section.title)
			writeHelpSection(&b, "Flags", selectHelpFlags(flags, section.name, false))
			b.WriteString("\nHow they work together:\n")
			writeHelpText(&b, section.body, "  ", "  ")
			writeHelpExamples(&b, examples, section.name)
		}
		if topic != "all" {
			writeHelpNavigation(&b, cmd, topics)
		}
	}
	_, err = w.Write(b.Bytes())
	return err
}

func writeHelpOverview(b *bytes.Buffer, cmd *cobra.Command, command settingrow.Command, flags []helpFlag, topics []helpTopic, examples []helpExample) {
	b.WriteString("\nExamples:\n")
	if command == settingrow.CommandCalibrate {
		b.WriteString("  A labelled record in labelled.jsonl:\n    " + helpLabelledRecord + "\n\n")
		writeHelpText(b, "The first command creates the answer file used by the second. The requirement is illustrative.", "  ", "  ")
	}
	writeHelpExamples(b, examples, "overview")
	for _, section := range overviewOrder(topics) {
		writeHelpSection(b, section.title, selectHelpFlags(flags, section.name, true))
	}
	writeHelpCommands(b, cmd)
	writeHelpNavigation(b, cmd, topics)
	if slices.ContainsFunc(examples, func(e helpExample) bool { return slices.Contains(e.places, "footer") }) {
		b.WriteString("\nConnection setup:\n")
		writeHelpExamples(b, examples, "footer")
		b.WriteString("  See onesie help profile create and onesie help connections.\n")
	}
}

func overviewOrder(topics []helpTopic) []helpTopic {
	shown := slices.DeleteFunc(slices.Clone(topics), func(topic helpTopic) bool { return topic.overview == 0 })
	slices.SortStableFunc(shown, func(a, b helpTopic) int { return a.overview - b.overview })
	return shown
}

func helpFlags(cmd *cobra.Command, command settingrow.Command, rows []settingrow.Row) ([]helpFlag, error) {
	var flags []helpFlag
	seen := map[*pflag.Flag]bool{}
	local, inherited := cmd.LocalFlags(), cmd.InheritedFlags()
	topics := helpTopics(command)
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
		if !slices.ContainsFunc(topics, func(topic helpTopic) bool { return topic.name == bound.HelpTopic }) {
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

func writeSubcommandHelp(w io.Writer, cmd *cobra.Command) error {
	local, inherited, err := commandHelpFlags(cmd)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	if text := cmp.Or(cmd.Long, cmd.Short); text != "" {
		b.WriteString(strings.TrimRightFunc(text, unicode.IsSpace) + "\n\n")
	}
	useLine := ""
	if cmd.Runnable() {
		useLine = cmd.UseLine()
	}
	writeHelpUsage(&b, cmd, useLine)
	if cmd.HasExample() {
		fmt.Fprintf(&b, "\nExamples:\n%s\n", cmd.Example)
	}
	writeHelpCommands(&b, cmd)
	writeHelpSection(&b, "Flags", local)
	writeHelpSection(&b, "Global Flags", inherited)
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintf(&b, "\nUse \"%s [command] --help\" for more information about a command.\n", cmd.CommandPath())
	}
	_, err = w.Write(b.Bytes())
	return err
}

func commandHelpFlags(cmd *cobra.Command) (local, inherited []helpFlag, missing error) {
	describe := func(flag *pflag.Flag, owner *cobra.Command, persistent bool) helpFlag {
		described, found := describedFlag(flag, rowCommand(owner), persistent)
		if !found && missing == nil {
			missing = fmt.Errorf("flag --%s has no help metadata", flag.Name)
		}
		return described
	}
	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Hidden {
			local = append(local, describe(flag, cmd, false))
		}
	})
	cmd.InheritedFlags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Hidden {
			inherited = append(inherited, describe(flag, flagOwner(cmd, flag), true))
		}
	})
	return local, inherited, missing
}

func describedFlag(flag *pflag.Flag, command settingrow.Command, persistent bool) (helpFlag, bool) {
	for _, row := range settingrow.Table() {
		if bound, available := row.FlagOn(command); available && bound.Long == flag.Name && (!persistent || bound.Persistent) {
			return helpFlag{flag: flag, bound: bound}, true
		}
	}
	if value, named := flag.Annotations[annotationHelpValue]; named && len(value) == 1 {
		return helpFlag{flag: flag, bound: settingrow.BoundFlag{HelpValue: value[0]}}, true
	}
	if switchFlag, ok := flag.Value.(interface{ IsBoolFlag() bool }); ok && switchFlag.IsBoolFlag() {
		return helpFlag{flag: flag, bound: settingrow.BoundFlag{Kind: settingrow.Bool()}}, true
	}
	return helpFlag{}, false
}

func rowCommand(cmd *cobra.Command) settingrow.Command {
	path := strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
	for _, row := range settingrow.Table() {
		for _, command := range row.Flag.Commands {
			if command.String() == path {
				return command
			}
		}
	}
	return 0
}

func flagOwner(cmd *cobra.Command, flag *pflag.Flag) *cobra.Command {
	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		if parent.PersistentFlags().Lookup(flag.Name) == flag {
			return parent
		}
	}
	return cmd
}

func writeHelpUsage(w *bytes.Buffer, cmd *cobra.Command, useLine string) {
	w.WriteString("Usage:\n")
	if useLine != "" {
		fmt.Fprintf(w, "  %s\n", useLine)
	}
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintf(w, "  %s [command]\n", cmd.CommandPath())
	}
}

func writeHelpSection(w *bytes.Buffer, title string, flags []helpFlag) {
	if len(flags) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, flag := range flags {
		writeHelpFlag(w, flag)
	}
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
	description := flagUsage(flag.flag)
	def := flag.flag.DefValue
	if def != "" && def != "false" && def != "[]" && def != argv.Bare {
		if !strings.HasSuffix(description, ".") && !strings.HasSuffix(description, "!") && !strings.HasSuffix(description, ":") {
			description += "."
		}
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

func flagUsage(flag *pflag.Flag) string {
	if flag.Name == "help" {
		return "Show this help."
	}
	return flag.Usage
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

func writeHelpText(w *bytes.Buffer, text, first, next string) {
	for index, paragraph := range strings.Split(text, "\n\n") {
		if index > 0 {
			w.WriteString("\n")
		}
		if strings.HasPrefix(paragraph, "    ") {
			fmt.Fprintln(w, paragraph)
			continue
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
	safe := value != ""
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r) {
			continue
		}
		safe = false
		break
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func borrowHelpValue(set *pflag.FlagSet, name string, from settingrow.Name) {
	row, found := settingrow.Lookup(from)
	if !found || row.HelpValue == "" {
		panic("help value: no settings row " + string(from) + " with a help value")
	}
	flag := set.Lookup(name)
	if flag == nil {
		panic("help value: no flag --" + name)
	}
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[annotationHelpValue] = []string{row.HelpValue}
}

type helpFlag struct {
	flag       *pflag.Flag
	bound      settingrow.BoundFlag
	commonRank int
}
