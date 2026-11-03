package resolve

import (
	"fmt"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

// The environment variables that choose the profile or the provider of a run.
const (
	EnvProfile  = "ONESIE_PROFILE"
	EnvProvider = "ONESIE_PROVIDER"
)

// Choose decides the profile or provider a run uses, from what was given and then from the
// environment. speller names the given settings.
func Choose(given Given, lookupEnv func(string) (string, bool), speller plan.Speller) (Chosen, error) {
	profileSetting, providerSetting := speller.Spell("--profile"), speller.Spell("--provider")

	if given.ProfileSet && given.ProviderSet {
		return Chosen{}, fmt.Errorf("%s and %s both choose what the run uses. Pass one",
			profileSetting, providerSetting)
	}

	switch {
	case given.ProfileSet:
		return Chosen{Profile: strings.TrimSpace(given.Profile), By: profileSetting}, nil
	case given.ProviderSet:
		return Chosen{Provider: strings.TrimSpace(given.Provider), By: providerSetting}, nil
	}

	profile, provider := setVariable(lookupEnv, EnvProfile), setVariable(lookupEnv, EnvProvider)

	switch {
	case profile != "" && provider != "":
		return Chosen{}, fmt.Errorf(
			"%s and %s are both set, and each chooses what the run uses. Unset one", EnvProfile, EnvProvider)
	case profile != "":
		return Chosen{Profile: profile, By: EnvProfile}, nil
	case provider != "":
		return Chosen{Provider: provider, By: EnvProvider}, nil
	}

	return Chosen{}, nil
}

// Given is the profile or provider a caller chose, each with whether it was given at all.
type Given struct {
	Profile, Provider       string
	ProfileSet, ProviderSet bool
}

// Chosen is the profile or provider a run uses, and By what chose it. By is empty when nothing did.
type Chosen struct {
	Profile  string
	Provider string
	By       string
}

func setVariable(lookupEnv func(string) (string, bool), name string) string {
	value, _ := lookupEnv(name)

	return strings.TrimSpace(value)
}
