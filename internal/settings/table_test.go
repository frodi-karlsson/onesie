package settings_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestTable(t *testing.T) {
	t.Parallel()

	commands := []settings.Command{
		settings.CommandRoot, settings.CommandCalibrate, settings.CommandProfileCreate, settings.CommandProfileUpdate,
	}

	tests := []struct {
		name  string
		check func(rows []settings.Row) []string
	}{
		{
			name: "should give each command unique long names and shorthands",
			check: func(rows []settings.Row) []string {
				var problems []string

				for _, command := range commands {
					longs, shorts := map[string]settings.Name{}, map[string]settings.Name{}

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
			check: func(rows []settings.Row) []string {
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
			check: func(rows []settings.Row) []string {
				var problems []string

				for _, row := range rows {
					switch {
					case row.Scope == settings.ScopeQuestion && len(row.Shapes) == 0:
						problems = append(problems, fmt.Sprintf("%s is per question and names no shape", row.Name))
					case row.Scope == settings.ScopeRun && len(row.Shapes) > 0:
						problems = append(problems, fmt.Sprintf("%s is per run and names shapes", row.Name))
					}
				}

				return problems
			},
		},
		{
			name: "should give every key a row whose kind yields its type",
			check: func(rows []settings.Row) []string {
				var problems []string

				seen := map[settings.Name]bool{}

				for _, key := range settings.Keys() {
					if seen[key.Name()] {
						problems = append(problems, fmt.Sprintf("Keys lists %s twice", key.Name()))
					}

					seen[key.Name()] = true

					row, found := settings.Lookup(key.Name())

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
			check: func(rows []settings.Row) []string {
				var problems []string

				names, orders := map[string]settings.Name{}, map[int]settings.Row{}

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
			check: func(rows []settings.Row) []string {
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
			check: func(rows []settings.Row) []string {
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
			name: "should give every provider's key variable to the api_key row",
			check: func(rows []settings.Row) []string {
				var problems []string

				row, found := settings.Lookup("api_key")
				if !found || len(row.Env) == 0 {
					return []string{"no api_key row with variables"}
				}

				for _, provider := range onesie.Providers() {
					if !slices.Contains(row.Env, settings.EnvSpec{
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
			check: func(rows []settings.Row) []string {
				var problems []string

				for _, row := range rows {
					if row.InBody && (row.FileKey == "" || row.Scope != settings.ScopeRun) {
						problems = append(problems, fmt.Sprintf("%s is a body key with no file key or on a question", row.Name))
					}
				}

				return problems
			},
		},
		{
			name: "should give every default the type of its kind",
			check: func(rows []settings.Row) []string {
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, problem := range tc.check(settings.Table()) {
				t.Error(problem)
			}
		})
	}
}

func sharesHelp(row settings.Row, env settings.EnvSpec) bool {
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
		row       settings.Name
		wantFound bool
	}{
		{name: "should find a row by its name", row: "allow_http", wantFound: true},
		{name: "should find no row for an unknown name", row: "allow-http"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row, found := settings.Lookup(tc.row)
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
