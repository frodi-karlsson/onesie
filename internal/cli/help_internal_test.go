package cli

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/delimiter"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func TestHelpFlags(t *testing.T) {
	for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
		t.Run("should cover visible flags on "+command.String(), func(t *testing.T) {
			cmd := helpTestCommand(t, command)
			flags, err := helpFlags(cmd, command, settingrow.Table())
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			common := make([]helpFlag, 0)
			for _, flag := range flags {
				if got[flag.flag.Name] {
					t.Fatalf("duplicate %s", flag.flag.Name)
				}
				got[flag.flag.Name] = true
				if flag.commonRank > 0 {
					common = append(common, flag)
				}
			}
			cmd.Flags().VisitAll(func(flag *pflag.Flag) {
				if !flag.Hidden && !got[flag.Name] {
					t.Errorf("missing %s", flag.Name)
				}
			})
			slices.SortFunc(common, func(a, b helpFlag) int { return a.commonRank - b.commonRank })
			names := make([]string, len(common))
			for i, f := range common {
				names[i] = f.flag.Name
			}
			want := strings.Fields("ask pick rate desc file state state-file input map output raw merge threshold assert quiet")
			if command == settingrow.CommandCalibrate {
				want = strings.Fields("ask pick rate file input map id label cuts require offline out output resume")
				if !got["provider"] || !got["profile"] || got["assert"] {
					t.Fatalf("availability = %v", got)
				}
				for _, f := range flags {
					if f.flag.Name == "output" && !strings.Contains(f.flag.Usage, "report") {
						t.Fatal("lost output override")
					}
				}
			}
			if !slices.Equal(names, want) {
				t.Fatalf("common = %v, want %v", names, want)
			}
		})
	}
	t.Run("should use requested command ranks on inherited metadata", func(t *testing.T) {
		cmd := helpTestCommand(t, settingrow.CommandCalibrate)
		rows := settingrow.Table()
		for i := range rows {
			if rows[i].Name == "profile" {
				rows[i].HelpCommon = map[settingrow.Command]int{settingrow.CommandCalibrate: 99}
			}
		}
		flags, err := helpFlags(cmd, settingrow.CommandCalibrate, rows)
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range flags {
			if flag.flag.Name == "profile" {
				if flag.commonRank != 99 {
					t.Fatal("lost inherited rank")
				}
				return
			}
		}
		t.Fatal("missing inherited profile")
	})
	t.Run("should respect local shadowing of inherited flags", func(t *testing.T) {
		root := helpTestCommand(t, settingrow.CommandRoot)
		var cmd *cobra.Command
		for _, sub := range root.Commands() {
			if sub.Name() == "calibrate" {
				cmd = sub
			}
		}
		if cmd == nil {
			t.Fatal("missing calibrate")
		}
		cmd.InitDefaultHelpFlag()
		var locals []*pflag.Flag
		cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) { locals = append(locals, flag) })
		cmd.ResetFlags()
		for _, flag := range locals {
			cmd.Flags().AddFlag(flag)
		}
		cmd.Flags().Bool("profile", false, "local shadow")
		if _, err := helpFlags(cmd, settingrow.CommandCalibrate, settingrow.Table()); err == nil {
			t.Fatal("gave root metadata to an unbound local shadow")
		}
	})

	t.Run("should refuse an unclassified visible flag", func(t *testing.T) {
		cmd := helpTestCommand(t, settingrow.CommandRoot)
		cmd.Flags().Bool("unclassified", false, "extra")
		if _, err := helpFlags(cmd, settingrow.CommandRoot, settingrow.Table()); err == nil {
			t.Fatal("accepted unclassified flag")
		}
		cmd.Flags().Lookup("unclassified").Hidden = true
		if _, err := helpFlags(cmd, settingrow.CommandRoot, settingrow.Table()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestWriteCommandHelp(t *testing.T) {
	t.Run("should render a help line for every visible flag on every command", func(t *testing.T) {
		for _, cmd := range helpTestTree(t) {
			var out bytes.Buffer
			if err := writeCommandHelp(&out, cmd, "all"); err != nil {
				t.Fatalf("%s: %v", cmd.CommandPath(), err)
			}
			for _, set := range []*pflag.FlagSet{cmd.LocalFlags(), cmd.InheritedFlags()} {
				set.VisitAll(func(flag *pflag.Flag) {
					line := regexp.MustCompile(`(?m)^  (-[[:alnum:]], |    )--` + regexp.QuoteMeta(flag.Name) + `( |$)`)
					if !flag.Hidden && !line.MatchString(out.String()) {
						t.Errorf("%s has no help line for --%s", cmd.CommandPath(), flag.Name)
					}
				})
			}
		}
	})
	t.Run("should name no flag value STRING or INT on any command", func(t *testing.T) {
		generic := regexp.MustCompile(`(?m)^  (-[[:alnum:]], |    )--[[:alnum:]-]+ (STRING|INT)( |$)`)
		for _, cmd := range helpTestTree(t) {
			var out bytes.Buffer
			if err := writeCommandHelp(&out, cmd, "all"); err != nil {
				t.Fatalf("%s: %v", cmd.CommandPath(), err)
			}
			for _, line := range generic.FindAllString(out.String(), -1) {
				t.Errorf("%s has a generic value name: %s", cmd.CommandPath(), strings.TrimSpace(line))
			}
		}
	})
	t.Run("should describe -h as Show this help. on every command", func(t *testing.T) {
		help := regexp.MustCompile(`(?m)^  -h, --help +Show this help\.$`)
		cobraDefault := regexp.MustCompile(`--help +help for `)
		for _, cmd := range helpTestTree(t) {
			var out bytes.Buffer
			if err := writeCommandHelp(&out, cmd, "all"); err != nil {
				t.Fatalf("%s: %v", cmd.CommandPath(), err)
			}
			if !help.MatchString(out.String()) || cobraDefault.MatchString(out.String()) {
				t.Errorf("%s does not describe -h as Show this help.\n%s", cmd.CommandPath(), out.String())
			}
		}
	})
	t.Run("should find no command content that the help writers drop", func(t *testing.T) {
		for _, cmd := range helpTestTree(t) {
			if len(cmd.Aliases) > 0 || len(cmd.Groups()) > 0 || cmd.GroupID != "" || cmd.HasHelpSubCommands() {
				t.Errorf("%s has aliases, command groups or help topic commands, which the help writers do not render", cmd.CommandPath())
			}
		}
	})
	t.Run("should refuse an unclassified visible flag on a subcommand", func(t *testing.T) {
		root := helpTestCommand(t, settingrow.CommandRoot)
		cmd, _, err := root.Find([]string{"version"})
		if err != nil {
			t.Fatal(err)
		}
		cmd.InitDefaultHelpFlag()
		cmd.Flags().String("unclassified", "", "extra")
		if err := writeCommandHelp(io.Discard, cmd, ""); err == nil {
			t.Fatal("accepted an unclassified flag with a value")
		}
	})
}

func TestDescribedFlag(t *testing.T) {
	annotated := pflag.NewFlagSet("annotated", pflag.ContinueOnError)
	annotated.String("output", "", "format")
	borrowHelpValue(annotated, "output", "output")
	plain := pflag.NewFlagSet("plain", pflag.ContinueOnError)
	plain.String("unclassified", "", "extra")
	plain.Bool("switch", false, "a switch")
	plain.String("model", "", "the model")
	plain.String("profile", "", "the profile")
	tests := []struct {
		name       string
		flag       *pflag.Flag
		command    settingrow.Command
		persistent bool
		wantFound  bool
		wantValue  string
	}{
		{name: "should take the value name from a borrowed annotation", flag: annotated.Lookup("output"), wantFound: true, wantValue: "FORMAT"},
		{name: "should describe a bool flag as a switch", flag: plain.Lookup("switch"), wantFound: true},
		{name: "should refuse a valued flag with no row or annotation", flag: plain.Lookup("unclassified")},
		{name: "should describe a flag from the row its command binds", flag: plain.Lookup("model"), command: settingrow.CommandProfileCreate, wantFound: true, wantValue: "MODEL"},
		{name: "should accept an inherited flag only from a persistent binding", flag: plain.Lookup("profile"), command: settingrow.CommandRoot, persistent: true, wantFound: true, wantValue: "NAME"},
		{name: "should refuse an inherited flag whose binding is not persistent", flag: plain.Lookup("model"), command: settingrow.CommandRoot, persistent: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			described, found := describedFlag(tc.flag, tc.command, tc.persistent)
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
			}
			if !found {
				return
			}
			var line bytes.Buffer
			writeHelpFlag(&line, described)
			if got := strings.Fields(line.String()); tc.wantValue != "" && got[1] != tc.wantValue {
				t.Errorf("line = %q, want value name %s", line.String(), tc.wantValue)
			}
		})
	}
}

func TestBorrowHelpValue(t *testing.T) {
	tests := []struct {
		name string
		flag string
		row  settingrow.Name
	}{
		{name: "should panic on a flag the set lacks", flag: "missing", row: "output"},
		{name: "should panic on a row that does not exist", flag: "output", row: "no_such_row"},
		{name: "should panic on a row with no help value", flag: "output", row: "raw"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set := pflag.NewFlagSet("test", pflag.ContinueOnError)
			set.String("output", "", "format")
			defer func() {
				if recover() == nil {
					t.Fatal("did not panic")
				}
			}()
			borrowHelpValue(set, tc.flag, tc.row)
		})
	}
}

