package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkMergedGateCases(t *testing.T) {
	t.Helper()
	t.Parallel()
	data, err := os.ReadFile("testdata/gate-merge.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var tc gateMergeCase
		if err := json.Unmarshal([]byte(line), &tc); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			file, mock := filepath.Join(dir, "q.yaml"), filepath.Join(dir, "mock.json")
			for path, content := range map[string]string{file: tc.File, mock: tc.Mock} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for i, arg := range tc.Args {
				switch arg {
				case "{file}":
					tc.Args[i] = file
				case "{mock}":
					tc.Args[i] = mock
				}
			}
			out, errOut, code := runMocked(t, t.Context(), tc.Args, "", nil, nil)
			if code != tc.Want.Code || out != tc.Want.Out || errOut != tc.Want.Err {
				t.Errorf("exit = %d, stdout = %q, stderr = %q, want %d, %q, %q", code, out, errOut, tc.Want.Code, tc.Want.Out, tc.Want.Err)
			}
		})
	}
}

type gateMergeCase struct {
	Name string
	Args []string
	File string
	Mock string
	Want struct {
		Code int
		Out  string
		Err  string
	}
}
