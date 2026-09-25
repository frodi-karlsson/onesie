package cli_test

import "testing"

func TestLiveLookup(t *testing.T) {
	t.Parallel()

	env := map[string]string{"ONESIE_MOCK": "answers.json", "ONESIE_CACHE": "1", "TYPESAFE_API_KEY": "k"}
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

	t.Run("should pin ONESIE_CACHE to 0, so a live run never reads or fills a developer's cache", func(t *testing.T) {
		t.Parallel()

		if value, found := lookup("ONESIE_CACHE"); !found || value != "0" {
			t.Errorf("lookup(ONESIE_CACHE) = %q, %v, want 0", value, found)
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

		if name == "ONESIE_CACHE" {
			return "0", true
		}

		return lookup(name)
	}
}
