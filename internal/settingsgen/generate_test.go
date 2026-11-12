package settingsgen_test

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/qfile"
	"github.com/frodi-karlsson/onesie/internal/settingsgen"
)

const root = "repo"

func TestGenerate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reference string
		check     func(t *testing.T, files *memFS, err error)
	}{
		{
			name:      "should replace each block in place",
			reference: staleReference(),
			check: func(t *testing.T, files *memFS, err error) {
				t.Helper()

				if err != nil {
					t.Fatalf("Generate: %v", err)
				}

				reference := files.text("REFERENCE.md")
				for _, want := range []string{
					"<!-- settings:environment begin -->\n| Variable | What it does |\n",
					"| `TYPESAFE_API_KEY`, `OPENROUTER_API_KEY`, `BERGET_API_KEY`, `LEVANTO_API_KEY`, `CLOUDFLARE_API_TOKEN`, `AI_GATEWAY_API_KEY` |",
					"| `ONESIE_MOCK` | a file to answer from, as `--mock`. Skipped beside `--list-models`, `-i request`, `--print-request` and `--print-questions` |\n",
					"anything else exits 2 where it applies |\n<!-- settings:environment end -->",
					"<!-- settings:question_keys begin -->\n| Key | Where | What it does |\n",
					"| `min_confidence` | a question |",
					"| `assert` | the file |",
					"| `model` | a request body |",
					"<!-- settings:profile_members begin -->\n| Member | Meaning |\n",
					"| `store`, `account` | `keychain` and the keychain item's name",
				} {
					if !strings.Contains(reference, want) {
						t.Errorf("REFERENCE.md lacks %q:\n%s", want, reference)
					}
				}

				if strings.Contains(reference, "stale") {
					t.Errorf("REFERENCE.md keeps a stale block:\n%s", reference)
				}

				if got := files.text("schema/questions.json"); got != string(qfile.Schema()) {
					t.Errorf("schema/questions.json is not qfile.Schema:\n%s", got)
				}
			},
		},
		{
			name:      "should leave prose outside markers untouched",
			reference: staleReference(),
			check: func(t *testing.T, files *memFS, err error) {
				t.Helper()

				if err != nil {
					t.Fatalf("Generate: %v", err)
				}

				reference := files.text("REFERENCE.md")
				for _, prose := range []string{"# Top\n\nBefore.\n\n", "\n\nBetween.\n\n", "\n\nAfter.\n"} {
					if !strings.Contains(reference, prose) {
						t.Errorf("REFERENCE.md lost %q:\n%s", prose, reference)
					}
				}

				if !strings.HasPrefix(reference, "# Top\n\nBefore.\n\n<!-- settings:environment begin -->") ||
					!strings.HasSuffix(reference, "<!-- settings:profile_members end -->\n\nAfter.\n") {
					t.Errorf("REFERENCE.md moved the prose around the blocks:\n%s", reference)
				}
			},
		},
		{
			name:      "should refuse a missing marker",
			reference: strings.Replace(staleReference(), "<!-- settings:profile_members end -->", "", 1),
			check: func(t *testing.T, files *memFS, err error) {
				t.Helper()

				if err == nil || !strings.Contains(err.Error(), "settings:profile_members end") {
					t.Errorf("Generate error = %v, want one naming settings:profile_members end", err)
				}

				if files.text("REFERENCE.md") != strings.Replace(staleReference(), "<!-- settings:profile_members end -->", "", 1) {
					t.Error("Generate rewrote REFERENCE.md despite the missing marker")
				}
			},
		},
		{
			name:      "should refuse a marker given twice",
			reference: staleReference() + "<!-- settings:environment begin -->\n",
			check: func(t *testing.T, _ *memFS, err error) {
				t.Helper()

				if err == nil || !strings.Contains(err.Error(), "settings:environment begin") {
					t.Errorf("Generate error = %v, want one naming settings:environment begin", err)
				}
			},
		},
		{
			name:      "should refuse a marker that names no known block",
			reference: staleReference() + "<!-- settings:run-uses begin -->\n",
			check: func(t *testing.T, files *memFS, err error) {
				t.Helper()

				if err == nil || !strings.Contains(err.Error(), "settings:run-uses") {
					t.Errorf("Generate error = %v, want one naming settings:run-uses", err)
				}

				if len(files.writes) != 0 {
					t.Errorf("Generate wrote %v despite the unknown marker", files.writes)
				}
			},
		},
		{
			name:      "should not rewrite an unchanged file",
			reference: staleReference(),
			check: func(t *testing.T, files *memFS, err error) {
				t.Helper()

				if err != nil {
					t.Fatalf("Generate: %v", err)
				}

				files.writes = nil

				if err := settingsgen.Generate(root, files); err != nil {
					t.Fatalf("second Generate: %v", err)
				}

				if len(files.writes) != 0 {
					t.Errorf("second Generate wrote %v", files.writes)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			files := newMemFS(map[string]string{"REFERENCE.md": tc.reference})
			err := settingsgen.Generate(root, files)
			tc.check(t, files, err)
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		generated bool
		schema    string
		want      []string
	}{
		{name: "should name a stale block", schema: string(qfile.Schema()), want: []string{"REFERENCE.md"}},
		{name: "should name a stale schema", generated: true, schema: "{}\n", want: []string{"schema/questions.json"}},
		{name: "should name nothing when every file is current", generated: true, schema: string(qfile.Schema())},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			files := newMemFS(map[string]string{"REFERENCE.md": staleReference()})
			if tc.generated {
				if err := settingsgen.Generate(root, files); err != nil {
					t.Fatalf("Generate: %v", err)
				}
			}

			files.files[filepath.Join(root, "schema", "questions.json")] = []byte(tc.schema)
			before := files.snapshot()
			files.writes = nil

			got, err := settingsgen.Check(root, files)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("Check = %v, want %v", got, tc.want)
			}

			if len(files.writes) != 0 || !files.same(before) {
				t.Errorf("Check should write nothing, wrote %v", files.writes)
			}
		})
	}
}

