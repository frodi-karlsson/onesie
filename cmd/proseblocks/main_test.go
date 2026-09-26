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
		args    []string
		content string
		hunks   string
		want    string
		wantErr string
	}{
		{
			name:    "should write the blocks on disk that an added line overlaps",
			content: "// Package a answers every question in turn.\npackage a\n\n// Stop ends the run at the next record.\nfunc Stop() {}\n",
			hunks:   "@@ -0,0 +1 @@\n+// Package a answers every question in turn.\n",
			want:    "{\"id\":\"FILE:1\",\"text\":\"Package a answers every question in turn.\"}\n",
		},
		{
			name:    "should write nothing when the added lines hold no prose",
			content: "// Package a answers every question in turn.\npackage a\n\nfunc Stop() {}\n",
			hunks:   "@@ -3,0 +4 @@\n+func Stop() {}\n",
			want:    "",
		},
		{
			name:    "should refuse arguments",
			args:    []string{"a.go"},
			wantErr: "takes no arguments",
		},
		{
			name:    "should fail on a diff that does not parse",
			hunks:   "@@ -1 +1,2 @@\n+x\n",
			wantErr: "ends inside a hunk",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "a.go")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			diff := "diff --git a" + path + " b" + path + "\n--- a" + path + "\n+++ b/" + path + "\n" + tc.hunks

			var out bytes.Buffer

			err := run(tc.args, strings.NewReader(diff), osFiles{}, &out)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
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
