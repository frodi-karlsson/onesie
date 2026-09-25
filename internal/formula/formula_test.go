package formula_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/formula"
)

func TestRender(t *testing.T) {
	t.Parallel()

	checksums := readFile(t, filepath.Join("testdata", "checksums.txt"))
	golden := readFile(t, filepath.Join("testdata", "onesie.rb"))

	tests := []struct {
		name      string
		version   string
		checksums string
		want      string
		wantErr   string
	}{
		{
			name:      "should match the golden formula",
			version:   "1.2.3",
			checksums: checksums,
			want:      golden,
		},
		{
			name:      "should drop a leading v from the version",
			version:   "v1.2.3",
			checksums: checksums,
			want:      golden,
		},
		{
			name:      "should fail when an archive the formula installs has no checksum",
			version:   "1.2.3",
			checksums: dropLine(checksums, "linux_arm64"),
			wantErr:   "no entry for onesie_1.2.3_linux_arm64.tar.gz",
		},
		{
			name:      "should fail when the checksums belong to another version",
			version:   "1.2.4",
			checksums: checksums,
			wantErr:   "no entry for onesie_1.2.4_darwin_arm64.tar.gz",
		},
		{
			name:      "should fail on a line that is not a sha256 and a name",
			version:   "1.2.3",
			checksums: "abc  onesie_1.2.3_darwin_arm64.tar.gz\n",
			wantErr:   "line 1 is not a sha256",
		},
		{
			name:      "should fail on an empty version",
			version:   "",
			checksums: checksums,
			wantErr:   "is not a release version",
		},
		{
			name:      "should fail on a version that would break out of the ruby string",
			version:   `1.2.3" do`,
			checksums: checksums,
			wantErr:   "is not a release version",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := formula.Render(tc.version, strings.NewReader(tc.checksums))

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("formula differs from testdata/onesie.rb:\n%s", got)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func dropLine(text, substr string) string {
	var kept []string

	for _, line := range strings.SplitAfter(text, "\n") {
		if !strings.Contains(line, substr) {
			kept = append(kept, line)
		}
	}

	return strings.Join(kept, "")
}
