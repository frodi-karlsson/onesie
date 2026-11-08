package cli

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestHelpTopics(t *testing.T) {
	t.Parallel()

	variables := map[string]bool{}
	for _, row := range settingrow.Table() {
		for _, variable := range row.Env {
			variables[variable.Name] = true
		}
	}

	flagName := regexp.MustCompile(`--[a-z][a-z-]*`)
	shortFlag := regexp.MustCompile(`(?:^|\s)-([A-Za-z])\b`)
	variableName := regexp.MustCompile(`[A-Z][A-Z0-9]*_[A-Z0-9_]+`)

	for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
		pages := map[string]string{"overview": ""}
		for _, topic := range helpTopics(command) {
			pages[topic.name] = topic.name
		}
		for page, topic := range pages {
			t.Run("should name only real flags and variables in "+command.String()+" "+page, func(t *testing.T) {
				t.Parallel()

				cmd := helpTestCommand(t, command)
				var text bytes.Buffer
				if err := writeHelp(&text, cmd, command, topic); err != nil {
					t.Fatal(err)
				}

				for _, flag := range flagName.FindAllString(text.String(), -1) {
					if cmd.Flags().Lookup(strings.TrimPrefix(flag, "--")) == nil {
						t.Errorf("%s names %s, which %s does not register", page, flag, command)
					}
				}

				for _, match := range shortFlag.FindAllStringSubmatch(text.String(), -1) {
					if cmd.Flags().ShorthandLookup(match[1]) == nil {
						t.Errorf("%s names -%s, which %s does not register", page, match[1], command)
					}
				}

				for _, variable := range variableName.FindAllString(text.String(), -1) {
					if !variables[variable] {
						t.Errorf("%s names %s, which no settings row reads", page, variable)
					}
				}
			})
		}
	}

	for _, tc := range []struct {
		command settingrow.Command
		want    []string
	}{
		{settingrow.CommandRoot, []string{"asking", "input", "output", "decisions", "connections", "execution"}},
		{settingrow.CommandCalibrate, []string{"asking", "input", "evaluation", "output", "connections", "execution"}},
	} {
		t.Run("should give the "+tc.command.String()+" overview its topic order", func(t *testing.T) {
			t.Parallel()

			var got []string
			for _, topic := range overviewOrder(helpTopics(tc.command)) {
				got = append(got, topic.name)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("should list every declared exit code on the root exit-codes topic", func(t *testing.T) {
		t.Parallel()

		declared := declaredExitCodes(t)
		if listed := listedExitCodes(t, settingrow.CommandRoot); !slices.Equal(listed, declared) {
			t.Fatalf("help exit-codes lists %v, exit.go declares %v", listed, declared)
		}
	})

	t.Run("should list only declared exit codes in ascending order on the calibrate exit-codes topic", func(t *testing.T) {
		t.Parallel()

		declared := declaredExitCodes(t)
		listed := listedExitCodes(t, settingrow.CommandCalibrate)
		if len(listed) == 0 || !slices.IsSorted(listed) {
			t.Fatalf("help calibrate exit-codes lists %v", listed)
		}
		for _, code := range listed {
			if !slices.Contains(declared, code) {
				t.Errorf("help calibrate exit-codes lists %d, which exit.go does not declare", code)
			}
		}
	})
}

func declaredExitCodes(t *testing.T) []int {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "exit.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var codes []int
	for _, decl := range file.Decls {
		group, isGroup := decl.(*ast.GenDecl)
		if !isGroup || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue {
				continue
			}
			for i, name := range value.Names {
				if !strings.HasPrefix(name.Name, "Exit") || i >= len(value.Values) {
					continue
				}
				literal, isLiteral := value.Values[i].(*ast.BasicLit)
				if !isLiteral {
					t.Fatalf("%s is not a literal", name.Name)
				}
				code, err := strconv.Atoi(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				codes = append(codes, code)
			}
		}
	}

	slices.Sort(codes)
	return codes
}

func listedExitCodes(t *testing.T, command settingrow.Command) []int {
	t.Helper()

	var page bytes.Buffer
	if err := writeHelp(&page, helpTestCommand(t, command), command, "exit-codes"); err != nil {
		t.Fatal(err)
	}

	line := regexp.MustCompile(`^  ([0-9]+) +\S`)
	var codes []int
	for _, text := range strings.Split(page.String(), "\n") {
		if match := line.FindStringSubmatch(text); match != nil {
			code, _ := strconv.Atoi(match[1])
			codes = append(codes, code)
		}
	}
	return codes
}

func TestBaseURLPrecedence(t *testing.T) {
	t.Parallel()

	endpoint, found := settingrow.Lookup(setting.APIEndpoint.Name())

	for _, tc := range []struct {
		name  string
		row   settingrow.Row
		found bool
		want  string
	}{
		{
			name: "should give each provider its own sentence when two providers have a base URL variable",
			row: settingrow.Row{Env: []settingrow.EnvSpec{
				{Name: "ONESIE_BASE_URL", Half: settingrow.HalfBaseURL},
				{Name: "TYPESAFE_BASE_URL", Provider: "typesafe", Half: settingrow.HalfBaseURL},
				{Name: "CLOUDFLARE_ACCOUNT_ID", Provider: "cloudflare", Half: settingrow.HalfAccount},
				{Name: "BERGET_BASE_URL", Provider: "berget", Half: settingrow.HalfBaseURL},
			}},
			found: true,
			want:  "On typesafe, ONESIE_BASE_URL beats TYPESAFE_BASE_URL. On berget, ONESIE_BASE_URL beats BERGET_BASE_URL.",
		},
		{
			name:  "should say nothing when no provider has a base URL variable",
			row:   settingrow.Row{Env: []settingrow.EnvSpec{{Name: "ONESIE_BASE_URL", Half: settingrow.HalfBaseURL}}},
			found: true,
		},
		{
			name: "should say nothing when the endpoint row is missing",
			row:  settingrow.Row{Env: []settingrow.EnvSpec{{Name: "TYPESAFE_BASE_URL", Provider: "typesafe", Half: settingrow.HalfBaseURL}}},
		},
		{
			name:  "should order the endpoint row's variables on typesafe",
			row:   endpoint,
			found: found,
			want:  "On typesafe, ONESIE_BASE_URL beats TYPESAFE_BASE_URL.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := baseURLPrecedence(tc.row, tc.found); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
