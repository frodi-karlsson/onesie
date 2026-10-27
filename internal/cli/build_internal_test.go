package cli

import (
	"testing"
)

func TestBuild(t *testing.T) {
	t.Parallel()

	const gated = "assert: urgent.value > 0.5\nabstain_if: urgent.value > 0.4\nurgent:\n  ask: is it urgent\n"

	tests := []struct {
		name         string
		ignoreJudged bool
		want         runFacts
		wantSetAside fileGate
	}{
		{
			name: "should carry the file's assert and abstain_if as facts",
			want: runFacts{assert: "urgent.value > 0.5", abstainIf: "urgent.value > 0.4"},
		},
		{
			name:         "should set the file's gate aside and carry no facts when judgment is ignored",
			ignoreJudged: true,
			wantSetAside: fileGate{assert: "urgent.value > 0.5", abstainIf: "urgent.value > 0.4"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			settings := rootSettings{readFile: func(string) ([]byte, error) { return []byte(gated), nil }}
			flags := runConfig{runFlags: runFlags{file: "gated.yaml", input: "text", jobs: 1}}

			inv, _, err := build(settings, flags, tc.ignoreJudged)
			if err != nil {
				t.Fatal(err)
			}

			if inv.facts != tc.want || inv.fileGate != tc.wantSetAside {
				t.Errorf("facts = %+v, set aside %+v, want %+v and %+v", inv.facts, inv.fileGate, tc.want, tc.wantSetAside)
			}
		})
	}
}
