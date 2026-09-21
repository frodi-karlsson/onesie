package plan_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
			name:       "should resolve a yes/no fallback to true with no validation",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "fallback", Value: "true"}},
			check:      wantFallbackBoolean(true),
		},
		{
			name:       "should resolve an uppercase yes fallback with no validation",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "fallback", Value: "YES"}},
			check:      wantFallbackBoolean(true),
		},
		{
			name:       "should resolve a yes/no fallback to false with no validation",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "fallback", Value: "false"}},
			check:      wantFallbackBoolean(false),
		},
		{
			name:       "should resolve a fallback given before the shape is known",
			positional: "how frustrated is the customer",
			events: []argv.Event{
				{Name: "fallback", Value: "calm"},
				{Name: "rate", Value: "calm,annoyed"},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				fallback := p.Questions[0].Policy.Fallback
				if fallback == nil {
					t.Fatal("the question must carry its fallback")
				}

				if fallback.Boolean {
					t.Error("a rate fallback must not be parsed as a boolean")
				}

				if fallback.Text != "calm" {
					t.Errorf("text = %q, want calm", fallback.Text)
				}
			},
		},
		{
			name:       "should leave an unparseable yes/no fallback for validation to report",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "fallback", Value: "maybe"}},
			check:      wantFallbackBoolean(false),
		},
		{
			name:    "should reject an empty ask",
			events:  []argv.Event{{Name: "ask", Value: ""}},
			wantErr: true,
		},
		{
			name:    "should reject an ask with an empty id",
			events:  []argv.Event{{Name: "ask", Value: "=is this urgent"}},
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

			got, err := plan.Assemble(plan.Source{
				Events:     tc.events,
				Positional: tc.positional,
				ReadFile:   readFile,
			})

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

		_, err := plan.Assemble(plan.Source{
			Events:   []argv.Event{{Name: "ask", Value: "a=@gone.txt"}},
			ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		})

		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("error = %v, want it to wrap os.ErrNotExist", err)
		}
	})
}

