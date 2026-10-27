package cli

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
)

func TestBuild(t *testing.T) {
	t.Parallel()

	const (
		gated      = "assert: urgent.value > 0.5\nabstain_if: urgent.value > 0.4\nurgent:\n  ask: is it urgent\n"
		abstaining = "abstain_if: urgent.value > 0.4\nurgent:\n  ask: is it urgent\n"
	)

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
	}{
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
			name:         "should refuse -q over several questions once the file's gate is set aside",
			file:         gated,
			quiet:        true,
			events:       twoAsks,
			ignoreJudged: true,
			wantErr:      "-q needs a single question",
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

			settings := rootSettings{readFile: func(string) ([]byte, error) { return []byte(tc.file), nil }}
			flags := runConfig{
				runFlags: runFlags{file: "gated.yaml", input: "text", jobs: 1, quiet: tc.quiet},
				events:   tc.events,
			}

			inv, _, err := build(settings, flags, tc.ignoreJudged)
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

			if inv.fileGate != tc.wantSetAside {
				t.Errorf("set aside %+v, want %+v", inv.fileGate, tc.wantSetAside)
			}
		})
	}
}
