package cli_test

import "testing"

func TestLiveLookup(t *testing.T) {
	t.Parallel()

	env := map[string]string{"ONESIE_MOCK": "answers.json", "TYPESAFE_API_KEY": "k"}
	lookup := liveLookup(func(name string) (string, bool) {
		value, found := env[name]

		return value, found
	})

	t.Run("should hide ONESIE_MOCK, so a live run never answers from a mock", func(t *testing.T) {
		t.Parallel()

		if value, found := lookup("ONESIE_MOCK"); found || value != "" {
			t.Errorf("lookup(ONESIE_MOCK) = %q, %v, want nothing", value, found)
		}
	})

	t.Run("should pass every other variable through", func(t *testing.T) {
		t.Parallel()

		if value, found := lookup("TYPESAFE_API_KEY"); !found || value != "k" {
			t.Errorf("lookup(TYPESAFE_API_KEY) = %q, %v, want k", value, found)
		}
	})
}

func liveLookup(lookup func(string) (string, bool)) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if name == "ONESIE_MOCK" {
			return "", false
		}

		return lookup(name)
	}
}
