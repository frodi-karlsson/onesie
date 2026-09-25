package release_test

import (
	"io/fs"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/release"
)

func TestBump(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tag      string
		dryRun   bool
		from     string
		edit     func(files map[string]string)
		want     []string
		wantErr  string
		wantKept bool
	}{
		{
			name: "should set the version in both plugins and the ref in both marketplaces",
			tag:  "v0.2.0",
			want: []string{
				"set .claude-plugin/plugin.json version from 0.1.0 to 0.2.0",
				"set .codex-plugin/plugin.json version from 0.1.0 to 0.2.0",
				"set .claude-plugin/marketplace.json ref from v0.1.0 to v0.2.0",
				"set .agents/plugins/marketplace.json ref from v0.1.0 to v0.2.0",
			},
		},
		{
			name:   "should word a dry run as what it would set and write nothing",
			tag:    "v0.2.0",
			dryRun: true,
			want: []string{
				"would set .claude-plugin/plugin.json version from 0.1.0 to 0.2.0",
				"would set .codex-plugin/plugin.json version from 0.1.0 to 0.2.0",
				"would set .claude-plugin/marketplace.json ref from v0.1.0 to v0.2.0",
				"would set .agents/plugins/marketplace.json ref from v0.1.0 to v0.2.0",
			},
		},
		{
			name:     "should say it would leave a manifest that already names the tag",
			tag:      "v0.2.0",
			dryRun:   true,
			from:     "0.2.0",
			wantKept: true,
			want: []string{
				"would leave .claude-plugin/plugin.json version at 0.2.0",
				"would leave .codex-plugin/plugin.json version at 0.2.0",
				"would leave .claude-plugin/marketplace.json ref at v0.2.0",
				"would leave .agents/plugins/marketplace.json ref at v0.2.0",
			},
		},
		{
			name:     "should not rewrite a manifest that already names the tag",
			tag:      "v0.2.0",
			from:     "0.2.0",
			wantKept: true,
			want: []string{
				"left .claude-plugin/plugin.json version at 0.2.0",
				"left .codex-plugin/plugin.json version at 0.2.0",
				"left .claude-plugin/marketplace.json ref at v0.2.0",
				"left .agents/plugins/marketplace.json ref at v0.2.0",
			},
		},
		{
			name: "should refuse a file where the key is missing, and write nothing",
			tag:  "v0.2.0",
			edit: func(files map[string]string) {
				files[".agents/plugins/marketplace.json"] = strings.Replace(files[".agents/plugins/marketplace.json"], `"ref"`, `"tag"`, 1)
			},
			wantErr: `.agents/plugins/marketplace.json holds "ref" 0 times, want once`,
		},
		{
			name: "should refuse a file where the key appears twice, and write nothing",
			tag:  "v0.2.0",
			edit: func(files map[string]string) {
				files[".codex-plugin/plugin.json"] = strings.Replace(files[".codex-plugin/plugin.json"], `"name"`, `"version": "9.9.9", "name"`, 1)
			},
			wantErr: `.codex-plugin/plugin.json holds "version" 2 times, want once`,
		},
		{
			name:    "should refuse a prerelease, which names no manifest",
			tag:     "v0.2.0-rc.1",
			wantErr: "v0.2.0-rc.1 is a prerelease",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			from := tc.from
			if from == "" {
				from = "0.1.0"
			}

			files := manifests(from)
			if tc.edit != nil {
				tc.edit(files)
			}

			fake := &fakeFiles{root: "root", files: maps.Clone(files)}

			changes, err := release.Bump("root", mustParse(t, tc.tag), tc.dryRun, fake)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Bump = %v, want an error containing %q", err, tc.wantErr)
				}

				if len(fake.written) != 0 {
					t.Errorf("Bump wrote %v after refusing", fake.written)
				}

				return
			}

			if err != nil {
				t.Fatalf("Bump: %v", err)
			}

			var got []string
			for _, change := range changes {
				got = append(got, change.Describe(tc.dryRun))
			}

			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}

			if tc.dryRun || tc.wantKept {
				if len(fake.written) != 0 {
					t.Errorf("Bump wrote %v, want nothing written", fake.written)
				}

				return
			}

			for name, content := range manifests("0.2.0") {
				if fake.files[name] != content {
					t.Errorf("%s =\n%s\nwant\n%s", name, fake.files[name], content)
				}
			}
		})
	}
}

type fakeFiles struct {
	root    string
	files   map[string]string
	written []string
}

func (f *fakeFiles) ReadFile(name string) ([]byte, error) {
	content, ok := f.files[f.rel(name)]
	if !ok {
		return nil, fs.ErrNotExist
	}

	return []byte(content), nil
}

func (f *fakeFiles) WriteFile(name string, data []byte) error {
	f.written = append(f.written, f.rel(name))
	f.files[f.rel(name)] = string(data)

	return nil
}

func (f *fakeFiles) rel(name string) string {
	rel, err := filepath.Rel(f.root, name)
	if err != nil {
		return name
	}

	return filepath.ToSlash(rel)
}

func manifests(version string) map[string]string {
	return map[string]string{
		".claude-plugin/plugin.json": `{
  "name": "onesie",
  "version": "` + version + `",
  "description": "Skills, with a version: 0.1.0 in prose",
  "author": {
    "name": "Frodi Karlsson"
  }
}
`,
		".codex-plugin/plugin.json": `{
  "name": "onesie",
  "version":"` + version + `",
  "skills": "./skills/",
  "interface": {
    "displayName": "onesie"
  }
}
`,
		".claude-plugin/marketplace.json": `{
  "$schema": "https://www.schemastore.org/claude-code-marketplace.json",
  "plugins": [
    {
      "name": "onesie",
      "source": {
        "source": "github",
        "ref": "v` + version + `"
      }
    }
  ]
}
`,
		".agents/plugins/marketplace.json": `{
  "plugins": [
    {
      "source": {
        "url": "https://github.com/frodi-karlsson/onesie.git",
        "ref" : "v` + version + `"
      }
    }
  ]
}
`,
	}
}
