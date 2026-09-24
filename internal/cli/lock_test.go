package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLockAnswers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		existing   bool
		readOnly   bool
		wantLocked bool
	}{
		{
			name:       "should refuse a second lock while the first is held",
			wantLocked: true,
		},
		{
			name:       "should lock the answers file itself when the directory refuses the lock file",
			existing:   true,
			readOnly:   true,
			wantLocked: true,
		},
		{
			name:     "should lock nothing when the directory refuses the lock file and there is no answers file",
			readOnly: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.readOnly && runtime.GOOS == "windows" {
				t.Skip("a read only directory on windows still lets a file be created in it")
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")

			if tc.existing {
				if err := os.WriteFile(path, []byte("answer\n"), 0o600); err != nil {
					t.Fatalf("writing the answers file: %v", err)
				}
			}

			if tc.readOnly {
				lockDir(t, dir)
			}

			release, err := lockAnswers(path)
			if err != nil {
				t.Fatalf("first lock: %v", err)
			}

			_, secondErr := lockAnswers(path)
			if errors.Is(secondErr, errLocked) != tc.wantLocked {
				t.Fatalf("second lock error = %v, want locked %v", secondErr, tc.wantLocked)
			}

			if releaseErr := release(); releaseErr != nil {
				t.Fatalf("release: %v", releaseErr)
			}

			again, err := lockAnswers(path)
			if err != nil {
				t.Fatalf("lock after the release: %v", err)
			}

			if err := again(); err != nil {
				t.Fatalf("second release: %v", err)
			}

			if _, statErr := os.Stat(path + lockSuffix); !os.IsNotExist(statErr) {
				t.Errorf("lock file left behind: %v", statErr)
			}
		})
	}
}
