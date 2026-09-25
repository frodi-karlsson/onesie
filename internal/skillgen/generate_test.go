package skillgen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/skillgen"
)

func TestGenerate(t *testing.T) {
	t.Parallel()

	t.Run("should write every client output", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "onesie-gate")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, rel := range []string{
			filepath.Join("skills", "onesie-gate", "SKILL.md"),
			filepath.Join(".agents", "skills", "onesie-gate", "SKILL.md"),
			filepath.Join(".cursor", "skills", "onesie-gate", "SKILL.md"),
			filepath.Join("skills", "onesie-gate", "agents", "gemini.toml"),
			filepath.Join(".agents", "skills", "onesie-gate", "references", "a.md"),
			filepath.Join(".cursor", "skills", "onesie-gate", "references", "a.md"),
		} {
			requireFile(t, filepath.Join(root, rel))
		}

		gemini := readFile(t, filepath.Join(root, "skills", "onesie-gate", "agents", "gemini.toml"))
		if !strings.Contains(gemini, "description = ") {
			t.Errorf("gemini.toml = %q, want a description line", gemini)
		}
	})

	t.Run("should write .agents skills and .cursor skills copies byte identical to skills SKILL.md", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "onesie-gate")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		source := readFile(t, filepath.Join(root, "skills", "onesie-gate", "SKILL.md"))
		agents := readFile(t, filepath.Join(root, ".agents", "skills", "onesie-gate", "SKILL.md"))
		cursor := readFile(t, filepath.Join(root, ".cursor", "skills", "onesie-gate", "SKILL.md"))

		if agents != source {
			t.Errorf(".agents/skills/onesie-gate/SKILL.md is not byte identical to skills/onesie-gate/SKILL.md")
		}

		if cursor != source {
			t.Errorf(".cursor/skills/onesie-gate/SKILL.md is not byte identical to skills/onesie-gate/SKILL.md")
		}
	})

	t.Run("should copy references unchanged", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "onesie-gate")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		source := readFile(t, filepath.Join(root, "skills", "onesie-gate", "references", "a.md"))
		agents := readFile(t, filepath.Join(root, ".agents", "skills", "onesie-gate", "references", "a.md"))
		cursor := readFile(t, filepath.Join(root, ".cursor", "skills", "onesie-gate", "references", "a.md"))

		if agents != source || cursor != source {
			t.Errorf("reference copy is not byte identical to skills/onesie-gate/references/a.md")
		}
	})

	t.Run("should pass sections to RenderSkill in skill.Sections order", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		dir := filepath.Join(root, "skills", "ordered")
		mustMkdirAll(t, filepath.Join(dir, "sections"))
		mustWriteFile(t, filepath.Join(dir, "skill.json"), `{
			"name": "ordered",
			"description": "checks section order",
			"sections": ["sections/first.md", "sections/second.md"]
		}`)
		mustWriteFile(t, filepath.Join(dir, "sections", "first.md"), "SECTION-FIRST-MARKER")
		mustWriteFile(t, filepath.Join(dir, "sections", "second.md"), "SECTION-SECOND-MARKER")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readFile(t, filepath.Join(dir, "SKILL.md"))

		first := strings.Index(got, "SECTION-FIRST-MARKER")
		second := strings.Index(got, "SECTION-SECOND-MARKER")

		if first == -1 || second == -1 {
			t.Fatalf("SKILL.md = %q, want both section markers", got)
		}

		if first > second {
			t.Errorf("SKILL.md carries SECTION-SECOND-MARKER before SECTION-FIRST-MARKER, want skill.Sections order")
		}
	})

	t.Run("should leave the tree untouched on a second run", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "onesie-gate")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on first run: %v", err)
		}

		paths := []string{
			filepath.Join(root, "skills", "onesie-gate", "SKILL.md"),
			filepath.Join(root, ".agents", "skills", "onesie-gate", "SKILL.md"),
			filepath.Join(root, ".cursor", "skills", "onesie-gate", "SKILL.md"),
			filepath.Join(root, "skills", "onesie-gate", "agents", "gemini.toml"),
			filepath.Join(root, ".agents", "skills", "onesie-gate", "references", "a.md"),
			filepath.Join(root, ".cursor", "skills", "onesie-gate", "references", "a.md"),
		}

		before := make(map[string][]byte, len(paths))

		// Set far in the past, so a rewrite shows as a new mtime however coarse the clock is.
		stamped := time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)

		for _, p := range paths {
			if err := os.Chtimes(p, stamped, stamped); err != nil {
				t.Fatalf("stamping %s: %v", p, err)
			}

			before[p] = []byte(readFile(t, p))
		}

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on second run: %v", err)
		}

		for _, p := range paths {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatalf("stat %s after second run: %v", p, err)
			}

			if !info.ModTime().Equal(stamped) {
				t.Errorf("%s modtime changed on a no op run: was %v, now %v", p, stamped, info.ModTime())
			}

			if got := readFile(t, p); got != string(before[p]) {
				t.Errorf("%s content changed on a no op run", p)
			}
		}
	})

	t.Run("should return an error naming the skill when validation fails and write nothing", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "aaa-good")

		dir := filepath.Join(root, "skills", "zzz-broken")
		mustMkdirAll(t, dir)
		mustWriteFile(t, filepath.Join(dir, "skill.json"), `{"name": "zzz-broken"}`)

		err := skillgen.Generate(root)
		if err == nil {
			t.Fatalf("expected an error, got none")
		}

		if !strings.Contains(err.Error(), "zzz-broken") {
			t.Errorf("error = %q, want it to name the skill zzz-broken", err.Error())
		}

		requireAbsent(t, filepath.Join(root, "skills", "aaa-good", "SKILL.md"))
		requireAbsent(t, filepath.Join(root, ".agents"))
		requireAbsent(t, filepath.Join(root, ".cursor"))
	})

	t.Run("should remove a mirror whose source skill was renamed or deleted", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "old-name")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on first run: %v", err)
		}

		requireFile(t, filepath.Join(root, ".agents", "skills", "old-name", "SKILL.md"))
		requireFile(t, filepath.Join(root, ".cursor", "skills", "old-name", "SKILL.md"))

		if err := os.Rename(
			filepath.Join(root, "skills", "old-name"),
			filepath.Join(root, "skills", "new-name"),
		); err != nil {
			t.Fatalf("rename: %v", err)
		}

		renamed := filepath.Join(root, "skills", "new-name", "skill.json")
		body := strings.ReplaceAll(readFile(t, renamed), `"old-name"`, `"new-name"`)
		mustWriteFile(t, renamed, body)

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on second run: %v", err)
		}

		requireAbsent(t, filepath.Join(root, ".agents", "skills", "old-name"))
		requireAbsent(t, filepath.Join(root, ".cursor", "skills", "old-name"))
		requireFile(t, filepath.Join(root, ".agents", "skills", "new-name", "SKILL.md"))
		requireFile(t, filepath.Join(root, ".cursor", "skills", "new-name", "SKILL.md"))
	})

	t.Run("should remove a mirror whose source skill was deleted outright", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "gone")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on first run: %v", err)
		}

		if err := os.RemoveAll(filepath.Join(root, "skills", "gone")); err != nil {
			t.Fatalf("remove skill: %v", err)
		}

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on second run: %v", err)
		}

		requireAbsent(t, filepath.Join(root, ".agents", "skills", "gone"))
		requireAbsent(t, filepath.Join(root, ".cursor", "skills", "gone"))
	})

	t.Run("should not delete anything outside the mirror directories during a prune", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "old-name")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on first run: %v", err)
		}

		untouched := map[string]string{
			filepath.Join(root, ".agents", "other-tool", "file.txt"): "keep me",
			filepath.Join(root, ".cursor", "other-tool", "file.txt"): "keep me",
			filepath.Join(root, "skills", "README.md"):               "keep me",
			filepath.Join(root, "some-unrelated-file.txt"):           "keep me",
		}

		for p, content := range untouched {
			mustMkdirAll(t, filepath.Dir(p))
			mustWriteFile(t, p, content)
		}

		if err := os.RemoveAll(filepath.Join(root, "skills", "old-name")); err != nil {
			t.Fatalf("remove skill: %v", err)
		}

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on second run: %v", err)
		}

		for p, content := range untouched {
			if got := readFile(t, p); got != content {
				t.Errorf("%s = %q, want it untouched by the prune", p, got)
			}
		}
	})

	t.Run("should write nothing when skills does not exist", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		requireAbsent(t, filepath.Join(root, ".agents"))
		requireAbsent(t, filepath.Join(root, ".cursor"))
	})

	t.Run("should write nothing when skills exists and is empty", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "skills"))

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		requireAbsent(t, filepath.Join(root, ".agents"))
		requireAbsent(t, filepath.Join(root, ".cursor"))
	})

	t.Run("should rewrite a file when its source content changes", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "onesie-gate")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on first run: %v", err)
		}

		path := filepath.Join(root, "skills", "onesie-gate", "SKILL.md")
		before := readFile(t, path)

		mustWriteFile(t, filepath.Join(root, "skills", "onesie-gate", "intro.md"), "This is the changed intro.")

		if err := skillgen.Generate(root); err != nil {
			t.Fatalf("unexpected error on second run: %v", err)
		}

		after := readFile(t, path)

		if after == before {
			t.Errorf("SKILL.md was not rewritten after its intro fragment changed")
		}

		if !strings.Contains(after, "This is the changed intro.") {
			t.Errorf("SKILL.md = %q, want it to carry the changed intro", after)
		}
	})
}

func writeFixtureSkill(t *testing.T, root, name string) {
	t.Helper()

	dir := filepath.Join(root, "skills", name)
	mustMkdirAll(t, filepath.Join(dir, "sections"))
	mustMkdirAll(t, filepath.Join(dir, "references"))

	mustWriteFile(t, filepath.Join(dir, "skill.json"), `{
		"name": "`+name+`",
		"description": "does a thing",
		"intro": "intro.md",
		"rules": [{"id": "r1", "short": "Do it.", "why": "because"}],
		"sections": ["sections/one.md"],
		"references": ["references/a.md"]
	}`)
	mustWriteFile(t, filepath.Join(dir, "intro.md"), "This is the intro.")
	mustWriteFile(t, filepath.Join(dir, "sections", "one.md"), "## One\n\nBody.")
	mustWriteFile(t, filepath.Join(dir, "references", "a.md"), "# A\n\nReference content.")
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(data)
}

func requireFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	if info.IsDir() {
		t.Fatalf("%s is a directory, want a file", path)
	}
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s exists, want it absent", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}
