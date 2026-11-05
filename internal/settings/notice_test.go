package settings_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestOrigin_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		origin settings.Origin
		want   string
	}{
		{
			name:   "should spell a flag as given",
			origin: settings.Origin{Source: settings.SourceFlag, Spelled: "--model"},
			want:   "--model",
		},
		{
			name:   "should spell a variable by its name",
			origin: settings.Origin{Source: settings.SourceEnv, Spelled: "ONESIE_BASE_URL"},
			want:   "ONESIE_BASE_URL",
		},
		{
			name:   "should name a question file's key",
			origin: settings.Origin{Source: settings.SourceFile, Spelled: "threshold"},
			want:   "the question file's threshold",
		},
		{
			name:   "should name a request body's key",
			origin: settings.Origin{Source: settings.SourceFile, Spelled: "model", Body: true},
			want:   "the request body's model",
		},
		{
			name:   "should name a chosen profile's member",
			origin: settings.Origin{Source: settings.SourceChosenProfile, Spelled: "model"},
			want:   "the profile's model",
		},
		{
			name:   "should name the default profile's rank",
			origin: settings.Origin{Source: settings.SourceDefaultProfile, Spelled: "account_id"},
			want:   "the default profile's account_id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.origin.String(); got != tc.want {
				t.Errorf("String = %q, want %q", got, tc.want)
			}
		})
	}
}
