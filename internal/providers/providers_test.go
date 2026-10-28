package providers_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/providers"
)

func TestNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{
			name: "should join every provider with or before the last",
			want: "typesafe, openrouter, berget, levanto or cloudflare",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := providers.Names(); got != tc.want {
				t.Errorf("Names = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKeyVariables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{
			name: "should join every key variable in the order of the names",
			want: "TYPESAFE_API_KEY, OPENROUTER_API_KEY, BERGET_API_KEY, LEVANTO_API_KEY or CLOUDFLARE_API_TOKEN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := providers.KeyVariables(); got != tc.want {
				t.Errorf("KeyVariables = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithAccounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "should name only the providers with an account variable", want: "cloudflare"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := providers.WithAccounts(); got != tc.want {
				t.Errorf("WithAccounts = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAccountVariables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "should name the account variable of each provider with accounts", want: "CLOUDFLARE_ACCOUNT_ID"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := providers.AccountVariables(); got != tc.want {
				t.Errorf("AccountVariables = %q, want %q", got, tc.want)
			}
		})
	}
}
