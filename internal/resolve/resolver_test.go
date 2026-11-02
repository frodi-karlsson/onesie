package resolve

import (
	"errors"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestResolver_Resolved(t *testing.T) {
	t.Parallel()

	profiles := creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
		"work": {Provider: "openrouter", APIKey: "SECRET-WORK"},
	}}
	unreadable := errors.New("profiles.json is readable by others")
	refusedChoice := errors.New("the settings could not be read")

	tests := []struct {
		name         string
		given        Given
		givenErr     error
		env          map[string]string
		openErr      error
		neverOpen    bool
		wantProfile  string
		wantProvider string
		wantBy       string
		wantKeyFrom  string
		wantErr      string
		wantIs       error
		wantRefused  *Chosen
	}{
		{
			name: "should take the default profile when nothing chose", wantProfile: "work",
			wantProvider: "openrouter", wantBy: "the default", wantKeyFrom: KeyFromFile,
		},
		{
			name: "should take the profile --profile names", given: Given{Profile: "work", ProfileSet: true},
			wantProfile: "work", wantProvider: "openrouter", wantBy: "--profile", wantKeyFrom: KeyFromFile,
		},
		{
			name:    "should pass a refused choice on",
			given:   Given{Profile: "work", ProfileSet: true, Provider: "berget", ProviderSet: true},
			wantErr: "--profile and --provider both choose what the run uses. Pass one",
		},
		{
			name:     "should pass on the error the given choice failed with",
			givenErr: refusedChoice, neverOpen: true,
			wantErr: refusedChoice.Error(), wantIs: refusedChoice,
		},
		{
			name:  "should run without a profile under --provider and ignore the profile's key",
			given: Given{Provider: "openrouter", ProviderSet: true}, wantProvider: "openrouter",
			wantBy: "--provider", wantKeyFrom: KeyFromNone, neverOpen: true,
		},
		{
			name:         "should take the key from the provider's variable under ONESIE_PROVIDER",
			env:          map[string]string{"ONESIE_PROVIDER": "openrouter", "OPENROUTER_API_KEY": "SECRET-OR"},
			wantProvider: "openrouter", wantBy: "ONESIE_PROVIDER", wantKeyFrom: KeyFromEnv, neverOpen: true,
		},
		{
			name: "should refuse an unknown provider", given: Given{Provider: "bogus", ProviderSet: true},
			wantErr:   "onesie: unknown provider 'bogus'. Use typesafe, openrouter, berget, levanto or cloudflare",
			neverOpen: true,
		},
		{
			name:  "should return a ProviderError carrying the given choice without opening the file",
			given: Given{Provider: "work", ProviderSet: true}, neverOpen: true,
			wantErr:     "onesie: unknown provider 'work'. Use typesafe, openrouter, berget, levanto or cloudflare",
			wantRefused: &Chosen{Provider: "work", By: "--provider"},
		},
		{
			name: "should return the error opening the file for a run that needs a profile", openErr: unreadable,
			wantErr: unreadable.Error(), wantIs: unreadable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			open := func() (creds.Opened, error) {
				if tc.neverOpen {
					t.Error("the resolver opened the profiles file")
				}

				return creds.Opened{Profiles: profiles}, tc.openErr
			}
			given := func() (Given, error) { return tc.given, tc.givenErr }

			got, err := NewResolver(given, open, lookupFrom(tc.env), plan.Flags).Resolved()
			if tc.wantErr != "" {
				assertRefusal(t, err, tc.wantErr, tc.wantIs, tc.wantRefused)

				return
			}

			if err != nil {
				t.Fatalf("Resolved returned an error: %v", err)
			}

			if got.Profile != tc.wantProfile || got.Provider.Name() != tc.wantProvider || got.By != tc.wantBy ||
				got.KeyFrom != tc.wantKeyFrom {
				t.Errorf("resolution = profile %q, provider %q, by %q, key from %q, want %q, %q, %q, %q",
					got.Profile, got.Provider.Name(), got.By, got.KeyFrom,
					tc.wantProfile, tc.wantProvider, tc.wantBy, tc.wantKeyFrom)
			}
		})
	}
}

func assertRefusal(t *testing.T, err error, want string, is error, refused *Chosen) {
	t.Helper()

	if err == nil || err.Error() != want || (is != nil && !errors.Is(err, is)) {
		t.Errorf("error = %v, want %q", err, want)
	}

	var provider *ProviderError
	if refused != nil && (!errors.As(err, &provider) || provider.Given != *refused) {
		t.Errorf("error = %#v, want a ProviderError carrying %+v", err, *refused)
	}
}
