package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
)

func TestNewRootCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantErr  bool
		contains []string
	}{
		{
			name:     "should print help when given no arguments",
			args:     nil,
			contains: []string{"Usage:", "jev [command]"},
		},
		{
			name:     "should print the short form for the version flag",
			args:     []string{"--version"},
			contains: []string{"jev 1.2.3"},
		},
		{
			name:     "should print full provenance for the version subcommand",
			args:     []string{"version"},
			contains: []string{"version   1.2.3", "commit    abc1234", "platform "},
		},
		{
			name:    "should reject arguments to the version subcommand",
			args:    []string{"version", "extra"},
			wantErr: true,
		},
		{
			name:    "should fail on an unknown command",
			args:    []string{"nope"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := execute(t, tc.args...)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got none (output: %q)", out)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\ngot:\n%s", want, out)
				}
			}
		})
	}
}

func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var buf bytes.Buffer

	root := cli.NewRootCmd(cli.BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-01-01"})
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)

	err := root.ExecuteContext(t.Context())

	return buf.String(), err
}
