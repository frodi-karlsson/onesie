package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "should write the blocks of the files on disk and skip a missing one",
			content: "// Package a answers every question in turn.\npackage a\n",
			want:    "{\"id\":\"FILE:1\",\"text\":\"Package a answers every question in turn.\"}\n",
		},
		{
			name:    "should write nothing for a file with no prose",
			content: "package a\n",
			want:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "a.go")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if err := run([]string{path, filepath.Join(t.TempDir(), "gone.md")}, osFiles{}, &out); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			quoted, err := json.Marshal(path)
			if err != nil {
				t.Fatal(err)
			}

			want := strings.ReplaceAll(tc.want, "FILE", strings.Trim(string(quoted), `"`))
			if got := out.String(); got != want {
				t.Errorf("wrote %q, want %q", got, want)
			}
		})
	}
}
