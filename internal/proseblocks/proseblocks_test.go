package proseblocks_test

import (
	"bytes"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/proseblocks"
)

var errDenied = errors.New("permission denied")

func TestExtract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		files   map[string]string
		paths   []string
		want    []proseblocks.Block
		wantErr string
	}{
		{
			name: "should emit each comment group once with its markers stripped",
			files: map[string]string{"a.go": "package a\n\n" +
				"// Run asks every question once\n// and prints the answers.\nfunc Run() {}\n\n" +
				"/* The cache keeps answers\n   on disk between runs. */\nvar x = 1\n"},
			paths: []string{"a.go"},
			want: []proseblocks.Block{
				{ID: "a.go:3", Text: "Run asks every question once and prints the answers."},
				{ID: "a.go:7", Text: "The cache keeps answers on disk between runs."},
			},
		},
		{
			name: "should skip directives, lint markers and short comments",
			files: map[string]string{"a.go": "//go:build linux\n\npackage a\n\n" +
				"//go:generate go run ./gen\n\n" +
				"//nolint\nvar a = 1\n\n" +
				"var b = 1 //nolint:errcheck // the close error cannot happen here\n\n" +
				"// Too short.\nvar c = 1\n\n" +
				"// A retry waits a second before it asks again.\n//nolint:gocritic\nvar d = 1\n"},
			paths: []string{"a.go"},
			want:  []proseblocks.Block{{ID: "a.go:15", Text: "A retry waits a second before it asks again."}},
		},
		{
			name: "should skip a license header but keep the package doc",
			files: map[string]string{"a.go": "// Copyright 2026 The Authors. Use of this source code is governed by a license.\n\n" +
				"// Package a answers every question in turn.\npackage a\n"},
			paths: []string{"a.go"},
			want:  []proseblocks.Block{{ID: "a.go:3", Text: "Package a answers every question in turn."}},
		},
		{
			name:    "should fail on a Go file that does not parse",
			files:   map[string]string{"a.go": "package a\n\nfunc {\n"},
			paths:   []string{"a.go"},
			wantErr: "a.go:3",
		},
		{
			name: "should emit paragraphs and list items as separate blocks",
			files: map[string]string{"a.md": "The cache answers a repeated\nrequest from disk.\n\n" +
				"- A hit sends nothing to the API.\n- A miss asks the API\n  and stores the answer.\n" +
				"1. Run the check before you push.\n"},
			paths: []string{"a.md"},
			want: []proseblocks.Block{
				{ID: "a.md:1", Text: "The cache answers a repeated request from disk."},
				{ID: "a.md:4", Text: "A hit sends nothing to the API."},
				{ID: "a.md:5", Text: "A miss asks the API and stores the answer."},
				{ID: "a.md:7", Text: "Run the check before you push."},
			},
		},
		{
			name: "should skip fenced code, tables, headings and HTML comments",
			files: map[string]string{"a.md": "# The heading of the page\n\n" +
				"```sh\nonesie --cache answers a repeated request\n```\n\n" +
				"~~~\nthis is code in a tilde fence\n~~~\n\n" +
				"| Code | Meaning of the code |\n|---|---|\n| 0 | the run answered every record |\n\n" +
				"Code | Meaning of the code\n--- | ---\n0 | the run answered every record\n\n" +
				"<!-- a comment that spans\nmore than one line of the file -->\n\n" +
				"A setext heading with several words\n===\n\n" +
				"Retries twice by default. <!-- an inline note -->\n"},
			paths: []string{"a.md"},
			want:  []proseblocks.Block{{ID: "a.md:25", Text: "Retries twice by default."}},
		},
		{
			name:  "should skip front matter",
			files: map[string]string{"a.md": "---\nname: a skill with a long description\n---\n\nThe skill runs a gate on every record.\n"},
			paths: []string{"a.md"},
			want:  []proseblocks.Block{{ID: "a.md:5", Text: "The skill runs a gate on every record."}},
		},
		{
			name: "should keep an indented paragraph under a list item and skip an indented code block",
			files: map[string]string{"a.md": "1. Create the key in the account.\n\n" +
				"   Note its issuer ID and key ID now.\n\n" +
				"Some prose between the two parts.\n\n" +
				"    go run ./cmd/formulagen -version 1\n"},
			paths: []string{"a.md"},
			want: []proseblocks.Block{
				{ID: "a.md:1", Text: "Create the key in the account."},
				{ID: "a.md:3", Text: "Note its issuer ID and key ID now."},
				{ID: "a.md:5", Text: "Some prose between the two parts."},
			},
		},
		{
			name: "should skip missing files and other kinds, and sort by path",
			files: map[string]string{
				"b.md":  "The second file has a paragraph.\n",
				"a.md":  "The first file has a paragraph.\n",
				"c.txt": "A text file is not prose this reads.\n",
			},
			paths: []string{"b.md", "gone.go", "a.md", "c.txt", "b.md", "gone.md"},
			want: []proseblocks.Block{
				{ID: "a.md:1", Text: "The first file has a paragraph."},
				{ID: "b.md:1", Text: "The second file has a paragraph."},
			},
		},
		{
			name:    "should fail on a file it cannot read",
			files:   map[string]string{},
			paths:   []string{"locked.md"},
			wantErr: "permission denied",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := proseblocks.Extract(fakeFiles(tc.files), tc.paths)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		blocks []proseblocks.Block
		want   string
	}{
		{
			name:   "should write one JSON line per block without escaping angle brackets",
			blocks: []proseblocks.Block{{ID: "a.md:1", Text: "Pipe <file> to it"}, {ID: "b.go:2", Text: `a "quoted" word`}},
			want:   "{\"id\":\"a.md:1\",\"text\":\"Pipe <file> to it\"}\n{\"id\":\"b.go:2\",\"text\":\"a \\\"quoted\\\" word\"}\n",
		},
		{name: "should write nothing for no blocks", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			if err := proseblocks.Write(&out, tc.blocks); err != nil {
				t.Fatal(err)
			}

			if out.String() != tc.want {
				t.Errorf("wrote %q, want %q", out.String(), tc.want)
			}
		})
	}
}

type fakeFiles map[string]string

func (f fakeFiles) ReadFile(name string) ([]byte, error) {
	if name == "locked.md" {
		return nil, errDenied
	}

	content, found := f[name]
	if !found {
		return nil, fs.ErrNotExist
	}

	return []byte(content), nil
}
