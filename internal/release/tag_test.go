package release_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/release"
)

func TestParseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		want    release.Tag
		wantErr bool
	}{
		{name: "should parse a release", text: "v0.2.0", want: release.Tag{Minor: 2}},
		{
			name: "should parse a prerelease",
			text: "v0.2.0-rc.1",
			want: release.Tag{Minor: 2, Pre: []string{"rc", "1"}},
		},
		{name: "should refuse a tag with no v", text: "0.2.0", wantErr: true},
		{name: "should refuse a tag with no patch", text: "v0.2", wantErr: true},
		{name: "should refuse a leading zero", text: "v01.2.0", wantErr: true},
		{name: "should refuse build metadata", text: "v0.2.0+b1", wantErr: true},
		{name: "should refuse an empty prerelease", text: "v0.2.0-", wantErr: true},
		{name: "should refuse an empty prerelease identifier", text: "v0.2.0-rc..1", wantErr: true},
		{name: "should refuse a leading zero in a numeric prerelease identifier", text: "v0.2.0-rc.01", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := release.ParseTag(tc.text)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTag(%q) = %v, want an error", tc.text, got)
				}

				if !strings.Contains(err.Error(), "vMAJOR.MINOR.PATCH") {
					t.Errorf("error %q does not name the form", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseTag(%q): %v", tc.text, err)
			}

			if got.String() != tc.want.String() || got.Prerelease() != tc.want.Prerelease() {
				t.Errorf("ParseTag(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tag   string
		other string
		want  bool
	}{
		{name: "should put a minor above a lower patch", tag: "v0.2.0", other: "v0.1.9", want: true},
		{name: "should put a release above its prerelease", tag: "v0.2.0", other: "v0.2.0-rc.1", want: true},
		{name: "should put a prerelease below its release", tag: "v0.2.0-rc.1", other: "v0.2.0", want: false},
		{name: "should compare prerelease numbers", tag: "v0.2.0-rc.2", other: "v0.2.0-rc.1", want: true},
		{name: "should compare numeric identifiers as numbers", tag: "v0.2.0-rc.10", other: "v0.2.0-rc.9", want: true},
		{name: "should put a numeric identifier below an alphanumeric one", tag: "v0.2.0-rc", other: "v0.2.0-1", want: true},
		{name: "should put a longer prerelease above its prefix", tag: "v0.2.0-rc.1.1", other: "v0.2.0-rc.1", want: true},
		{name: "should put a major above any minor", tag: "v1.0.0", other: "v0.99.99", want: true},
		{name: "should not call an equal tag newer", tag: "v0.2.0", other: "v0.2.0", want: false},
		{name: "should not call an equal prerelease newer", tag: "v0.2.0-rc.1", other: "v0.2.0-rc.1", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := mustParse(t, tc.tag).Newer(mustParse(t, tc.other)); got != tc.want {
				t.Errorf("%s.Newer(%s) = %v, want %v", tc.tag, tc.other, got, tc.want)
			}
		})
	}
}

func TestCheckTag(t *testing.T) {
	t.Parallel()

	existing := []string{
		"v0.1.0",
		"latest",
		"v0.1.1-rc.1",
		"3b18e512dba79e4c8300dd08aeb37f8e728b8dad\trefs/tags/v0.1.0",
		"3b18e512dba79e4c8300dd08aeb37f8e728b8dad\trefs/tags/v0.1.0^{}",
		"9fceb02d0ae598e95dc970b74767f19372d61af8\trefs/tags/v0.1.5",
		"9fceb02d0ae598e95dc970b74767f19372d61af8\trefs/tags/v0.1.5^{}",
		"refs/tags/v9.0",
		"v10.0.0+build",
		"",
	}

	tests := []struct {
		name    string
		tag     string
		wantErr string
	}{
		{name: "should accept a tag above every existing one", tag: "v0.2.0"},
		{name: "should accept a prerelease above every existing one", tag: "v0.1.6-rc.1"},
		{name: "should refuse the latest tag itself", tag: "v0.1.5", wantErr: "latest tag is v0.1.5"},
		{name: "should refuse a tag below one that exists only on the remote", tag: "v0.1.2", wantErr: "latest tag is v0.1.5"},
		{name: "should refuse a prerelease of the latest tag", tag: "v0.1.5-rc.1", wantErr: "latest tag is v0.1.5"},
		{name: "should refuse a malformed tag", tag: "v0.2", wantErr: "vMAJOR.MINOR.PATCH"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := release.CheckTag(tc.tag, existing)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckTag(%q): %v", tc.tag, err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CheckTag(%q) = %v, want an error containing %q", tc.tag, err, tc.wantErr)
			}
		})
	}
}

func FuzzParseTag(f *testing.F) {
	for _, seed := range []string{
		"v0.2.0", "v0.2.0-rc.1", "v1.2.3-alpha.beta.10", "v0.2.0+b1", "v0.2.0-", "v0.2.0-rc..1",
		"v01.2.0", "v0.2", "0.2.0", "v99999999999999999999.0.0", "v0.0.0-0", "v0.0.0-00", "v0.0.0--",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		tag, err := release.ParseTag(text)
		if err != nil {
			return
		}

		if got := tag.String(); got != text {
			t.Fatalf("ParseTag(%q).String() = %q, want the input", text, got)
		}
	})
}

func mustParse(t *testing.T, text string) release.Tag {
	t.Helper()

	tag, err := release.ParseTag(text)
	if err != nil {
		t.Fatal(err)
	}

	return tag
}
