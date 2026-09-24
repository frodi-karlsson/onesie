package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewQuestionsCmd(t *testing.T) {
	t.Parallel()

	home := filepath.Join(string(filepath.Separator)+"home", "x")
	workDir := filepath.Join(string(filepath.Separator)+"repo", "sub")
	configDir := filepath.Join(home, ".config", "onesie")
	repoDir := filepath.Join(string(filepath.Separator)+"repo", ".onesie", "questions")
	configQuestions := filepath.Join(configDir, "questions")

	sets := map[string]string{
		"/repo/.onesie/questions/triage.yaml":            "a: q\n",
		"/home/x/.config/onesie/questions/support.yaml":  "a: q\n",
		"/home/x/.config/onesie/questions/triage.json":   "a: q\n",
		"/home/x/.config/onesie/questions/draft.old.yml": "a: q\n",
	}

	clashed := map[string]string{
		"/repo/.onesie/questions/triage.yaml": "a: q\n",
		"/repo/.onesie/questions/triage.json": "a: q\n",
	}

	upOne := filepath.Join("..", ".onesie", "questions")

	tests := []struct {
		name     string
		args     []string
		files    map[string]string
		noConfig bool
		wantCode int
		want     string
		contains []string
	}{
		{
			name:     "should list each name with the file it resolves to",
			args:     []string{"questions"},
			files:    sets,
			wantCode: ExitOK,
			want: "triage   " + filepath.Join(upOne, "triage.yaml") + "\n" +
				"support  " + filepath.Join("~", ".config", "onesie", "questions", "support.yaml") + "\n",
		},
		{
			name:     "should take -o table as the default",
			args:     []string{"questions", "-o", "table"},
			files:    map[string]string{"/repo/.onesie/questions/triage.yaml": "a: q\n"},
			wantCode: ExitOK,
			want:     "triage  " + filepath.Join(upOne, "triage.yaml") + "\n",
		},
		{
			name:     "should list a name with two files as a clash",
			args:     []string{"questions"},
			files:    clashed,
			wantCode: ExitOK,
			want: "triage  " + filepath.Join(upOne, "triage.yaml") + "  clash\n" +
				"triage  " + filepath.Join(upOne, "triage.json") + "  clash\n",
		},
		{
			name:     "should print json with absolute paths",
			args:     []string{"questions", "-o", "json"},
			files:    sets,
			wantCode: ExitOK,
			want: `[{"name":"triage","path":` + quoted(filepath.Join(repoDir, "triage.yaml")) + `},` +
				`{"name":"support","path":` + quoted(filepath.Join(configQuestions, "support.yaml")) + `}]` + "\n",
		},
		{
			name:     "should mark a clash in json",
			args:     []string{"questions", "-o", "json"},
			files:    clashed,
			wantCode: ExitOK,
			want: `[{"name":"triage","path":` + quoted(filepath.Join(repoDir, "triage.yaml")) + `,"clash":true},` +
				`{"name":"triage","path":` + quoted(filepath.Join(repoDir, "triage.json")) + `,"clash":true}]` + "\n",
		},
		{
			name:     "should print nothing when no name is saved",
			args:     []string{"questions"},
			wantCode: ExitOK,
		},
		{
			name:     "should print an empty array in json when no name is saved",
			args:     []string{"questions", "-o", "json"},
			wantCode: ExitOK,
			want:     "[]\n",
		},
		{
			name:     "should list the repository set when the config dir cannot be resolved",
			args:     []string{"questions"},
			files:    sets,
			noConfig: true,
			wantCode: ExitOK,
			want:     "triage  " + filepath.Join(upOne, "triage.yaml") + "\n",
		},
		{
			name:     "should name what -f searched when the config dir cannot be resolved",
			args:     []string{"-f", "support", "--state", "x", "--print-request"},
			files:    sets,
			noConfig: true,
			wantCode: ExitUsage,
			contains: []string{
				"onesie: no file or question file named support",
				"in " + repoDir + ".",
				"The config dir could not be resolved, so it was not searched",
			},
		},
		{
			name:     "should refuse an unknown output",
			args:     []string{"questions", "-o", "csv"},
			wantCode: ExitUsage,
			contains: []string{"onesie: questions -o takes table, json or auto, got csv"},
		},
		{
			name:     "should refuse an argument",
			args:     []string{"questions", "triage"},
			wantCode: ExitUsage,
			contains: []string{"onesie: questions takes no argument"},
		},
		{
			name:     "should hint that a question flag belongs to the root",
			args:     []string{"questions", "--ask", "a=b"},
			wantCode: ExitUsage,
			contains: []string{"'questions' is a subcommand. To ask it as a question, put it after --"},
		},
		{
			name:     "should still ask questions as a question after --",
			args:     []string{"--state", "x", "--print-request", "--", "questions"},
			wantCode: ExitOK,
			contains: []string{`"instructions":"questions"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			opts := append([]RootOption{
				WithKeychain(noKeychain()),
				WithStdin(strings.NewReader("")),
				WithStdinTTY(false),
				WithStdoutTTY(false),
			}, newFakeTree(tc.files, workDir, configDir).options()...)
			opts = append(opts, WithHomeDir(func() (string, error) { return home, nil }))

			if tc.noConfig {
				opts = append(opts,
					WithLookupEnv(func(string) (string, bool) { return "", false }),
					WithHomeDir(func() (string, error) { return "", errors.New("$HOME is not defined") }))
			}

			root := NewRootCmd(BuildInfo{Version: "1.2.3"}, opts...)
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(tc.args)

			if code := Execute(t.Context(), root); code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, &out, &errOut)
			}

			if tc.contains == nil && out.String() != tc.want {
				t.Errorf("stdout = %q, want %q\nstderr:\n%s", out.String(), tc.want, &errOut)
			}

			for _, want := range tc.contains {
				if !strings.Contains(out.String()+errOut.String(), want) {
					t.Errorf("output missing %q\nstdout:\n%s\nstderr:\n%s", want, &out, &errOut)
				}
			}
		})
	}
}

func quoted(path string) string {
	return `"` + strings.ReplaceAll(path, `\`, `\\`) + `"`
}