func staleReference() string {
	return "# Top\n\nBefore.\n\n" +
		"<!-- settings:environment begin -->\nstale\n<!-- settings:environment end -->\n\nBetween.\n\n" +
		"<!-- settings:question_keys begin -->\nstale\n<!-- settings:question_keys end -->\n\nBetween.\n\n" +
		"<!-- settings:run_uses begin -->\nstale\n<!-- settings:run_uses end -->\n\nBetween.\n\n" +
		"<!-- settings:profile_members begin -->\nstale\n<!-- settings:profile_members end -->\n\nAfter.\n"
}

func newMemFS(texts map[string]string) *memFS {
	files := &memFS{files: map[string][]byte{}}
	for name, text := range texts {
		files.files[filepath.Join(root, name)] = []byte(text)
	}

	return files
}

type memFS struct {
	files  map[string][]byte
	writes []string
}

func (m *memFS) ReadFile(name string) ([]byte, error) {
	data, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}

	return bytes.Clone(data), nil
}

func (m *memFS) WriteFile(name string, data []byte) error {
	m.files[name] = bytes.Clone(data)
	m.writes = append(m.writes, name)

	return nil
}

func (m *memFS) text(name string) string {
	return string(m.files[filepath.Join(root, name)])
}

func (m *memFS) snapshot() map[string][]byte {
	copied := map[string][]byte{}
	for name, data := range m.files {
		copied[name] = bytes.Clone(data)
	}

	return copied
}

func (m *memFS) same(other map[string][]byte) bool {
	if len(other) != len(m.files) {
		return false
	}

	for name, data := range m.files {
		if !bytes.Equal(other[name], data) {
			return false
		}
	}

	return true
}