func helpTestTree(t *testing.T) []*cobra.Command {
	t.Helper()
	root := helpTestCommand(t, settingrow.CommandRoot)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	commands := []*cobra.Command{root}
	for i := 0; i < len(commands); i++ {
		commands[i].InitDefaultHelpFlag()
		commands = append(commands, commands[i].Commands()...)
	}
	if len(commands) < 15 {
		t.Fatalf("walked %d commands, want the whole tree", len(commands))
	}
	return commands
}

func TestWriteHelp(t *testing.T) {
	for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
		t.Run("should compose consistent views on "+command.String(), func(t *testing.T) {
			cmd := helpTestCommand(t, command)
			var all, overview bytes.Buffer
			if err := writeHelp(&all, cmd, command, "all"); err != nil {
				t.Fatal(err)
			}
			if err := writeHelp(&overview, cmd, command, ""); err != nil {
				t.Fatal(err)
			}
			if strings.Count(all.String(), "Exit codes:") != 1 || !strings.Contains(overview.String(), "Examples:") {
				t.Fatal("missing sections")
			}
			for _, topic := range helpTopics(command) {
				var page bytes.Buffer
				if err := writeHelp(&page, cmd, command, topic.name); err != nil {
					t.Fatal(err)
				}
				content := strings.Split(page.String(), "More help:")[0]
				if !strings.Contains(all.String(), strings.TrimSpace(content)) {
					t.Fatalf("%s missing shared topic body", topic.name)
				}
			}
			if err := writeHelp(&bytes.Buffer{}, cmd, command, "unknown"); err == nil {
				t.Fatal("unknown topic accepted")
			}
		lines:
			for _, line := range strings.Split(all.String(), "\n") {
				if len(line) > 88 && !strings.HasPrefix(line, "    onesie") && !strings.HasPrefix(line, "    echo") {
					for _, word := range strings.Fields(line) {
						if len(word) > 80 {
							continue lines
						}
					}
					t.Errorf("long line %d: %s", len(line), line)
				}
			}
		})
	}
	t.Run("should leave parser state unchanged across repeated views", func(t *testing.T) {
		cmd := helpTestCommand(t, settingrow.CommandRoot)
		if err := cmd.Flags().Set("state", "example"); err != nil {
			t.Fatal(err)
		}
		cmd.Flags().Lookup("stats").Hidden = true
		snapshot := func() string {
			var b strings.Builder
			cmd.Flags().VisitAll(func(f *pflag.Flag) {
				fmt.Fprintf(&b, "%s:%v:%s:%s:%v:%s\n", f.Name, f.Hidden, f.NoOptDefVal, f.DefValue, f.Changed, f.Value.String())
			})
			return b.String()
		}
		before := snapshot()
		for _, topic := range []string{"", "all", "asking", "all", ""} {
			if err := writeHelp(&bytes.Buffer{}, cmd, settingrow.CommandRoot, topic); err != nil {
				t.Fatal(err)
			}
		}
		if snapshot() != before {
			t.Fatal("rendering changed parser state")
		}
	})
	for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
		t.Run("should include every real flag exactly once on "+command.String(), func(t *testing.T) {
			cmd := helpTestCommand(t, command)
			var all, overview bytes.Buffer
			if err := writeHelp(&all, cmd, command, "all"); err != nil {
				t.Fatal(err)
			}
			if err := writeHelp(&overview, cmd, command, ""); err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, line := range strings.Split(all.String(), "\n") {
				signature := strings.HasPrefix(line, "      --") || strings.HasPrefix(line, "  -") && len(line) > 6 && line[4:6] == ", "
				if !signature {
					continue
				}
				_, rest, ok := strings.Cut(line, "--")
				if !ok {
					continue
				}
				name := strings.Fields(rest)[0]
				counts[name]++
			}
			cmd.Flags().VisitAll(func(flag *pflag.Flag) {
				if !flag.Hidden && counts[flag.Name] != 1 {
					t.Errorf("--%s occurs %d times", flag.Name, counts[flag.Name])
				}
			})
			flags, err := helpFlags(cmd, command, settingrow.Table())
			if err != nil {
				t.Fatal(err)
			}
			for _, flag := range flags {
				var entry bytes.Buffer
				writeHelpFlag(&entry, flag)
				if !strings.Contains(all.String(), entry.String()) {
					t.Fatalf("missing entry %s", flag.flag.Name)
				}
				if flag.commonRank > 0 && !strings.Contains(overview.String(), entry.String()) {
					t.Fatalf("overview differs for %s", flag.flag.Name)
				}
			}
			for _, sub := range cmd.Commands() {
				if slices.ContainsFunc(helpTopics(command), func(topic helpTopic) bool { return topic.name == sub.Name() }) {
					t.Fatalf("topic became command %s", sub.Name())
				}
			}
		})
	}
	t.Run("should return the output writer failure", func(t *testing.T) {
		want := errors.New("writer failed")
		err := writeHelp(helpErrorWriter{want}, helpTestCommand(t, settingrow.CommandRoot), settingrow.CommandRoot, "all")
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	})
}

