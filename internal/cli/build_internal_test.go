package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestBuild(t *testing.T) {
	t.Parallel()

	const (
		gated      = "assert: urgent.value > 0.5\nabstain_if: urgent.value > 0.4\nurgent:\n  ask: is it urgent\n"
		abstaining = "abstain_if: urgent.value > 0.4\nurgent:\n  ask: is it urgent\n"
	)

	longBody := `{"long_context":true,"questions":{"urgent":{"type":"noul","instructions":"is it urgent"}}}`

	twoAsks := []argv.Event{{Name: "ask", Value: "a=x"}, {Name: "ask", Value: "b=y"}}

	tests := []struct {
		name         string
		file         string
		quiet        bool
		events       []argv.Event
		ignoreJudged bool
		wantErr      string
		wantGate     bool
		wantSetAside fileGate
		args         []string
		wantLong     bool
	}{
		{
			name:    "should reject a whitespace only file assertion",
			file:    "assert: '   '\nurgent:\n  ask: is it urgent\n",
			wantErr: "'assert': the expression is empty",
		},
		{
			name:    "should reject a whitespace only file abstain expression",
			file:    "assert: urgent.value > 0.5\nabstain_if: '   '\nurgent:\n  ask: is it urgent\n",
			wantErr: "'abstain_if': the expression is empty",
		},
		{
			name: "should send no long context when --long-context=false meets a body with true",
			file: longBody,
			args: []string{"--long-context=false"},
		},
		{
			name:     "should keep a body's true with no flag",
			file:     longBody,
			wantLong: true,
		},
		{
			name:     "should let the file's assert gate -q over several questions",
			file:     gated,
			quiet:    true,
			events:   twoAsks,
			wantGate: true,
		},
		{
			name:    "should name the file's abstain_if when nothing asserts",
			file:    abstaining,
			wantErr: "'abstain_if' needs --assert",
		},
		{
			name:         "should set the file's gate aside and build no gate when judgment is ignored",
			file:         gated,
			ignoreJudged: true,
			wantSetAside: fileGate{assert: "urgent.value > 0.5", abstainIf: "urgent.value > 0.4"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parsed := &runFlags{}
			cmd := &cobra.Command{Use: "onesie"}
			parsed.given = setting.Register(cmd.Flags(), cmd.PersistentFlags(), setting.Table(), argv.New(),
				setting.CommandRoot, runTargets(parsed))
			cmd.Flags().AddFlagSet(cmd.PersistentFlags())

			args := append([]string{"-f", "gated.yaml"}, tc.args...)
			if tc.quiet {
				args = append(args, "-q")
			}

			if err := cmd.Flags().Parse(args); err != nil {
				t.Fatal(err)
			}

			settings := defaultSettings(BuildInfo{})
			settings.lookupEnv = lookupFrom(nil)
			settings.readFile = func(string) ([]byte, error) { return []byte(tc.file), nil }
			settings.holder = newHolder(settings, parsed, func() io.Writer { return io.Discard })
			flags := runConfig{runFlags: *parsed, events: tc.events}

			command := setting.CommandRoot
			if tc.ignoreJudged {
				command = setting.CommandCalibrate
			}

			inv, _, err := build(settings, flags, parsed.given, command, tc.ignoreJudged)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("build error = %v, want one naming %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if (inv.gate != nil) != tc.wantGate || (inv.abstain != nil) != tc.wantGate {
				t.Errorf("gate %v, abstain %v, want both built %v", inv.gate, inv.abstain, tc.wantGate)
			}

			if inv.plan.LongContext != tc.wantLong {
				t.Errorf("long context = %v, want %v", inv.plan.LongContext, tc.wantLong)
			}

			if inv.fileGate != tc.wantSetAside {
				t.Errorf("set aside %+v, want %+v", inv.fileGate, tc.wantSetAside)
			}
		})
	}
}
