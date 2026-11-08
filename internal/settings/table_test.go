package settings_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestTable(t *testing.T) {
	t.Parallel()

	commands := []settingrow.Command{
		settingrow.CommandRoot, settingrow.CommandCalibrate, settingrow.CommandProfileCreate, settingrow.CommandProfileUpdate,
	}

	tests := []struct {
		name  string
		check func(rows []settingrow.Row) []string
	}{
		{
			name: "should give each command unique long names and shorthands",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				for _, command := range commands {
					longs, shorts := map[string]settingrow.Name{}, map[string]settingrow.Name{}

					for _, row := range rows {
						flag, ok := row.FlagOn(command)
						if !ok {
							continue
						}

						if other, taken := longs[flag.Long]; taken {
							problems = append(problems, fmt.Sprintf("%s: --%s is both %s and %s", command, flag.Long, other, row.Name))
						}

						longs[flag.Long] = row.Name

						if other, taken := shorts[flag.Short]; taken && flag.Short != "" {
							problems = append(problems, fmt.Sprintf("%s: -%s is both %s and %s", command, flag.Short, other, row.Name))
						}

						shorts[flag.Short] = row.Name
					}
				}

				return problems
			},
		},
		{
			name: "should give every row help on every command that registers it",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				for _, command := range commands {
					for _, row := range rows {
						if flag, ok := row.FlagOn(command); ok && flag.Help == "" {
							problems = append(problems, fmt.Sprintf("%s: %s has no help", command, row.Name))
						}
					}
				}

				return problems
			},
		},
		{
			name: "should name the shapes of every question row",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				for _, row := range rows {
					switch {
					case row.Scope == settingrow.ScopeQuestion && len(row.Shapes) == 0:
						problems = append(problems, fmt.Sprintf("%s is per question and names no shape", row.Name))
					case row.Scope == settingrow.ScopeRun && len(row.Shapes) > 0:
						problems = append(problems, fmt.Sprintf("%s is per run and names shapes", row.Name))
					}
				}

				return problems
			},
		},
		{
			name: "should give every key a row whose kind yields its type",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				seen := map[settingrow.Name]bool{}

				for _, key := range settings.Keys() {
					if seen[key.Name()] {
						problems = append(problems, fmt.Sprintf("Keys lists %s twice", key.Name()))
					}

					seen[key.Name()] = true

					row, found := settingrow.Lookup(key.Name())

					switch {
					case !found:
						problems = append(problems, fmt.Sprintf("key %s has no row", key.Name()))
					case !key.Fits(row.Kind):
						problems = append(problems, fmt.Sprintf("key %s does not fit its row's kind, which yields a %T",
							key.Name(), row.Kind.Zero()))
					}
				}

				return problems
			},
		},
		{
			name: "should keep env names unique per provider, and give an order to one row and one help",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				names, orders := map[string]settingrow.Name{}, map[int]settingrow.Row{}

				for _, row := range rows {
					for _, env := range row.Env {
						spelled := env.Name + " on " + env.Provider
						if other, taken := names[spelled]; taken {
							problems = append(problems, fmt.Sprintf("%s is both %s and %s", spelled, other, row.Name))
						}

						names[spelled] = row.Name

						if other, taken := orders[env.Order]; taken && other.Name != row.Name {
							problems = append(problems, fmt.Sprintf("order %d is both %s and %s", env.Order, other.Name, row.Name))
						} else if taken && !sharesHelp(other, env) {
							problems = append(problems, fmt.Sprintf("order %d of %s holds two helps", env.Order, row.Name))
						}

						orders[env.Order] = row

						if env.Help == "" {
							problems = append(problems, env.Name+" has no help")
						}
					}
				}

				return problems
			},
		},
		{
			name: "should give every row with a file key its file help",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				for _, row := range rows {
					if row.FileKey != "" && row.FileHelp == "" {
						problems = append(problems, fmt.Sprintf("%s has file key %s and no file help", row.Name, row.FileKey))
					}
				}

				return problems
			},
		},
		{
			name: "should name only members FileMembers holds",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				var members []string
				for _, member := range creds.FileMembers() {
					members = append(members, member.Keys...)
				}

				for _, row := range rows {
					for _, member := range row.Profile {
						if !slices.Contains(members, member.Key) {
							problems = append(problems, fmt.Sprintf("%s names %s, which FileMembers lacks", row.Name, member.Key))
						}
					}
				}

				return problems
			},
		},
		{
			name: "should map each profile writer flag that writes a member to one FileMembers holds",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				var members []string
				for _, member := range creds.FileMembers() {
					members = append(members, member.Keys...)
				}

				wants := map[string]string{"provider": "provider", "model": "model", "base-url": "base_url", "account-id": "account_id"}

				for _, command := range []settingrow.Command{settingrow.CommandProfileCreate, settingrow.CommandProfileUpdate} {
					for _, row := range rows {
						flag, ok := row.FlagOn(command)
						if !ok {
							continue
						}

						if want := wants[flag.Long]; flag.Member.Key != want {
							problems = append(problems, fmt.Sprintf("%s: --%s writes %q, want %q", command, flag.Long, flag.Member.Key, want))
						}

						if flag.Member.Key != "" && !slices.Contains(members, flag.Member.Key) {
							problems = append(problems, fmt.Sprintf("%s: --%s writes %s, which FileMembers lacks",
								command, flag.Long, flag.Member.Key))
						}
					}
				}

				for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
					for _, row := range rows {
						if flag, ok := row.FlagOn(command); ok && flag.Member.Key != "" {
							problems = append(problems, fmt.Sprintf("%s: --%s writes a profile member", command, flag.Long))
						}
					}
				}

				return problems
			},
		},
		{
			name: "should give every provider's key variable to the api_key row",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				row, found := settingrow.Lookup("api_key")
				if !found || len(row.Env) == 0 {
					return []string{"no api_key row with variables"}
				}

				for _, provider := range onesie.Providers() {
					if !slices.Contains(row.Env, settingrow.EnvSpec{
						Name: provider.EnvAPIKey(), Provider: provider.Name(), Order: row.Env[0].Order, Help: row.Env[0].Help,
					}) {
						problems = append(problems, provider.EnvAPIKey()+" is not a variable of the api_key row")
					}
				}

				return problems
			},
		},
		{
			name: "should mark only run rows with a file key as request body keys",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				for _, row := range rows {
					if row.InBody && (row.FileKey == "" || row.Scope != settingrow.ScopeRun) {
						problems = append(problems, fmt.Sprintf("%s is a body key with no file key or on a question", row.Name))
					}
				}

				return problems
			},
		},
		{
			name: "should give every default the type of its kind",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				for _, command := range commands {
					for _, row := range rows {
						flag, ok := row.FlagOn(command)
						if ok && flag.Default != nil && fmt.Sprintf("%T", flag.Default) != fmt.Sprintf("%T", flag.Kind.Zero()) {
							problems = append(problems, fmt.Sprintf("%s: %s defaults to a %T, and its kind yields a %T",
								command, row.Name, flag.Default, flag.Kind.Zero()))
						}
					}
				}

				return problems
			},
		},
		{
			name: "should not start a flag's help with its value name",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				for _, command := range commands {
					for _, row := range rows {
						flag, ok := row.FlagOn(command)
						if ok && flag.HelpValue != "" && strings.HasPrefix(flag.Help, flag.HelpValue) {
							problems = append(problems, fmt.Sprintf("%s: --%s help repeats its value name %s", command, flag.Long, flag.HelpValue))
						}
					}
				}

				return problems
			},
		},
		{
			name: "should write every flag help as sentences that start with a capital and end with a period",
			check: func(rows []settingrow.Row) []string {
				var problems []string

				for _, command := range commands {
					for _, row := range rows {
						flag, ok := row.FlagOn(command)
						if !ok {
							continue
						}

						first, _ := utf8.DecodeRuneInString(flag.Help)
						if !unicode.IsUpper(first) || !strings.HasSuffix(flag.Help, ".") {
							problems = append(problems, fmt.Sprintf("%s: --%s help %q is not a sentence", command, flag.Long, flag.Help))
						}
					}
				}

				return problems
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, problem := range tc.check(settingrow.Table()) {
				t.Error(problem)
			}
		})
	}
}

func sharesHelp(row settingrow.Row, env settingrow.EnvSpec) bool {
	for _, other := range row.Env {
		if other.Order == env.Order && other.Help != env.Help {
			return false
		}
	}

	return true
}

func TestLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		row       settingrow.Name
		wantFound bool
	}{
		{name: "should find a row by its name", row: "allow_http", wantFound: true},
		{name: "should find no row for an unknown name", row: "allow-http"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row, found := settingrow.Lookup(tc.row)
			if found != tc.wantFound || (found && row.Name != tc.row) {
				t.Errorf("Lookup(%s) = %s, %v, want found %v", tc.row, row.Name, found, tc.wantFound)
			}
		})
	}
}

func TestNoAliasTTL(t *testing.T) {
	t.Parallel()

	t.Run("should equal the cache's own no alias lifetime", func(t *testing.T) {
		t.Parallel()

		if settings.NoAliasTTL != cache.NoAliasTTL {
			t.Errorf("settings.NoAliasTTL = %v, cache.NoAliasTTL = %v, want them equal", settings.NoAliasTTL, cache.NoAliasTTL)
		}
	})
}
