package plan_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestSpeller_Shape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		speller plan.Speller
		origin  plan.Origin
		want    string
	}{
		{name: "should spell a flag question's shape as its flag", origin: plan.OriginFlag, want: "--pick"},
		{name: "should spell a file question's shape as its key", origin: plan.OriginFile, want: "'pick'"},
		{name: "should spell a body's shape as its criteria key", origin: plan.OriginBody, want: "'criteria'"},
		{
			name:    "should spell an asked question's shape as a tool argument for that speller",
			speller: plan.Arguments,
			origin:  plan.OriginFlag,
			want:    "pick",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			question := &plan.Question{ID: "team", Origin: tc.origin}
			if got := tc.speller.Shape(question, "--pick"); got != tc.want {
				t.Errorf("Shape = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSpeller_For(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		speller plan.Speller
		origin  plan.Origin
		want    plan.Speller
	}{
		{name: "should spell a file question by its keys under flags", origin: plan.OriginFile, want: plan.Keys},
		{
			name:    "should spell a file question by its keys under tool arguments",
			speller: plan.Arguments,
			origin:  plan.OriginFile,
			want:    plan.Keys,
		},
		{name: "should keep the speller for a flag question", origin: plan.OriginFlag, want: plan.Flags},
		{
			name:    "should keep the speller for a positional question",
			speller: plan.Arguments,
			origin:  plan.OriginPositional,
			want:    plan.Arguments,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.speller.For(tc.origin); got != tc.want {
				t.Errorf("For = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSpeller_Spell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		speller plan.Speller
		flag    string
		want    string
	}{
		{name: "should keep a flag as it is", flag: "--min-confidence", want: "--min-confidence"},
		{name: "should keep a short flag as it is", flag: "-i", want: "-i"},
		{name: "should quote a key", speller: plan.Keys, flag: "--min-confidence", want: "'min_confidence'"},
		{name: "should name a tool argument", speller: plan.Arguments, flag: "--abstain-if", want: "abstain_if"},
		{name: "should name the output setting", speller: plan.Arguments, flag: "-o", want: "output"},
		{name: "should name the input setting", speller: plan.Arguments, flag: "-i", want: "input"},
		{name: "should quote the input key", speller: plan.Keys, flag: "-i", want: "'input'"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.speller.Spell(tc.flag); got != tc.want {
				t.Errorf("Spell(%q) = %q, want %q", tc.flag, got, tc.want)
			}
		})
	}
}
