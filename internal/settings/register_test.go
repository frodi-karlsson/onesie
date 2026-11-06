package settings_test

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestRegister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "should register exactly its rows on each command", run: func(t *testing.T) {
			for _, command := range []settings.Command{
				settings.CommandRoot, settings.CommandCalibrate, settings.CommandProfileCreate,
				settings.CommandProfileUpdate,
			} {
				registered := register(command)

				var want []string

				for _, row := range settings.Table() {
					if flag, ok := row.FlagOn(command); ok {
						want = append(want, flag.Long)
					}
				}

				if got := registered.names(); !slices.Equal(got, sorted(want)) {
					t.Errorf("%s registers %v, want %v", command, got, sorted(want))
				}
			}
		}},
		{name: "should count each Set of a flag that is not repeatable", run: func(t *testing.T) {
			registered := register(settings.CommandRoot)
			registered.parse(t, "-m", "a", "--model=b", "-qm", "c", "--assert", "x", "--assert", "y", "--threshold", "0.5")

			for name, want := range map[settings.Name]int{
				"model": 3, "quiet": 1, "assert": 0, "threshold": 1, "stats": 0,
			} {
				if got := registered.given.Count(name); got != want {
					t.Errorf("Count(%s) = %d, want %d", name, got, want)
				}
			}

			if got := *registered.targets["model"].(*string); got != "c" {
				t.Errorf("model = %q, want the last value c", got)
			}
		}},
		{name: "should send group rows through the recorder", run: func(t *testing.T) {
			registered := register(settings.CommandRoot)
			registered.parse(t, "--ask", "a=is it", "--pick", "x,y", "--box", "--sep", " ", "--threshold", "0.4")

			want := []argv.Event{
				{Name: "ask", Value: "a=is it"},
				{Name: "pick", Value: "x,y"},
				{Name: "box", Value: "true"},
				{Name: "sep", Value: " "},
				{Name: "threshold", Value: "0.4"},
			}

			if got := registered.recorder.Events(); !slices.Equal(got, want) {
				t.Errorf("events = %v, want %v", got, want)
			}
		}},
		{name: "should keep help text equal to Row.Help or the command's own", run: func(t *testing.T) {
			registered := register(settings.CommandCalibrate)

			for _, row := range settings.Table() {
				flag, ok := row.FlagOn(settings.CommandCalibrate)
				if !ok {
					continue
				}

				want := row.Help
				if own := row.Flag.PerCommand[settings.CommandCalibrate].Help; own != "" {
					want = own
				}

				if got := registered.lookup(flag.Long).Usage; got != want {
					t.Errorf("--%s help = %q, want %q", flag.Long, got, want)
				}
			}

			if got := registered.lookup("output").Usage; got != "the report, table, json or auto, which means table" {
				t.Errorf("calibrate -o help = %q, want its own", got)
			}
		}},
		{name: "should register --version before any other flag on root", run: func(t *testing.T) {
			set := pflag.NewFlagSet("onesie", pflag.ContinueOnError)
			set.SortFlags = false
			persistent := pflag.NewFlagSet("persistent", pflag.ContinueOnError)
			rows := settings.Table()
			settings.Register(set, persistent, rows, argv.New(), settings.CommandRoot,
				targetsFor(rows, settings.CommandRoot))

			var first string

			set.VisitAll(func(flag *pflag.Flag) {
				if first == "" {
					first = flag.Name
				}
			})

			if first != "version" || set.Lookup("version").Shorthand != "V" {
				t.Errorf("first flag = %q, want version as -V", first)
			}
		}},
		{name: "should put a row persistent only on the commands that mark it", run: func(t *testing.T) {
			root := register(settings.CommandRoot)
			if root.persistent.Lookup("provider") == nil || root.set.Lookup("provider") != nil {
				t.Error("root --provider is not persistent")
			}

			if root.persistent.Lookup("profile") == nil || root.persistent.Lookup("model") != nil {
				t.Error("root persists the wrong flags")
			}

			create := register(settings.CommandProfileCreate)
			if create.set.Lookup("provider") == nil || create.persistent.Lookup("provider") != nil {
				t.Error("profile create --provider is not local")
			}
		}},
		{name: "should refuse an empty value with ErrEmptyValue for a non empty kind", run: func(t *testing.T) {
			for _, args := range [][]string{
				{"--model", " "},
				{"-o", ""},
				{"--provider", ""},
				{"--image-field", " "},
				{"--sep", ""},
				{"--base-url", ""},
				{"--map", ""},
			} {
				err := register(settings.CommandRoot).parseErr(args...)
				if !errors.Is(err, settings.ErrEmptyValue) {
					t.Errorf("%v error = %v, want ErrEmptyValue", args, err)
				}
			}

			for _, args := range [][]string{{"--state", ""}, {"--delimiter", ""}, {"--sep", " "}, {"--assert", ""}} {
				if err := register(settings.CommandRoot).parseErr(args...); err != nil {
					t.Errorf("%v error = %v, want none", args, err)
				}
			}

			update := register(settings.CommandProfileUpdate)
			if err := update.parseErr("--model", "", "--base-url", "", "--account-id", ""); err != nil {
				t.Errorf("profile update refuses an empty member: %v", err)
			}

			if err := update.parseErr("--provider", ""); !errors.Is(err, settings.ErrEmptyValue) {
				t.Errorf("profile update --provider '' error = %v, want ErrEmptyValue", err)
			}
		}},
		{name: "should refuse an empty and a blank value on exactly the pinned flags of each command", run: func(t *testing.T) {
			runs := []string{"base-url", "file", "id", "image-field", "input", "map", "mock", "model", "out", "output"}
			root := sorted(append(slices.Clone(runs), "merge-key", "profile", "provider", "state-file"))
			calibrate := sorted(append(slices.Clone(runs), "label"))

			for _, pin := range []struct {
				command      settings.Command
				empty, blank []string
			}{
				{command: settings.CommandRoot, empty: sorted(append(slices.Clone(root), "sep")), blank: root},
				{command: settings.CommandCalibrate, empty: sorted(append(slices.Clone(calibrate), "sep")), blank: calibrate},
				{
					command: settings.CommandProfileCreate,
					empty:   []string{"account-id", "base-url", "model"}, blank: []string{"account-id", "base-url", "model"},
				},
				{command: settings.CommandProfileUpdate, empty: []string{"provider"}, blank: []string{"provider"}},
			} {
				for value, want := range map[string][]string{"": pin.empty, " ": pin.blank} {
					var got []string

					for _, name := range register(pin.command).names() {
						if errors.Is(register(pin.command).parseErr("--"+name+"="+value), settings.ErrEmptyValue) {
							got = append(got, name)
						}
					}

					if !slices.Equal(got, want) {
						t.Errorf("%s refuses %q on %v, want %v", pin.command, value, got, want)
					}
				}
			}
		}},
		{name: "should keep pflag's own message for a bad int", run: func(t *testing.T) {
			err := register(settings.CommandRoot).parseErr("-j", "x")

			var invalid *pflag.InvalidValueError
			if !errors.As(err, &invalid) || invalid.GetFlag().Value.Type() != "int" {
				t.Fatalf("-j x error = %v, want pflag's invalid value error for an int", err)
			}

			if !strings.Contains(err.Error(), `invalid argument "x" for "-j, --jobs" flag`) {
				t.Errorf("-j x error = %q, want pflag's own message", err)
			}
		}},
		{name: "should mark --provider given through Changed", run: func(t *testing.T) {
			registered := register(settings.CommandRoot)
			registered.parse(t, "--provider", "berget")

			if !registered.persistent.Changed("provider") || registered.given.Count("provider") != 1 {
				t.Error("--provider is not marked given")
			}

			if registered.persistent.Changed("profile") || registered.given.Count("profile") != 0 {
				t.Error("--profile is marked given")
			}
		}},
		{name: "should give the shared run flags one set of defaults and leave -i and -o out", run: func(t *testing.T) {
			root, calibrate := register(settings.CommandRoot), register(settings.CommandCalibrate)

			got := map[string]string{}

			for _, row := range settings.Table() {
				_, onRoot := row.FlagOn(settings.CommandRoot)
				_, onCalibrate := row.FlagOn(settings.CommandCalibrate)

				if !onRoot || !onCalibrate || row.Flag.Group || row.Name == "input" || row.Name == "output" {
					continue
				}

				long := row.Flag.Long
				if root.lookup(long).DefValue != calibrate.lookup(long).DefValue {
					t.Errorf("--%s defaults to %q on root and %q on calibrate", long,
						root.lookup(long).DefValue, calibrate.lookup(long).DefValue)
				}

				got[long] = root.lookup(long).DefValue
			}

			want := map[string]string{
				"image": "[]", "image-field": "", "map": "", "id": "", "model": "", "base-url": "",
				"allow-http": "false", "long-context": "false", "file": "", "replace": "false", "jobs": "1",
				"timeout":         strconv.Itoa(int(limits.DefaultAttemptTimeout.Seconds())),
				"retries":         strconv.Itoa(limits.DefaultRetries),
				"max-retry-after": strconv.Itoa(int(limits.DefaultMaxRetryAfter.Seconds())),
				"out":             "", "resume": "false", "prune": "false", "skip-blank": "false", "delimiter": "", "stats": "false",
				"usage": "false", "print-request": "false", "cache": "false", "mock": "",
			}

			if !maps.Equal(got, want) {
				t.Errorf("flags and defaults = %v, want %v", got, want)
			}

			if *calibrate.targets["jobs"].(*int) != 1 || *calibrate.targets["input"].(*string) != "" ||
				*root.targets["input"].(*string) != "text" {
				t.Error("jobs is not 1, or -i does not default to text on root and to nothing on calibrate")
			}
		}},
		{name: "should panic with a named message when a row has no flag set or recorder to go to", run: func(t *testing.T) {
			for _, tc := range []struct {
				recorder   *argv.Recorder
				persistent *pflag.FlagSet
				want       string
			}{
				{recorder: argv.New(), want: "provider on onesie is persistent"},
				{persistent: pflag.NewFlagSet("persistent", pflag.ContinueOnError), want: "ask on onesie is a group flag"},
			} {
				func() {
					defer func() {
						if got := fmt.Sprint(recover()); !strings.Contains(got, tc.want) {
							t.Errorf("panic = %q, want it to hold %q", got, tc.want)
						}
					}()

					rows := settings.Table()
					settings.Register(pflag.NewFlagSet("onesie", pflag.ContinueOnError), tc.persistent, rows,
						tc.recorder, settings.CommandRoot, targetsFor(rows, settings.CommandRoot))
				}()
			}
		}},
		{name: "should mark the flags that ask nothing", run: func(t *testing.T) {
			registered := register(settings.CommandRoot)

			for _, name := range []string{"version", "print-schema", "list-models", "model"} {
				_, marked := registered.lookup(name).Annotations[settings.AnnotationAsksNothing]
				if want := name != "model"; marked != want {
					t.Errorf("--%s marked as asking nothing = %v, want %v", name, marked, want)
				}
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestGiven_Spelled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		row  settings.Name
		want []string
	}{
		{name: "should spell -qm a -m b as -m twice", args: []string{"-qm", "a", "-m", "b"}, row: "model", want: []string{"-m", "-m"}},
		{name: "should spell --model=x as --model", args: []string{"--model=x", "question"}, row: "model", want: []string{"--model"}},
		{name: "should stop at --", args: []string{"-m", "a", "--", "-m", "b"}, row: "model", want: []string{"-m"}},
		{
			name: "should read a value that looks like a flag as a value",
			args: []string{"--model", "--stats", "-m", "-q"}, row: "stats",
		},
		{
			name: "should read the rest of a cluster as the value of a flag that takes one",
			args: []string{"-mq", "-q"}, row: "quiet", want: []string{"-q"},
		},
		{name: "should spell nothing for a row the command does not register", args: []string{"--label", "x"}, row: "label"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := register(settings.CommandRoot).given.Spelled(tc.args, tc.row); !slices.Equal(got, tc.want) {
				t.Errorf("Spelled(%v, %s) = %v, want %v", tc.args, tc.row, got, tc.want)
			}
		})
	}
}