func TestAssembleWithFile(t *testing.T) {
	t.Parallel()

	readFile := func(string) ([]byte, error) { return nil, nil }

	fileQuestions := func() []plan.Question {
		return []plan.Question{
			{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Named: true},
			{ID: "team", Shape: plan.Noul, Instructions: "which team", Named: true},
		}
	}

	tests := []struct {
		name    string
		src     plan.Source
		wantErr string
		check   func(t *testing.T, p *plan.Plan)
	}{
		{
			name: "should keep file questions in file order",
			src:  plan.Source{File: fileQuestions(), ReadFile: readFile},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Questions) != 2 || p.Questions[0].ID != "urgent" {
					t.Errorf("questions = %+v", p.Questions)
				}
			},
		},
		{
			name: "should append ask questions after file questions",
			src: plan.Source{
				File:     fileQuestions(),
				Events:   []argv.Event{{Name: "ask", Value: "extra=one more"}},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				want := []string{"urgent", "team", "extra"}
				for i, q := range p.Questions {
					if q.ID != want[i] {
						t.Errorf("question %d = %s, want %s", i, q.ID, want[i])
					}
				}
			},
		},
		{
			name: "should reject an id defined in both sources",
			src: plan.Source{
				File:     fileQuestions(),
				FileName: "triage.yaml",
				Events:   []argv.Event{{Name: "ask", Value: "team=override"}},
				ReadFile: readFile,
			},
			wantErr: "'team' is defined in triage.yaml and by --ask. Pass --replace to override",
		},
		{
			name: "should let replace win while keeping the file position",
			src: plan.Source{
				File:     fileQuestions(),
				Events:   []argv.Event{{Name: "ask", Value: "urgent=override"}},
				Replace:  true,
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].ID != "urgent" {
					t.Fatalf("the replaced question must keep its file position, got %s",
						p.Questions[0].ID)
				}

				if p.Questions[0].Instructions != "override" {
					t.Errorf("instructions = %v, want the --ask text to win",
						p.Questions[0].Instructions)
				}

				if len(p.Questions) != 2 {
					t.Errorf("replace must not add a question, got %d", len(p.Questions))
				}
			},
		},
		{
			name: "should bind top level flags to a lone file question",
			src: plan.Source{
				File: []plan.Question{
					{ID: "team", Shape: plan.Noul, Instructions: "which team", Named: true},
				},
				Events:   []argv.Event{{Name: "pick", Value: "billing,technical"}},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Shape != plan.Pick {
					t.Errorf("shape = %s, want pick", p.Questions[0].Shape)
				}

				if len(p.Questions[0].Options) != 2 {
					t.Errorf("options = %+v, want two", p.Questions[0].Options)
				}

				if len(p.Orphans) != 0 {
					t.Errorf("orphans = %v, want none", p.Orphans)
				}
			},
		},
		{
			name: "should orphan top level flags when the file has several questions",
			src: plan.Source{
				File:     fileQuestions(),
				Events:   []argv.Event{{Name: "pick", Value: "a,b"}},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Orphans) != 1 {
					t.Errorf("orphans = %v, want one", p.Orphans)
				}
			},
		},
		{
			name: "should bind a policy flag to a lone file question",
			src: plan.Source{
				File: []plan.Question{
					{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Named: true},
				},
				Events:   []argv.Event{{Name: "threshold", Value: "0.9"}},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Policy.Threshold == nil {
					t.Fatal("a top level policy flag must bind to a lone file question")
				}

				if *p.Questions[0].Policy.Threshold != 0.9 {
					t.Errorf("threshold = %v, want 0.9", *p.Questions[0].Policy.Threshold)
				}
			},
		},
		{
			name: "should orphan a leading flag when a file question joins a lone ask",
			src: plan.Source{
				File: []plan.Question{
					{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Named: true},
				},
				Events: []argv.Event{
					{Name: "threshold", Value: "0.8"},
					{Name: "ask", Value: "other=second"},
				},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Orphans) != 1 || p.Orphans[0].Name != "threshold" {
					t.Fatalf("orphans = %v, want the leading --threshold", p.Orphans)
				}

				for _, question := range p.Questions {
					if question.Policy.Threshold != nil {
						t.Errorf("'%s' must not absorb a top level flag when two questions "+
							"were asked", question.ID)
					}
				}
			},
		},
		{
			name: "should still absorb a leading flag into a lone ask with no file",
			src: plan.Source{
				Events: []argv.Event{
					{Name: "threshold", Value: "0.8"},
					{Name: "ask", Value: "other=second"},
				},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if len(p.Orphans) != 0 {
					t.Fatalf("orphans = %v, want none", p.Orphans)
				}

				if p.Questions[0].Policy.Threshold == nil {
					t.Fatal("a lone --ask must still absorb the leading flags")
				}
			},
		},
		{
			name: "should merge a top level desc into the file's yes/no criteria",
			src: plan.Source{
				File: []plan.Question{
					{
						ID: "urgent", Shape: plan.Noul, Instructions: "q", Named: true,
						Criteria: &plan.YesNoCriteria{Yes: "FILE YES", No: "FILE NO"},
					},
				},
				Events:   []argv.Event{{Name: "desc", Value: "yes=CLI YES"}},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				criteria := p.Questions[0].Criteria
				if criteria == nil {
					t.Fatal("criteria = nil, want the file's criteria refined")
				}

				if criteria.Yes != "CLI YES" {
					t.Errorf("yes = %v, want the flag to win", criteria.Yes)
				}

				if criteria.No != "FILE NO" {
					t.Errorf("no = %v, want the file's half kept", criteria.No)
				}
			},
		},
		{
			name: "should keep the file's yes half when only no is named",
			src: plan.Source{
				File: []plan.Question{
					{
						ID: "urgent", Shape: plan.Noul, Instructions: "q", Named: true,
						Criteria: &plan.YesNoCriteria{Yes: "FILE YES", No: "FILE NO"},
					},
				},
				Events:   []argv.Event{{Name: "desc", Value: "no=CLI NO"}},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				criteria := p.Questions[0].Criteria
				if criteria.Yes != "FILE YES" || criteria.No != "CLI NO" {
					t.Errorf("criteria = %+v, want FILE YES and CLI NO", criteria)
				}
			},
		},
		{
			name: "should still build criteria for a question that had none",
			src: plan.Source{
				File: []plan.Question{
					{ID: "urgent", Shape: plan.Noul, Instructions: "q", Named: true},
				},
				Events:   []argv.Event{{Name: "desc", Value: "yes=CLI YES"}},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				criteria := p.Questions[0].Criteria
				if criteria == nil || criteria.Yes != "CLI YES" || criteria.No != nil {
					t.Errorf("criteria = %+v, want only the yes half set", criteria)
				}
			},
		},
		{
			name: "should reject a shape flag aimed at a body question",
			src: plan.Source{
				File: []plan.Question{
					{ID: "bq", Shape: plan.Noul, Instructions: "q", Named: true, FromBody: true},
				},
				Events:   []argv.Event{{Name: "pick", Value: "x,y"}},
				ReadFile: readFile,
			},
			wantErr: "jev: --pick cannot reshape a request body's question. " +
				"A body carries its own type and criteria",
		},
		{
			name: "should reject a desc aimed at a body question",
			src: plan.Source{
				File: []plan.Question{
					{ID: "bq", Shape: plan.Noul, Instructions: "q", Named: true, FromBody: true},
				},
				Events:   []argv.Event{{Name: "desc", Value: "yes=nope"}},
				ReadFile: readFile,
			},
			wantErr: "jev: --desc cannot reshape a request body's question",
		},
		{
			name: "should bind a policy flag to a lone body question",
			src: plan.Source{
				File: []plan.Question{
					{ID: "bq", Shape: plan.Noul, Instructions: "q", Named: true, FromBody: true},
				},
				Events:   []argv.Event{{Name: "threshold", Value: "0.8"}},
				ReadFile: readFile,
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Policy.Threshold == nil {
					t.Fatal("policy is the one thing a body question accepts from a flag")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := plan.Assemble(tc.src)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantErr)
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

func wantFallbackBoolean(want bool) func(t *testing.T, p *plan.Plan) {
	return func(t *testing.T, p *plan.Plan) {
		t.Helper()

		fallback := p.Questions[0].Policy.Fallback
		if fallback == nil {
			t.Fatal("the question must carry its fallback")
		}

		if fallback.Boolean != want {
			t.Errorf("boolean = %t, want %t", fallback.Boolean, want)
		}
	}
}
