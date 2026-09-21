package plan_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/argv"
	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func TestAssemble(t *testing.T) {
	t.Parallel()

	files := map[string]string{"q.txt": "are these the same person\n"}

	readFile := func(name string) ([]byte, error) {
		body, ok := files[filepath.Base(name)]
		if !ok {
			return nil, os.ErrNotExist
		}

		return []byte(body), nil
	}

	tests := []struct {
		name       string
		events     []argv.Event
		positional string
		wantErr    bool
		check      func(t *testing.T, p *plan.Plan)
	}{
		{
			name:       "should key a positional question as answer",
			positional: "is this urgent",
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Questions) != 1 {
					t.Fatalf("questions = %d, want 1", len(p.Questions))
				}

				if p.Questions[0].ID != "answer" {
					t.Errorf("id = %q, want answer", p.Questions[0].ID)
				}

				if p.Questions[0].Shape != plan.Noul {
					t.Errorf("shape = %s, want noul", p.Questions[0].Shape)
				}

				if p.Questions[0].Named {
					t.Error("a positional question must not be marked Named")
				}
			},
		},
		{
			name: "should bind shape flags to the nearest preceding ask",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "ask", Value: "b=second"},
				{Name: "rate", Value: "p,q"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Questions) != 2 {
					t.Fatalf("questions = %d, want 2", len(p.Questions))
				}

				if p.Questions[0].ID != "a" || p.Questions[0].Shape != plan.Pick {
					t.Errorf("first = %s/%s, want a/pick", p.Questions[0].ID, p.Questions[0].Shape)
				}

				if p.Questions[1].ID != "b" || p.Questions[1].Shape != plan.Rate {
					t.Errorf("second = %s/%s, want b/rate", p.Questions[1].ID, p.Questions[1].Shape)
				}
			},
		},
		{
			name: "should preserve question order as defined",
			events: []argv.Event{
				{Name: "ask", Value: "zebra=first"},
				{Name: "ask", Value: "apple=second"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].ID != "zebra" || p.Questions[1].ID != "apple" {
					t.Errorf("order = %s,%s, want zebra,apple",
						p.Questions[0].ID, p.Questions[1].ID)
				}
			},
		},
		{
			name: "should append repeated pick flags within a group",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "pick", Value: "z"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Questions[0].Options) != 3 {
					t.Fatalf("options = %d, want 3", len(p.Questions[0].Options))
				}

				if p.Questions[0].Options[2].Name != "z" {
					t.Errorf("third option = %q, want z", p.Questions[0].Options[2].Name)
				}
			},
		},
		{
			name: "should preserve rate level order across repeats",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "rate", Value: "low,mid"},
				{Name: "rate", Value: "high"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				want := []string{"low", "mid", "high"}
				for i, level := range p.Questions[0].Levels {
					if level.Label != want[i] {
						t.Errorf("level %d = %q, want %q", i, level.Label, want[i])
					}
				}
			},
		},
		{
			name: "should apply sep to the flags that follow it",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "sep", Value: "|"},
				{Name: "pick", Value: "x|y"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Questions[0].Options) != 2 {
					t.Fatalf("options = %d, want 2", len(p.Questions[0].Options))
				}
			},
		},
		{
			name: "should not apply sep to a pick that preceded it",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x|y"},
				{Name: "sep", Value: "|"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Questions[0].Options) != 1 {
					t.Fatalf("options = %d, want 1", len(p.Questions[0].Options))
				}
			},
		},
		{
			name: "should split ask on the first equals only",
			events: []argv.Event{
				{Name: "ask", Value: "a=what does x=y mean"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Instructions != "what does x=y mean" {
					t.Errorf("instructions = %v", p.Questions[0].Instructions)
				}
			},
		},
		{
			name: "should read ask text from a file reference",
			events: []argv.Event{
				{Name: "ask", Value: "a=@q.txt"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Instructions != "are these the same person" {
					t.Errorf("instructions = %q, want the file body with one newline stripped",
						p.Questions[0].Instructions)
				}
			},
		},
		{
			name: "should treat a doubled at sign as a literal at",
			events: []argv.Event{
				{Name: "ask", Value: "a=@@literal"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Instructions != "@literal" {
					t.Errorf("instructions = %v, want @literal", p.Questions[0].Instructions)
				}
			},
		},
		{
			name: "should attach descriptions to the matching option",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "desc", Value: "x=the ex one"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Options[0].Desc != "the ex one" {
					t.Errorf("desc = %v", p.Questions[0].Options[0].Desc)
				}

				if p.Questions[0].Options[1].Desc != nil {
					t.Errorf("undescribed option should carry a nil desc")
				}
			},
		},
		{
			name: "should map yes and no onto criteria",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "desc", Value: "yes=it is urgent"},
				{Name: "desc", Value: "no=it is calm"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Criteria == nil {
					t.Fatal("criteria should be set")
				}

				if p.Questions[0].Criteria.Yes != "it is urgent" {
					t.Errorf("yes = %v", p.Questions[0].Criteria.Yes)
				}
			},
		},
		{
			name: "should bind top level flags to a lone question",
			events: []argv.Event{
				{Name: "pick", Value: "x,y"},
			},
			positional: "which one",
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Shape != plan.Pick {
					t.Errorf("shape = %s, want pick", p.Questions[0].Shape)
				}

				if len(p.Orphans) != 0 {
					t.Errorf("orphans = %v, want none", p.Orphans)
				}
			},
		},
		{
			name: "should merge top level flags into a single ask",
			events: []argv.Event{
				{Name: "pick", Value: "x,y"},
				{Name: "ask", Value: "only=one question"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Shape != plan.Pick {
					t.Errorf("shape = %s, want pick", p.Questions[0].Shape)
				}

				if len(p.Orphans) != 0 {
					t.Errorf("orphans = %v, want none", p.Orphans)
				}
			},
		},
		{
			name: "should report top level flags as orphans with several asks",
			events: []argv.Event{
				{Name: "pick", Value: "x,y"},
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Questions) != 2 {
					t.Fatalf("questions = %d, want 2", len(p.Questions))
				}

				if len(p.Orphans) != 1 || p.Orphans[0].Name != "pick" {
					t.Errorf("orphans = %v, want one pick event", p.Orphans)
				}
			},
		},
		{
			name: "should record policy flags on the group they follow",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=is this urgent"},
				{Name: "threshold", Value: "0.85"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Policy.Threshold == nil {
					t.Fatal("threshold should be set")
				}

				if *p.Questions[0].Policy.Threshold != 0.85 {
					t.Errorf("threshold = %v, want 0.85", *p.Questions[0].Policy.Threshold)
				}
			},
		},
		{
			name:    "should reject an ask with no equals",
			events:  []argv.Event{{Name: "ask", Value: "noequals"}},
			wantErr: true,
		},
		{
			name:    "should reject an empty ask",
			events:  []argv.Event{{Name: "ask", Value: ""}},
			wantErr: true,
		},
		{
			name:    "should reject a desc with no equals",
			events:  []argv.Event{{Name: "ask", Value: "a=x"}, {Name: "desc", Value: "noequals"}},
			wantErr: true,
		},
		{
			name:    "should report a missing file reference",
			events:  []argv.Event{{Name: "ask", Value: "a=@absent.txt"}},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := plan.Assemble(tc.events, tc.positional, readFile)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.check(t, got)
		})
	}
}

func TestAssembleFileError(t *testing.T) {
	t.Parallel()

	t.Run("should wrap the underlying read error", func(t *testing.T) {
		t.Parallel()

		_, err := plan.Assemble(
			[]argv.Event{{Name: "ask", Value: "a=@gone.txt"}},
			"",
			func(string) ([]byte, error) { return nil, os.ErrNotExist },
		)

		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("error = %v, want it to wrap os.ErrNotExist", err)
		}
	})
}
