package settings_test

import (
	"fmt"
	"testing"

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

				for _, match := range everyKey() {
					if problem := match(rows); problem != "" {
						problems = append(problems, problem)
					}
				}

				return problems
			},
		},
		{
			name: "should keep env names unique per provider and their order unique",
			check: func(rows []settings.Row) []string {
				var problems []string

				names, orders := map[string]settings.Name{}, map[int]string{}

				for _, row := range rows {
					for _, env := range row.Env {
						spelled := env.Name + " on " + env.Provider
						if other, taken := names[spelled]; taken {
							problems = append(problems, fmt.Sprintf("%s is both %s and %s", spelled, other, row.Name))
						}

						names[spelled] = row.Name

						if other, taken := orders[env.Order]; taken {
							problems = append(problems, fmt.Sprintf("order %d is both %s and %s", env.Order, other, env.Name))
						}

						orders[env.Order] = env.Name

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

func everyKey() []func([]settings.Row) string {
	return []func([]settings.Row) string{
		matches(settings.Version), matches(settings.Output), matches(settings.Raw), matches(settings.Quiet),
		matches(settings.Input), matches(settings.State), matches(settings.StateFile), matches(settings.Provider),
		matches(settings.Profile), matches(settings.PrintQuestions), matches(settings.PrintSchema),
		matches(settings.ListModels), matches(settings.Unordered), matches(settings.StopOnError),
		matches(settings.StopOnAssert), matches(settings.NoDedup), matches(settings.MergeInput),
		matches(settings.MergeKey), matches(settings.Map), matches(settings.ID), matches(settings.Model),
		matches(settings.APIEndpoint), matches(settings.AllowHTTP), matches(settings.LongContext),
		matches(settings.File), matches(settings.Replace), matches(settings.Jobs), matches(settings.Timeout),
		matches(settings.Retries), matches(settings.MaxRetryAfter), matches(settings.Out), matches(settings.Resume),
		matches(settings.Prune), matches(settings.SkipBlank), matches(settings.Delimiter), matches(settings.Stats),
		matches(settings.Usage), matches(settings.PrintRequest), matches(settings.Cache), matches(settings.Mock),
		matches(settings.Cuts), matches(settings.Offline), matches(settings.CacheTTL), matches(settings.ConfigDir),
		matches(settings.CacheDir), matches(settings.Sep), matches(settings.Box), matches(settings.Threshold),
		matches(settings.MinConfidence), matches(settings.FallbackAnswer),
	}
}

func matches[T any](key settings.Key[T]) func([]settings.Row) string {
	return func(rows []settings.Row) string {
		for _, row := range rows {
			if row.Name != key.Name() {
				continue
			}

			if _, ok := row.Kind.Zero().(T); !ok {
				var want T

				return fmt.Sprintf("key %s is a %T, and its row's kind yields a %T", key.Name(), want, row.Kind.Zero())
			}

			return ""
		}

		return fmt.Sprintf("key %s has no row", key.Name())
	}
}