func TestHelpInput(t *testing.T) {
	t.Run("should describe the separator owned by input and the CSV writer", func(t *testing.T) {
		if csv.NewWriter(io.Discard).Comma != delimiter.DefaultComma {
			t.Fatal("CSV output default differs from input")
		}
		if !strings.Contains(helpInput(settingrow.CommandRoot), strconv.QuoteRune(delimiter.DefaultComma)) {
			t.Fatal("missing owned delimiter")
		}
	})
}

func TestResolveHelp(t *testing.T) {
	for _, tc := range []struct {
		args        []string
		path, topic string
		fails       bool
	}{
		{[]string{"decisions"}, "onesie", "decisions", false},
		{[]string{"calibrate", "evaluation"}, "onesie calibrate", "evaluation", false},
		{[]string{"exit-codes"}, "onesie", "exit-codes", false},
		{[]string{"calibrate", "exit-codes"}, "onesie calibrate", "exit-codes", false},
		{[]string{"questions"}, "onesie questions", "", false},
		{[]string{"profile", "create"}, "onesie profile create", "", false},
		{[]string{"decisions", "extra"}, "", "", true},
		{[]string{"calibrate", "decisions"}, "", "", true},
	} {
		t.Run("should resolve "+strings.Join(tc.args, " "), func(t *testing.T) {
			root := helpTestCommand(t, settingrow.CommandRoot)
			cmd, topic, err := resolveHelp(root, tc.args)
			if tc.fails {
				if err == nil {
					t.Fatal("accepted invalid route")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cmd.CommandPath() != tc.path || topic != tc.topic {
				t.Fatalf("resolved %s %s", cmd.CommandPath(), topic)
			}
		})
	}
}

func TestCompleteHelp(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		prefix string
		want   []string
	}{
		{nil, "", []string{"all", "asking", "cache", "calibrate", "completion", "connections", "decisions", "execution", "exit-codes", "help", "input", "output", "profile", "questions", "version"}},
		{[]string{"calibrate"}, "", []string{"all", "asking", "connections", "evaluation", "execution", "exit-codes", "input", "output"}},
		{nil, "dec", []string{"decisions"}},
		{[]string{"decisions"}, "", nil},
		{[]string{"profile"}, "cr", []string{"create"}},
	} {
		t.Run("should complete "+strings.Join(tc.args, " ")+" with "+tc.prefix, func(t *testing.T) {
			root := helpTestCommand(t, settingrow.CommandRoot)
			got, directive := completeHelp(root, tc.args, tc.prefix)
			if slices.Equal(tc.args, []string{"profile"}) && (len(got) != 1 || !strings.Contains(got[0], "\t")) {
				t.Fatal("lost ordinary command completion descriptions")
			}
			for i, item := range got {
				got[i], _, _ = strings.Cut(item, "\t")
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) || directive != cobra.ShellCompDirectiveNoFileComp {
				t.Fatalf("got %v directive %v", got, directive)
			}
		})
	}
}

func TestWriteHelpFlag(t *testing.T) {
	t.Run("should separate an unpunctuated description from its default", func(t *testing.T) {
		var b bytes.Buffer
		writeHelpFlag(&b, helpFlag{flag: &pflag.Flag{Name: "input", Usage: "read records", DefValue: "text"}, bound: settingrow.BoundFlag{Kind: settingrow.Text()}})
		if !strings.Contains(b.String(), "read records. Default: text.") {
			t.Fatal(b.String())
		}
	})
}

func TestWriteHelpText(t *testing.T) {
	t.Run("should preserve a literal sample line", func(t *testing.T) {
		literal := `    {"id":"a","text":"The server is down","urgent":true}`
		var b bytes.Buffer
		writeHelpText(&b, "A record:\n\n"+literal, "  ", "  ")
		if !strings.Contains(b.String(), literal+"\n") {
			t.Fatal(b.String())
		}
	})
}

func TestHelpShellQuote(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"billing,support", "billing,support"}, {"", "''"}, {"#comment", "'#comment'"}, {"~/file", "'~/file'"}, {"a'b", "'a'\"'\"'b'"}, {"$(command)", "'$(command)'"},
	} {
		t.Run("should quote "+tc.value, func(t *testing.T) {
			if got := helpShellQuote(tc.value); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func helpTestCommand(t *testing.T, command settingrow.Command) *cobra.Command {
	t.Helper()
	root := NewRootCmd(BuildInfo{}, WithLookupEnv(func(string) (string, bool) { return "", false }), WithHomeDir(func() (string, error) { return t.TempDir(), nil }), WithWorkingDir(func() (string, error) { return t.TempDir(), nil }))
	cmd := root.Command
	if command == settingrow.CommandCalibrate {
		var err error
		cmd, _, err = cmd.Find([]string{"calibrate"})
		if err != nil {
			t.Fatal(err)
		}
	}
	cmd.InitDefaultHelpFlag()
	cmd.InheritedFlags()
	cmd.LocalFlags()
	return cmd
}
