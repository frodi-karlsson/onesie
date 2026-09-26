package examples_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/examples"
	"github.com/frodi-karlsson/onesie/internal/cli"
)

var projectDir = filepath.Join("..", ".onesie")

var projectRequirements = map[string][]string{
	"history-lesson": {
		"history.catches >= 1",
		"history.false_alarms <= 0.15",
		"history.catches >= 0.9 at abstain",
		"history.false_alarms <= 0 at abstain",
	},
}

func TestProjectSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, set examples.Set)
	}{
		{name: "should meet its requirements offline", check: meetsProjectRequirements},
		{name: "should name the model its answers came from", check: namesProjectModel},
	}

	sets, err := examples.Sets(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(sets))
	for _, set := range sets {
		names = append(names, set.Name)
	}

	for name := range projectRequirements {
		if !slices.Contains(names, name) {
			t.Errorf("requirements list %s, which %s does not hold", name, projectDir)
		}
	}

	for _, tc := range tests {
		for _, set := range sets {
			t.Run(tc.name+" for "+set.Name, func(t *testing.T) {
				t.Parallel()

				tc.check(t, set)
			})
		}
	}
}

func meetsProjectRequirements(t *testing.T, set examples.Set) {
	requirements, found := projectRequirements[set.Name]
	if !found {
		t.Fatalf("no requirements for %s", set.Name)
	}

	var args []string
	for _, requirement := range requirements {
		args = append(args, "--require", requirement)
	}

	stdout, stderr, code := offline(t, projectDir, set, args...)
	if code != cli.ExitOK {
		t.Fatalf("exit %d, want 0\nstderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
}

func namesProjectModel(t *testing.T, set examples.Set) {
	for n, line := range strings.Split(strings.TrimSuffix(readFile(t, set.Answers(projectDir)), "\n"), "\n") {
		if !strings.Contains(line, `"model":"`+examples.Model+`"`) {
			t.Errorf("%s line %d does not name the model %s", set.Answers(projectDir), n+1, examples.Model)
		}
	}
}
