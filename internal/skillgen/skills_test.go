package skillgen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/skillgen"
)

func TestSkills(t *testing.T) {
	t.Parallel()

	t.Run("should read every skill in directory order", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "aaa-first")
		writeFixtureSkill(t, root, "zzz-second")

		found, err := skillgen.Skills(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(found) != 2 {
			t.Fatalf("len(found) = %d, want 2", len(found))
		}

		if found[0].Dir != "aaa-first" || found[1].Dir != "zzz-second" {
			t.Errorf("found = %+v, want aaa-first then zzz-second", found)
		}

		if found[0].Skill.Name != "aaa-first" || found[1].Skill.Name != "zzz-second" {
			t.Errorf("found = %+v, want each Skill loaded and named after its directory", found)
		}
	})

	t.Run("should return no skills when skills does not exist", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()

		found, err := skillgen.Skills(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(found) != 0 {
			t.Errorf("len(found) = %d, want 0", len(found))
		}
	})

	t.Run("should return no skills when skills exists and is empty", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		mustMkdirAll(t, filepath.Join(root, "skills"))

		found, err := skillgen.Skills(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(found) != 0 {
			t.Errorf("len(found) = %d, want 0", len(found))
		}
	})

	t.Run("should skip a directory carrying no skill.json", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "has-a-skill")
		mustMkdirAll(t, filepath.Join(root, "skills", "no-skill-json"))

		found, err := skillgen.Skills(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(found) != 1 || found[0].Dir != "has-a-skill" {
			t.Errorf("found = %+v, want only has-a-skill", found)
		}
	})

	t.Run("should return an error naming the skill whose skill.json fails validation", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		dir := filepath.Join(root, "skills", "broken")
		mustMkdirAll(t, dir)
		mustWriteFile(t, filepath.Join(dir, "skill.json"), `{"name": "broken"}`)

		_, err := skillgen.Skills(root)
		if err == nil {
			t.Fatalf("expected an error, got none")
		}

		if !strings.Contains(err.Error(), "broken") {
			t.Errorf("error = %q, want it to name the skill broken", err.Error())
		}
	})

	t.Run("should ignore a regular file directly inside skills", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeFixtureSkill(t, root, "has-a-skill")
		mustWriteFile(t, filepath.Join(root, "skills", "README.md"), "not a skill")

		found, err := skillgen.Skills(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(found) != 1 || found[0].Dir != "has-a-skill" {
			t.Errorf("found = %+v, want only has-a-skill", found)
		}
	})
}
