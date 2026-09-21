package plan_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/argv"
	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	readFile := func(string) ([]byte, error) { return nil, nil }

	tests := []struct {
		name         string
		events       []argv.Event
		file         []plan.Question
		positional   string
		cfg          plan.Config
		wantErr      string
		wantWarning  string
		wantFallback *bool
	}{
		{
			name:    "should reject an empty invocation",
			wantErr: "jev: no question given. Pass a question, --ask, or -f",
		},
		{
			name:       "should reject a positional combined with ask",
			positional: "is this urgent",
			events:     []argv.Event{{Name: "ask", Value: "a=first"}},
			wantErr:    "jev: a positional question cannot be combined with --ask or -f",
		},
		{
			name:       "should reject a positional combined with a file question",
			positional: "is this urgent",
			file: []plan.Question{
				{ID: "urgent", Shape: plan.Noul, Instructions: "is this urgent", Named: true},
			},
			wantErr: "jev: a positional question cannot be combined with --ask or -f",
		},
		{
			name:    "should reject a reserved id",
			events:  []argv.Event{{Name: "ask", Value: "usage=first"}},
			wantErr: "jev: question id 'usage' is reserved",
		},
		{
			name:    "should reject the positional id used explicitly",
			events:  []argv.Event{{Name: "ask", Value: "answer=first"}},
			wantErr: "jev: question id 'answer' is reserved",
		},
		{
			name:    "should reject an id beginning with two underscores",
			events:  []argv.Event{{Name: "ask", Value: "__private=first"}},
			wantErr: "jev: question id '__private' is reserved",
		},
		{
			name: "should reject a single option pick",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "safe"},
			},
			wantErr: "jev: --pick needs at least two options, got 1: safe",
		},
		{
			name: "should reject a single level rate",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "rate", Value: "calm"},
			},
			wantErr: "jev: --rate needs at least two levels, got 1: calm",
		},
		{
			name: "should reject a duplicate option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,technical,billing"},
			},
			wantErr: "jev: --pick option 'billing' is listed twice in question 'team'",
		},
		{
			name: "should reject a duplicate level",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,high,high"},
			},
			wantErr: "jev: --rate label 'high' is listed twice in question 'severity'",
		},
		{
			name: "should reject pick and rate together",
			events: []argv.Event{
				{Name: "ask", Value: "a=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "rate", Value: "p,q"},
			},
			wantErr: "jev: --pick and --rate are mutually exclusive",
		},
		{
			name: "should reject a partly described rubric",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "minor,major,critical"},
				{Name: "desc", Value: "minor=small"},
			},
			wantErr: "jev: --rate levels must all be described or all bare. 'severity' describes",
		},
		{
			name: "should reject a desc naming an unknown option",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "billing,technical"},
				{Name: "desc", Value: "bilingl=typo"},
			},
			wantErr: "jev: --desc names an unknown key 'bilingl' in question 'team'. --pick has: billing, technical",
		},
		{
			name: "should reject a desc other than yes or no on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "desc", Value: "maybe=unclear"},
			},
			wantErr: "jev: --desc on a yes/no question takes 'yes' or 'no', got 'maybe'",
		},
		{
			name: "should reject min-confidence on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "min-confidence", Value: "0.7"},
				{Name: "fallback", Value: "true"},
			},
			wantErr: "jev: --min-confidence needs a confidence value. 'urgent' is a yes/no question",
		},
		{
			name: "should reject threshold on a pick question",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "threshold", Value: "0.5"},
			},
			wantErr: "jev: --threshold cuts a yes/no probability. 'team' has options",
		},
		{
			name: "should reject threshold on a rate question",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "low,high"},
				{Name: "threshold", Value: "0.5"},
			},
			wantErr: "jev: --threshold cuts a yes/no probability. 'severity' has levels",
		},
		{
			name: "should reject min-confidence with no fallback",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "0.7"},
			},
			wantErr: "jev: --min-confidence needs --fallback, nothing to substitute for 'team'",
		},
		{
			name: "should reject a threshold outside zero to one",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "threshold", Value: "85"},
			},
			wantErr: "jev: --threshold must be between 0 and 1, got 85",
		},
		{
			name: "should reject a min-confidence outside zero to one",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
				{Name: "min-confidence", Value: "1.5"},
				{Name: "fallback", Value: "human"},
			},
			wantErr: "jev: --min-confidence must be between 0 and 1, got 1.5",
		},
		{
			name: "should reject a non boolean fallback on a yes/no question",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "fallback", Value: "human"},
			},
			wantErr: "jev: --fallback on a yes/no question takes true, false, yes or no, got 'human'",
		},
		{
			name: "should accept yes as a boolean fallback",
			events: []argv.Event{
				{Name: "ask", Value: "urgent=first"},
				{Name: "fallback", Value: "YES"},
			},
			wantFallback: pointerTo(true),
		},
		{
			name: "should reject a shape flag with no ask to bind to",
			events: []argv.Event{
				{Name: "pick", Value: "x,y"},
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
				{Name: "ask", Value: "c=three"},
			},
			wantErr: "jev: --pick given with no --ask to bind to and 3 questions asked",
		},
		{
			name: "should reject raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			cfg:     plan.Config{Raw: true},
			wantErr: "jev: -r needs a single question. 'a', 'b' were asked",
		},
		{
			name: "should reject -o raw with more than one question",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "b=two"},
			},
			cfg:     plan.Config{Output: "raw"},
			wantErr: "jev: -o raw needs a single question. 'a', 'b' were asked",
		},
		{
			name:       "should accept -o raw with a single question",
			positional: "is this urgent",
			cfg:        plan.Config{Output: "raw"},
		},
		{
			name: "should reject two questions sharing an id",
			events: []argv.Event{
				{Name: "ask", Value: "a=one"},
				{Name: "ask", Value: "a=two"},
			},
			wantErr: "jev: question id 'a' is given twice",
		},
		{
			name: "should reject quiet on a pick with no policy",
			events: []argv.Event{
				{Name: "ask", Value: "team=first"},
				{Name: "pick", Value: "x,y"},
			},
			cfg: plan.Config{Quiet: true},
			wantErr: "jev: -q on 'team' needs --min-confidence and --fallback. " +
				"Without a policy the exit code is always 0",
		},
		{
			name:       "should reject raw combined with output",
			positional: "is this urgent",
			cfg:        plan.Config{Raw: true, Output: "json"},
			wantErr:    "jev: -r and -o are mutually exclusive",
		},
		{
			name:       "should reject both state flags together",
			positional: "is this urgent",
			cfg:        plan.Config{HasState: true, HasStateFile: true},
			wantErr:    "jev: --state and --state-file are mutually exclusive",
		},
		{
			name:       "should warn about a partly described option set",
			positional: "which team",
			events: []argv.Event{
				{Name: "pick", Value: "billing,technical,sales"},
				{Name: "desc", Value: "billing=payments"},
				{Name: "desc", Value: "technical=bugs"},
			},
			wantWarning: "warning: 'answer' describes billing, technical but not sales",
		},
		{
			name:       "should accept a plain positional question",
			positional: "is this urgent",
		},
		{
			name: "should accept a fully described rubric",
			events: []argv.Event{
				{Name: "ask", Value: "severity=first"},
				{Name: "rate", Value: "minor,major"},
				{Name: "desc", Value: "minor=small"},
				{Name: "desc", Value: "major=big"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			built, err := plan.Assemble(plan.Source{
				Events:     tc.events,
				File:       tc.file,
				Positional: tc.positional,
				ReadFile:   readFile,
			})
			if err != nil {
				t.Fatalf("assembling: %v", err)
			}

			warnings, err := plan.Validate(built, tc.cfg)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got none", tc.wantErr)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q\nwant it to contain %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantFallback != nil {
				parsed := built.Questions[0].Policy.Fallback
				if parsed == nil {
					t.Fatal("fallback = nil, want it parsed onto the question")
				}

				if parsed.Boolean != *tc.wantFallback {
					t.Errorf("fallback boolean = %v, want %v", parsed.Boolean, *tc.wantFallback)
				}
			}

			if tc.wantWarning == "" {
				if len(warnings) != 0 {
					t.Errorf("unexpected warnings: %v", warnings)
				}

				return
			}

			joined := strings.Join(warnings, "\n")
			if !strings.Contains(joined, tc.wantWarning) {
				t.Errorf("warnings = %q\nwant one containing %q", joined, tc.wantWarning)
			}
		})
	}
}

func pointerTo[T any](value T) *T {
	return &value
}
