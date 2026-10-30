package planbuild_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/planbuild"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func TestFromFile(t *testing.T) {
	t.Parallel()

	half := 0.5

	tests := []struct {
		name         string
		doc          string
		file         *qfile.File
		wantModel    string
		wantLong     bool
		wantGate     string
		wantAbstain  string
		wantWarnings []string
		wantErr      string
		wantIs       error
	}{
		{
			name:      "should build a file's questions with its model and long context",
			doc:       `{"model":"m-1","long_context":true,"questions":{"urgent":{"type":"noul","instructions":"is it urgent"}}}`,
			wantModel: "m-1",
			wantLong:  true,
		},
		{
			name: "should compile the file's assert and abstain_if",
			doc: "assert: urgent.value < 0.5\nabstain_if: urgent.value > 0.4\n" +
				"urgent:\n  ask: is it urgent\n",
			wantGate:    "urgent.value < 0.5",
			wantAbstain: "urgent.value > 0.4",
		},
		{
			name: "should return the validation warnings beside a refusal",
			doc: "assert: team.value ==\nteam:\n  ask: which team\n  pick:\n    billing: money\n" +
				"    sales:\n",
			wantWarnings: []string{"'team' describes billing but not sales"},
			wantErr:      "'assert': ",
		},
		{
			name:    "should name a bad gate by its file key",
			doc:     "assert: nobody.value < 0.5\nurgent:\n  ask: is it urgent\n",
			wantErr: "'assert': ",
		},
		{
			name:   "should return ErrNoQuestion for a file with no question",
			doc:    "# nothing asked yet\n",
			wantIs: planbuild.ErrNoQuestion,
		},
		{
			name: "should name the file in a body policy refusal",
			file: &qfile.File{IsBody: true, Questions: []plan.Question{
				{ID: "a", Shape: plan.Noul, Instructions: "x", Origin: plan.OriginBody, Policy: plan.Policy{Threshold: &half}},
				{ID: "b", Shape: plan.Noul, Instructions: "y", Origin: plan.OriginBody},
			}},
			wantErr: "policy flags apply to a request body only when it has one question. " +
				"sets/triage.json has 2, freeze with --print-questions first",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			file := tc.file
			if file == nil {
				loaded, err := qfile.Load([]byte(tc.doc), plan.Keys)
				if err != nil {
					t.Fatalf("Load: %v", err)
				}

				file = loaded
			}

			built, warnings, err := planbuild.FromFile(file, "sets/triage.json", plan.Arguments)

			if strings.Join(warnings, "\n") != strings.Join(tc.wantWarnings, "\n") {
				t.Errorf("warnings = %q, want %q", warnings, tc.wantWarnings)
			}

			if tc.wantErr != "" || tc.wantIs != nil {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) || (tc.wantIs != nil && !errors.Is(err, tc.wantIs)) {
					t.Fatalf("FromFile error = %v, want one starting %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("FromFile: %v", err)
			}

			if built.Plan.Model != tc.wantModel || built.Plan.LongContext != tc.wantLong {
				t.Errorf("model %q, long context %v, want %q and %v",
					built.Plan.Model, built.Plan.LongContext, tc.wantModel, tc.wantLong)
			}

			if built.Gate.Source() != tc.wantGate || built.Abstain.Source() != tc.wantAbstain {
				t.Errorf("gate %q, abstain %q, want %q and %q",
					built.Gate.Source(), built.Abstain.Source(), tc.wantGate, tc.wantAbstain)
			}
		})
	}
}

func TestCompile(t *testing.T) {
	t.Parallel()

	built := &plan.Plan{Questions: []plan.Question{
		{ID: "urgent", Shape: plan.Noul},
		{ID: "needs-review", Shape: plan.Noul},
	}}

	tests := []struct {
		name       string
		gates      planbuild.Gates
		speller    plan.Speller
		wantGate   string
		wantErr    string
		wantMapper bool
		wantNamer  bool
	}{
		{
			name:     "should combine the file's gate before the caller's",
			gates:    planbuild.Gates{FileAssert: "urgent.value < 0.5", Assert: []string{"urgent.value > 0.1"}},
			wantGate: "(urgent.value < 0.5) and (urgent.value > 0.1)",
		},
		{
			name:    "should name the caller's gate as a tool argument for that speller",
			gates:   planbuild.Gates{AbstainIf: []string{"missing.value > 0.5"}},
			speller: plan.Arguments,
			wantErr: "abstain_if: ",
		},
		{
			name:  "should add the bracket hint to a gate that writes a dashed id bare",
			gates: planbuild.Gates{Assert: []string{"needs-review.value < 0.5"}},
			wantErr: "--assert: unexpected character '-'. " +
				`Write the id 'needs-review' in brackets, as in ["needs-review"].value`,
		},
		{
			name:  "should leave the mapper and namer nil when neither is given",
			gates: planbuild.Gates{Map: ".text", ID: ".id"},
		},
		{
			name:       "should compile the mapper and namer when given",
			gates:      planbuild.Gates{Map: ".text", ID: ".id", HasMap: true, HasID: true},
			wantMapper: true,
			wantNamer:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compiled, err := planbuild.Compile(built, tc.gates, tc.speller)
			if tc.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Fatalf("Compile error = %v, want one starting %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Compile: %v", err)
			}

			if compiled.Gate.Source() != tc.wantGate {
				t.Errorf("gate = %q, want %q", compiled.Gate.Source(), tc.wantGate)
			}

			if (compiled.Mapper != nil) != tc.wantMapper || (compiled.Namer != nil) != tc.wantNamer {
				t.Errorf("mapper %v, namer %v, want %v and %v", compiled.Mapper, compiled.Namer, tc.wantMapper, tc.wantNamer)
			}
		})
	}
}

func TestExpr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "should prefix a compile error with the flag", want: "map: "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := planbuild.Expr("map", ".text |"); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("Expr error = %v, want one starting %q", err, tc.want)
			}
		})
	}
}

func TestPlan(t *testing.T) {
	t.Parallel()

	file := &qfile.File{Model: "file-model"}
	asked := plan.Source{Events: []argv.Event{{Name: "ask", Value: "urgent=is it urgent"}}}

	tests := []struct {
		name      string
		src       planbuild.Source
		file      *qfile.File
		wantModel string
		wantLong  bool
	}{
		{
			name:      "should prefer the run's model over the file's",
			src:       planbuild.Source{Source: asked, Model: "run-model"},
			file:      file,
			wantModel: "run-model",
		},
		{name: "should fall back to the file's model", src: planbuild.Source{Source: asked}, file: file, wantModel: "file-model"},
		{
			name:     "should turn long context on from the run",
			src:      planbuild.Source{Source: asked, LongContext: true},
			wantLong: true,
		},
		{
			name:     "should turn long context on from the file",
			src:      planbuild.Source{Source: asked},
			file:     &qfile.File{LongContext: true},
			wantLong: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			built, err := planbuild.Plan(tc.src, tc.file)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}

			if built.Model != tc.wantModel || built.LongContext != tc.wantLong {
				t.Errorf("model %q, long context %v, want %q and %v", built.Model, built.LongContext, tc.wantModel, tc.wantLong)
			}
		})
	}
}