func TestGiven_Inherit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "should read a persistent flag of the parent",
			run: func(t *testing.T) {
				t.Helper()

				root := register(settings.CommandRoot)
				calibrate := register(settings.CommandCalibrate)
				calibrate.persistent = root.persistent
				calibrate.given.Inherit(root.persistent)
				calibrate.parse(t, "--profile", "work")

				layers, err := settings.Read(settings.Table(), settings.Sources{
					Given: calibrate.given, Facts: settings.Facts{Command: settings.CommandCalibrate},
				})
				if err != nil {
					t.Fatalf("Read: %v", err)
				}

				wantValue(t, layerAt(t, layers, settings.SourceFlag), "profile", "work", "--profile")
			},
		},
		{
			name: "should do nothing on a nil Given",
			run: func(t *testing.T) {
				t.Helper()

				var given *settings.Given

				given.Inherit(pflag.NewFlagSet("persistent", pflag.ContinueOnError))

				if got := given.Count("profile"); got != 0 {
					t.Errorf("Count = %d, want 0", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func TestGiven_Values(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		row  settings.Name
		want []string
	}{
		{name: "should list each value typed, in order", args: []string{"-m", "a", "--model=b"}, row: "model", want: []string{"a", "b"}},
		{name: "should list the value a bare switch sets", args: []string{"--cache"}, row: "cache", want: []string{"true"}},
		{name: "should list nothing for a repeatable flag", args: []string{"--assert", "x", "--assert", "y"}, row: "assert"},
		{name: "should list nothing for a flag not typed", args: []string{"q"}, row: "model"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := register(settings.CommandRoot)
			root.parse(t, tc.args...)

			if got := root.given.Values(tc.row); !slices.Equal(got, tc.want) {
				t.Errorf("Values(%s) = %v, want %v", tc.row, got, tc.want)
			}
		})
	}

	t.Run("should list nothing on a nil Given", func(t *testing.T) {
		t.Parallel()

		var given *settings.Given

		if got := given.Values("model"); got != nil {
			t.Errorf("Values = %v, want nil", got)
		}
	})
}

func TestGiven_Within(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "should count, list and spell a persistent flag of the parent",
			run: func(t *testing.T) {
				t.Helper()

				root := register(settings.CommandRoot)
				calibrate := register(settings.CommandCalibrate)
				calibrate.persistent = root.persistent
				calibrate.given.Inherit(root.persistent)
				calibrate.given.Within(root.given)

				args := []string{"-i", "jsonl", "--profile", "a", "--profile=b"}
				calibrate.parse(t, args...)

				if got := calibrate.given.Count("profile"); got != 2 {
					t.Errorf("Count = %d, want 2", got)
				}

				if got := calibrate.given.Values("profile"); !slices.Equal(got, []string{"a", "b"}) {
					t.Errorf("Values = %v, want [a b]", got)
				}

				if got := calibrate.given.Spelled(args, "profile"); !slices.Equal(got, []string{"--profile", "--profile"}) {
					t.Errorf("Spelled = %v, want --profile twice", got)
				}

				if got := calibrate.given.Values("input"); !slices.Equal(got, []string{"jsonl"}) {
					t.Errorf("Values(input) = %v, want its own -i", got)
				}
			},
		},
		{
			name: "should do nothing on a nil Given",
			run: func(t *testing.T) {
				t.Helper()

				var given *settings.Given

				given.Within(register(settings.CommandRoot).given)

				if got := given.Count("profile"); got != 0 {
					t.Errorf("Count = %d, want 0", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

func register(command settings.Command) registered {
	rows := settings.Table()
	r := registered{
		set:        pflag.NewFlagSet(command.String(), pflag.ContinueOnError),
		persistent: pflag.NewFlagSet("persistent", pflag.ContinueOnError),
		recorder:   argv.New(),
		targets:    targetsFor(rows, command),
	}
	r.given = settings.Register(r.set, r.persistent, rows, r.recorder, command, r.targets)

	return r
}

type registered struct {
	set, persistent *pflag.FlagSet
	recorder        *argv.Recorder
	targets         map[settings.Name]any
	given           *settings.Given
}

func (r registered) names() []string {
	var names []string

	for _, set := range []*pflag.FlagSet{r.set, r.persistent} {
		set.VisitAll(func(flag *pflag.Flag) { names = append(names, flag.Name) })
	}

	return sorted(names)
}

func (r registered) parse(t *testing.T, args ...string) {
	t.Helper()

	if err := r.parseErr(args...); err != nil {
		t.Fatal(err)
	}
}

func (r registered) parseErr(args ...string) error {
	r.set.AddFlagSet(r.persistent)

	return r.set.Parse(args)
}

func (r registered) lookup(name string) *pflag.Flag {
	if flag := r.set.Lookup(name); flag != nil {
		return flag
	}

	return r.persistent.Lookup(name)
}

func targetsFor(rows []settings.Row, command settings.Command) map[settings.Name]any {
	targets := map[settings.Name]any{}

	for _, row := range rows {
		flag, ok := row.FlagOn(command)
		if !ok || row.Flag.Group {
			continue
		}

		switch {
		case row.Flag.Repeat:
			targets[row.Name] = new([]string)
		default:
			switch flag.Kind.Zero().(type) {
			case bool:
				targets[row.Name] = new(bool)
			case int:
				targets[row.Name] = new(int)
			default:
				targets[row.Name] = new(string)
			}
		}
	}

	return targets
}

func sorted(names []string) []string {
	names = slices.Clone(names)
	slices.Sort(names)

	return names
}
