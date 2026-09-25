package main

import "testing"

func TestReleaseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stamped string
		module  string
		want    string
	}{
		{name: "should keep the tag the build stamped", stamped: "v0.2.0", module: "v0.1.0", want: "v0.2.0"},
		{name: "should take the module version go install records", module: "v0.2.0", want: "v0.2.0"},
		{name: "should take a prerelease module version", module: "v0.2.0-rc.1", want: "v0.2.0-rc.1"},
		{name: "should name no tag for a build from a checkout", module: "(devel)", want: ""},
		{name: "should name no tag when the build records no version", module: "", want: ""},
		{name: "should name no tag for a pseudo version", module: "v0.0.0-20260925101500-abcdef123456", want: ""},
		{name: "should name no tag for a pseudo version past a tag", module: "v0.2.1-0.20260925101500-abcdef123456", want: ""},
		{name: "should name no tag for a pseudo version past a prerelease", module: "v0.2.0-rc.1.0.20260925101500-abcdef123456", want: ""},
		{name: "should name no tag for a dirty pseudo version", module: "v0.2.1-0.20260925101500-abcdef123456+dirty", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := releaseTag(tc.stamped, tc.module); got != tc.want {
				t.Errorf("releaseTag(%q, %q) = %q, want %q", tc.stamped, tc.module, got, tc.want)
			}
		})
	}
}
