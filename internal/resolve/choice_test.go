package resolve

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestChoose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		given   Given
		speller plan.Speller
		env     map[string]string
		want    Chosen
		wantErr string
	}{
		{name: "should choose nothing when nothing is given", want: Chosen{}},
		{
			name:  "should choose the profile --profile names",
			given: Given{Profile: "work", ProfileSet: true},
			want:  Chosen{Profile: "work", By: "--profile"},
		},
		{
			name:  "should choose the provider --provider names",
			given: Given{Provider: "openrouter", ProviderSet: true},
			want:  Chosen{Provider: "openrouter", By: "--provider"},
		},
		{
			name:  "should trim the name a flag gives",
			given: Given{Profile: " work ", ProfileSet: true},
			want:  Chosen{Profile: "work", By: "--profile"},
		},
		{
			name: "should choose the profile ONESIE_PROFILE names",
			env:  map[string]string{"ONESIE_PROFILE": "work"},
			want: Chosen{Profile: "work", By: "ONESIE_PROFILE"},
		},
		{
			name: "should choose the provider ONESIE_PROVIDER names",
			env:  map[string]string{"ONESIE_PROVIDER": "berget"},
			want: Chosen{Provider: "berget", By: "ONESIE_PROVIDER"},
		},
		{
			name:  "should let --profile beat ONESIE_PROVIDER",
			given: Given{Profile: "work", ProfileSet: true},
			env:   map[string]string{"ONESIE_PROVIDER": "berget"},
			want:  Chosen{Profile: "work", By: "--profile"},
		},
		{
			name:  "should let --provider beat ONESIE_PROFILE",
			given: Given{Provider: "levanto", ProviderSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "work"},
			want:  Chosen{Provider: "levanto", By: "--provider"},
		},
		{
			name:  "should let --profile beat ONESIE_PROFILE",
			given: Given{Profile: "home", ProfileSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "work"},
			want:  Chosen{Profile: "home", By: "--profile"},
		},
		{
			name:  "should let --provider beat ONESIE_PROVIDER",
			given: Given{Provider: "typesafe", ProviderSet: true},
			env:   map[string]string{"ONESIE_PROVIDER": "berget"},
			want:  Chosen{Provider: "typesafe", By: "--provider"},
		},
		{
			name:  "should let --profile win over both variables",
			given: Given{Profile: "work", ProfileSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "home", "ONESIE_PROVIDER": "berget"},
			want:  Chosen{Profile: "work", By: "--profile"},
		},
		{
			name:    "should refuse --profile beside --provider",
			given:   Given{Profile: "work", ProfileSet: true, Provider: "typesafe", ProviderSet: true},
			wantErr: "--profile and --provider both choose what the run uses. Pass one",
		},
		{
			name: "should refuse ONESIE_PROFILE beside ONESIE_PROVIDER",
			env:  map[string]string{"ONESIE_PROFILE": "work", "ONESIE_PROVIDER": "berget"},
			wantErr: "ONESIE_PROFILE and ONESIE_PROVIDER are both set, and each chooses what the run uses. " +
				"Unset one",
		},
		{
			name: "should count an empty ONESIE_PROFILE as unset",
			env:  map[string]string{"ONESIE_PROFILE": "", "ONESIE_PROVIDER": "berget"},
			want: Chosen{Provider: "berget", By: "ONESIE_PROVIDER"},
		},
		{
			name: "should count a whitespace only ONESIE_PROVIDER as unset",
			env:  map[string]string{"ONESIE_PROVIDER": "  "},
			want: Chosen{},
		},
		{
			name:    "should name the flags as tool arguments for that speller",
			given:   Given{Profile: "work", ProfileSet: true, Provider: "typesafe", ProviderSet: true},
			speller: plan.Arguments,
			wantErr: "profile and provider both choose what the run uses. Pass one",
		},
		{
			name:    "should set By to the setting that chose, as the speller names it",
			given:   Given{Provider: "typesafe", ProviderSet: true},
			speller: plan.Arguments,
			want:    Chosen{Provider: "typesafe", By: "provider"},
		},
		{
			name:    "should set By from the environment variable that chose",
			env:     map[string]string{"ONESIE_PROFILE": "work"},
			speller: plan.Arguments,
			want:    Chosen{Profile: "work", By: "ONESIE_PROFILE"},
		},
		{
			name:  "should leave an unknown provider for the resolution to refuse",
			given: Given{Provider: "nope", ProviderSet: true},
			want:  Chosen{Provider: "nope", By: "--provider"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Choose(tc.given, lookupFrom(tc.env), tc.speller)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("choose returned an error: %v", err)
			}

			if got != tc.want {
				t.Errorf("choice = %+v, want %+v", got, tc.want)
			}
		})
	}
}
