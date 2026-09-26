package proseblocks_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/proseblocks"
)

func TestParseDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		diff    string
		want    []proseblocks.Change
		wantErr string
	}{
		{
			name: "should take the added lines of several hunks in one file",
			diff: "diff --git a/a.md b/a.md\nindex 1..2 100644\n--- a/a.md\n+++ b/a.md\n" +
				"@@ -3 +3,2 @@ heading\n-old\n+new\n+newer\n" +
				"@@ -10,0 +12 @@\n+added\n" +
				"@@ -20,2 +21,2 @@\n-a\n-b\n+c\n+d\n",
			want: []proseblocks.Change{{Path: "a.md", Added: []proseblocks.Lines{
				{First: 3, Last: 4}, {First: 12, Last: 12}, {First: 21, Last: 22},
			}}},
		},
		{
			name: "should leave out a file whose only hunk adds nothing",
			diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -4,2 +3,0 @@\n-gone\n-too\n" +
				"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-x\n+y\n",
			want: []proseblocks.Change{{Path: "b.go", Added: []proseblocks.Lines{{First: 1, Last: 1}}}},
		},
		{
			name: "should take every line of a new file",
			diff: "diff --git a/new.md b/new.md\nnew file mode 100644\nindex 0..1\n--- /dev/null\n+++ b/new.md\n" +
				"@@ -0,0 +1,3 @@\n+one\n+two\n+three\n",
			want: []proseblocks.Change{{Path: "new.md", Added: []proseblocks.Lines{{First: 1, Last: 3}}}},
		},
		{
			name: "should skip a deleted file",
			diff: "diff --git a/gone.md b/gone.md\ndeleted file mode 100644\n--- a/gone.md\n+++ /dev/null\n" +
				"@@ -1,2 +0,0 @@\n-one\n-two\n",
			want: nil,
		},
		{
			name: "should skip a rename with no added line",
			diff: "diff --git a/old.md b/new.md\nsimilarity index 100%\nrename from old.md\nrename to new.md\n" +
				"diff --git a/b.md b/b.md\n--- a/b.md\n+++ b/b.md\n@@ -0,0 +1 @@\n+x\n",
			want: []proseblocks.Change{{Path: "b.md", Added: []proseblocks.Lines{{First: 1, Last: 1}}}},
		},
		{
			name: "should read a path git quotes and a path with a space git ends with a tab",
			diff: "diff --git \"a/\\303\\251\\\"q.md\" \"b/\\303\\251\\\"q.md\"\n--- /dev/null\n+++ \"b/\\303\\251\\\"q.md\"\n" +
				"@@ -0,0 +1 @@\n+q\n" +
				"diff --git a/sp ace.md b/sp ace.md\n--- a/sp ace.md\t\n+++ b/sp ace.md\t\n@@ -1,0 +2 @@\n+z\n",
			want: []proseblocks.Change{
				{Path: "é\"q.md", Added: []proseblocks.Lines{{First: 1, Last: 1}}},
				{Path: "sp ace.md", Added: []proseblocks.Lines{{First: 2, Last: 2}}},
			},
		},
		{
			name: "should count context lines and read hunk lines that look like headers as content",
			diff: "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n" +
				"@@ -1,4 +1,4 @@\n keep\n--- a removed rule\n++++ an added rule\n\n keep\n\\ No newline at end of file\n",
			want: []proseblocks.Change{{Path: "a.md", Added: []proseblocks.Lines{{First: 2, Last: 2}}}},
		},
		{
			name:    "should fail on a hunk header that does not parse",
			diff:    "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n@@ -1 +x @@\n+y\n",
			wantErr: "line 4",
		},
		{
			name:    "should fail on a diff that ends inside a hunk",
			diff:    "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n@@ -1 +1,2 @@\n-x\n+y\n",
			wantErr: "ends inside a hunk",
		},
		{
			name:    "should fail on a hunk line its header does not allow",
			diff:    "diff --git a/a.md b/a.md\n--- a/a.md\n+++ b/a.md\n@@ -1 +1 @@\n+y\n+z\n",
			wantErr: "line 6",
		},
		{
			name:    "should fail on a new side path without the b/ prefix",
			diff:    "diff --git a/a.md b/a.md\n--- a/a.md\n+++ w/a.md\n@@ -1 +1 @@\n-x\n+y\n",
			wantErr: "b/ prefix",
		},
		{name: "should return nothing for an empty diff", diff: "", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := proseblocks.ParseDiff(strings.NewReader(tc.diff))

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}
