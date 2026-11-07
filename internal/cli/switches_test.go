package cli

import (
	"testing"

	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestCheckEnvSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{name: "should accept no variable"},
		{name: "should accept values with spaces around them", env: map[string]string{settingEnv(setting.Cache.Name()): " 1 ", settingEnv(setting.CacheTTL.Name()): " 90m"}},
		{
			name:    "should refuse an ONESIE_LONG_CONTEXT the flag would not take",
			env:     map[string]string{settingEnv(setting.LongContext.Name()): "garbage"},
			wantErr: "ONESIE_LONG_CONTEXT=garbage is not a value --long-context takes. Use 1 or 0",
		},
		{
			name:    "should refuse an ONESIE_CACHE_TTL that is no duration",
			env:     map[string]string{settingEnv(setting.CacheTTL.Name()): "soon"},
			wantErr: "ONESIE_CACHE_TTL=soon is not a duration of zero or more, such as 90m or 72h",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkEnvSettings(lookupFrom(tc.env), "cache", "long_context", "cache_ttl")
			if got := errText(err); got != tc.wantErr {
				t.Errorf("checkEnvSettings = %q, want %q", got, tc.wantErr)
			}
		})
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
