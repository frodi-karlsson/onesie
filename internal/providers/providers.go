// Package providers words onesie-go's provider list for help and messages, so a provider the
// library adds reaches every one of them.
package providers

import (
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
)

// Names joins every provider's name in the library's order, as typesafe, openrouter, berget,
// levanto or cloudflare.
func Names() string {
	return joined(onesie.Providers(), onesie.Provider.Name)
}

// KeyVariables joins every provider's API key variable, in the order Names gives.
func KeyVariables() string {
	return joined(onesie.Providers(), onesie.Provider.EnvAPIKey)
}

// WithAccounts joins the names of the providers that need an account id, as cloudflare.
func WithAccounts() string {
	return joined(withAccounts(), onesie.Provider.Name)
}

// AccountVariables joins the account id variables of the providers WithAccounts names.
func AccountVariables() string {
	return joined(withAccounts(), onesie.Provider.EnvAccountID)
}

func withAccounts() []onesie.Provider {
	return slices.DeleteFunc(onesie.Providers(), func(provider onesie.Provider) bool {
		return provider.EnvAccountID() == ""
	})
}

func joined(providers []onesie.Provider, word func(onesie.Provider) string) string {
	words := make([]string, 0, len(providers))
	for _, provider := range providers {
		words = append(words, word(provider))
	}

	if len(words) < 2 {
		return strings.Join(words, "")
	}

	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}
