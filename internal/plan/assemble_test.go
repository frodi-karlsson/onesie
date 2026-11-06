package plan_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settings"
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
			name:   "should make a box question from --box",
			events: []argv.Event{{Name: "ask", Value: "mug=the mug in `photo`"}, {Name: "box", Value: "true"}},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				if p.Questions[0].Shape != plan.Box || !p.Questions[0].Boxed {
					t.Errorf("question = %+v, want a box", p.Questions[0])
				}
			},
		},
		{
			name:    "should refuse a value given to --box",
			events:  []argv.Event{{Name: "ask", Value: "mug=the mug in `photo`"}, {Name: "box", Value: "false"}},
			wantErr: true,
		},
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

				if p.Questions[0].Origin != plan.OriginPositional {
					t.Error("a positional question must carry the positional origin")
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
			name: "should trim the spaces around each option and level",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "yes, no ,\tmaybe"},
				{Name: "ask", Value: "b=second"},
				{Name: "sep", Value: "|"},
				{Name: "rate", Value: " low | high "},
			},
			check: func(t *testing.T, p *plan.Plan) {
				t.Helper()

				for i, want := range []string{"yes", "no", "maybe"} {
					if got := p.Questions[0].Options[i].Name; got != want {
						t.Errorf("option %d = %q, want %q", i, got, want)
					}
				}

				for i, want := range []string{"low", "high"} {
					if got := p.Questions[1].Levels[i].Label; got != want {
						t.Errorf("level %d = %q, want %q", i, got, want)
					}
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
				Numbers:    settings.PolicyNumbers(),
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

	t.Run("should refuse a zero Numbers, which the caller has to fill", func(t *testing.T) {
		t.Parallel()

		_, err := plan.Assemble(plan.Source{Positional: "is this urgent", ReadFile: readFile})
		if !errors.Is(err, plan.ErrNoNumbers) {
			t.Errorf("Assemble error = %v, want ErrNoNumbers", err)
		}
	})

	t.Run("should wrap a file read error", func(t *testing.T) {
		t.Parallel()

		t.Run("should wrap the underlying read error", func(t *testing.T) {
			t.Parallel()

			_, err := plan.Assemble(plan.Source{
				Events:   []argv.Event{{Name: "ask", Value: "a=@gone.txt"}},
				ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
				Numbers:  settings.PolicyNumbers(),
			})

			if !errors.Is(err, os.ErrNotExist) {
				t.Errorf("error = %v, want it to wrap os.ErrNotExist", err)
			}
		})
	})

	t.Run("should merge a question file with the command line", func(t *testing.T) {
		t.Parallel()

		readFile := func(string) ([]byte, error) { return nil, nil }

		fileQuestions := func() []plan.Question {
			return []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Origin: plan.OriginFile},
				{ID: "team", Shape: plan.Noul, Instructions: "which team", Origin: plan.OriginFile},
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
				name: "should record a --for group as a selection",
				src: plan.Source{
					File: fileQuestions(),
					Events: []argv.Event{
						{Name: "for", Value: "urgent"}, {Name: "threshold", Value: "0.9"},
					},
					ReadFile: readFile,
				},
				check: func(t *testing.T, p *plan.Plan) {
					t.Helper()

					want := []plan.Selection{{ID: "urgent", Events: []argv.Event{{Name: "threshold", Value: "0.9"}}}}
					if !reflect.DeepEqual(p.Selections, want) {
						t.Errorf("selections = %+v, want %+v", p.Selections, want)
					}

					if len(p.Orphans) != 0 || len(p.Questions) != 2 {
						t.Errorf("orphans = %v, questions = %d, want none and the file's two", p.Orphans, len(p.Questions))
					}
				},
			},
			{
				name: "should continue a repeated --for ID",
				src: plan.Source{
					File: fileQuestions(),
					Events: []argv.Event{
						{Name: "for", Value: "team"},
						{Name: "fallback", Value: "human"},
						{Name: "for", Value: "urgent"},
						{Name: "threshold", Value: "0.6"},
						{Name: "for", Value: "team"},
						{Name: "desc", Value: "yes=a team"},
					},
					ReadFile: readFile,
				},
				check: func(t *testing.T, p *plan.Plan) {
					t.Helper()

					want := []plan.Selection{
						{ID: "team", Events: []argv.Event{
							{Name: "fallback", Value: "human"}, {Name: "desc", Value: "yes=a team"},
						}},
						{ID: "urgent", Events: []argv.Event{{Name: "threshold", Value: "0.6"}}},
					}
					if !reflect.DeepEqual(p.Selections, want) {
						t.Errorf("selections = %+v, want %+v", p.Selections, want)
					}
				},
			},
			{
				name: "should keep refusing --ask without =TEXT",
				src: plan.Source{
					File:     fileQuestions(),
					Events:   []argv.Event{{Name: "for", Value: "urgent"}, {Name: "ask", Value: "urgent"}},
					ReadFile: readFile,
				},
				wantErr: "--ask takes NAME=QUESTION, got 'urgent'",
			},
			{
				name: "should keep top level flags as orphans beside a lone file question, for the merge",
				src: plan.Source{
					File: []plan.Question{
						{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Origin: plan.OriginFile},
					},
					Events:   []argv.Event{{Name: "threshold", Value: "0.9"}, {Name: "desc", Value: "yes=@q.txt"}},
					ReadFile: func(string) ([]byte, error) { return []byte("very urgent\n"), nil },
				},
				check: func(t *testing.T, p *plan.Plan) {
					t.Helper()

					want := []argv.Event{{Name: "threshold", Value: "0.9"}, {Name: "desc", Value: "yes=very urgent"}}
					if !reflect.DeepEqual(p.Orphans, want) {
						t.Errorf("orphans = %v, want %v with the reference read", p.Orphans, want)
					}

					if p.Questions[0].Policy.Threshold != nil || p.Questions[0].Criteria != nil {
						t.Errorf("question = %+v, want it as the file gave it", p.Questions[0])
					}
				},
			},
			{
				name: "should refuse a --for group's threshold that is no number",
				src: plan.Source{
					File:     fileQuestions(),
					Events:   []argv.Event{{Name: "for", Value: "urgent"}, {Name: "threshold", Value: "high"}},
					ReadFile: readFile,
				},
				wantErr: "--threshold takes a number, got 'high'",
			},
			{
				name: "should leave a --for group naming no file question to validation",
				src: plan.Source{
					File:     fileQuestions(),
					Events:   []argv.Event{{Name: "for", Value: "nope"}, {Name: "threshold", Value: "high"}},
					ReadFile: readFile,
				},
				check: func(t *testing.T, p *plan.Plan) {
					t.Helper()

					want := []plan.Selection{{ID: "nope", Events: []argv.Event{{Name: "threshold", Value: "high"}}}}
					if !reflect.DeepEqual(p.Selections, want) {
						t.Errorf("selections = %+v, want %+v", p.Selections, want)
					}
				},
			},
			{
				name: "should refuse a --for group's desc with no equals",
				src: plan.Source{
					File:     fileQuestions(),
					Events:   []argv.Event{{Name: "for", Value: "urgent"}, {Name: "desc", Value: "yes"}},
					ReadFile: readFile,
				},
				wantErr: "--desc takes KEY=TEXT, got 'yes'",
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
				name: "should orphan a leading flag when a file question joins a lone ask",
				src: plan.Source{
					File: []plan.Question{
						{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Origin: plan.OriginFile},
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
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				src := tc.src
				src.Numbers = settings.PolicyNumbers()

				got, err := plan.Assemble(src)

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
	})
}

func TestRefine(t *testing.T) {
	t.Parallel()

	noul := func(origin plan.Origin) plan.Question {
		return plan.Question{ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: origin}
	}

	pick := plan.Question{
		ID: "team", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginFile,
		Options: []plan.Option{{Name: "billing", Desc: "money"}, {Name: "technical"}},
	}

	tests := []struct {
		name     string
		question plan.Question
		events   []argv.Event
		want     plan.Question
	}{
		{
			name:     "should set a policy on a file question",
			question: noul(plan.OriginFile),
			events:   []argv.Event{{Name: "threshold", Value: "0.9"}, {Name: "fallback", Value: "yes"}},
			want: plan.Question{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile,
				Policy: plan.Policy{Threshold: pointerTo(0.9), Fallback: &plan.Fallback{Text: "yes", Boolean: true}},
				Typed:  []string{"--threshold", "--fallback"},
			},
		},
		{
			name:     "should replace a pick question's options, a repeated --pick adding to them",
			question: pick,
			events:   []argv.Event{{Name: "pick", Value: "sales"}, {Name: "pick", Value: "support"}},
			want: plan.Question{
				ID: "team", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginFile,
				Options: []plan.Option{{Name: "sales"}, {Name: "support"}}, Typed: []string{"--pick"},
			},
		},
		{
			name: "should replace a rate question's levels after --sep",
			question: plan.Question{
				ID: "mood", Shape: plan.Rate, Instructions: "q", Origin: plan.OriginFile, Labelled: true,
				Levels: []plan.Level{{Label: "calm"}, {Label: "angry"}},
			},
			events: []argv.Event{{Name: "sep", Value: ";"}, {Name: "rate", Value: "low;high"}},
			want: plan.Question{
				ID: "mood", Shape: plan.Rate, Instructions: "q", Origin: plan.OriginFile, Labelled: true,
				Levels: []plan.Level{{Label: "low"}, {Label: "high"}}, Typed: []string{"--rate"},
			},
		},
		{
			name:     "should merge --desc by key",
			question: pick,
			events:   []argv.Event{{Name: "desc", Value: "technical=systems"}},
			want: plan.Question{
				ID: "team", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginFile,
				Options:   []plan.Option{{Name: "billing", Desc: "money"}, {Name: "technical", Desc: "systems"}},
				DescOrder: []string{"technical"}, Typed: []string{"--desc"},
			},
		},
		{
			name:     "should leave --pick on a yes or no question for validation",
			question: noul(plan.OriginFile),
			events:   []argv.Event{{Name: "pick", Value: "a,b"}},
			want:     plan.Question{ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginFile},
		},
		{
			name:     "should leave --box on a file question for validation",
			question: pick,
			events:   []argv.Event{{Name: "box", Value: "true"}},
			want: plan.Question{
				ID: "team", Shape: plan.Pick, Instructions: "q", Origin: plan.OriginFile,
				Options: []plan.Option{{Name: "billing", Desc: "money"}, {Name: "technical"}},
			},
		},
		{
			name:     "should apply only the policy flags to a body question",
			question: noul(plan.OriginBody),
			events:   []argv.Event{{Name: "desc", Value: "yes=x"}, {Name: "threshold", Value: "0.8"}},
			want: plan.Question{
				ID: "urgent", Shape: plan.Noul, Instructions: "q", Origin: plan.OriginBody,
				Policy: plan.Policy{Threshold: pointerTo(0.8)}, Typed: []string{"--threshold"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			question := tc.question
			question.Options = slices.Clone(question.Options)
			plan.Refine(&question, tc.events, settings.PolicyNumbers())

			// Refine leaves an empty map of unknown keys, which reads the same as none.
			if len(question.UnknownDesc) == 0 {
				question.UnknownDesc = nil
			}

			if !reflect.DeepEqual(question, tc.want) {
				t.Errorf("question = %+v\nwant %+v", question, tc.want)
			}
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
