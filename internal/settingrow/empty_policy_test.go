package settingrow_test

import (
	"errors"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func TestEmptyPolicyOf(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   settingrow.Kind
		policy settingrow.EmptyPolicy
	}{
		{"should allow empty text", settingrow.Text(), settingrow.AllowEmpty},
		{"should refuse blank text", settingrow.NonEmptyText(), settingrow.RefuseBlank},
		{"should refuse empty separators", settingrow.Separator(), settingrow.RefuseEmpty},
		{"should refuse blank enums", settingrow.Enum([]string{"a"}, nil), settingrow.RefuseBlank},
		{"should refuse blank endpoints", settingrow.Endpoint(), settingrow.RefuseBlank},
		{"should refuse empty durations", settingrow.Duration(), settingrow.RefuseEmpty},
		{"should leave boolean syntax to its parser", settingrow.Bool(), settingrow.AllowEmpty},
		{"should leave integer syntax to its parser", settingrow.Int(0, 1), settingrow.AllowEmpty},
		{"should leave probability syntax to its parser", settingrow.Probability(), settingrow.AllowEmpty},
		{"should allow empty expressions", settingrow.Expr(), settingrow.AllowEmpty},
		{"should allow empty fallbacks", settingrow.Fallback(), settingrow.AllowEmpty},
		{"should allow empty options", settingrow.Options(), settingrow.AllowEmpty},
		{"should allow empty levels", settingrow.Levels(), settingrow.AllowEmpty},
		{"should leave switch syntax to its parser", settingrow.TrueOnly(), settingrow.AllowEmpty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := settingrow.EmptyPolicyOf(tc.kind)
			if policy != tc.policy {
				t.Fatalf("policy = %v, want %v", policy, tc.policy)
			}
			for _, raw := range []string{"", " ", "\u2003\u00a0", "a"} {
				_, err := tc.kind.Parse(raw)
				if got, want := policy.Refuses(raw), errors.Is(err, settingrow.ErrEmptyValue); got != want {
					t.Errorf("Refuses %q = %v, parser empty error = %v", raw, got, want)
				}
			}
		})
	}
	t.Run("should apply endpoint policy to both halves", func(t *testing.T) {
		kind := settingrow.Endpoint()
		for _, half := range []settingrow.EndpointHalf{settingrow.HalfBaseURL, settingrow.HalfAccount} {
			for _, raw := range []string{"", "\u2003"} {
				_, err := kind.ParseHalf(raw, half)
				if !errors.Is(err, settingrow.ErrEmptyValue) {
					t.Errorf("ParseHalf %q error = %v", raw, err)
				}
			}
		}
	})
}
