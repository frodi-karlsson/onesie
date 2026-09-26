// Package proseblocks splits Go and Markdown files into the pieces of prose a reader sees: one block
// per comment group, paragraph or list item.
package proseblocks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// A block with fewer words than this is a label or a fragment, and says nothing about history.
const minWords = 4

// Extract returns the prose blocks of every .go and .md path, sorted by path and then by line. It
// skips a missing file and a path of any other kind.
func Extract(files FileReader, paths []string) ([]Block, error) {
	sorted := slices.Clone(paths)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	var blocks []Block

	for _, path := range sorted {
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".go" && ext != ".md" {
			continue
		}

		src, err := files.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, err
		}

		if ext == ".md" {
			blocks = append(blocks, markdownBlocks(path, src)...)

			continue
		}

		found, err := goBlocks(path, src)
		if err != nil {
			return nil, err
		}

		blocks = append(blocks, found...)
	}

	return blocks, nil
}

// Block is one piece of prose. ID is the file path and the line the block starts on.
type Block struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// FileReader reads a whole file. A missing file reports an error that wraps fs.ErrNotExist.
type FileReader interface {
	ReadFile(name string) ([]byte, error)
}

// Write writes each block as one JSON line.
func Write(w io.Writer, blocks []Block) error {
	encoder := json.NewEncoder(w)
	// The text goes to a model and to a summary a person reads, so < and > stay as they are.
	encoder.SetEscapeHTML(false)

	for _, block := range blocks {
		if err := encoder.Encode(block); err != nil {
			return err
		}
	}

	return nil
}

func newBlock(path string, line int, text string) (Block, bool) {
	text = strings.Join(strings.Fields(text), " ")
	if len(strings.Fields(text)) < minWords {
		return Block{}, false
	}

	return Block{ID: fmt.Sprintf("%s:%d", path, line), Text: text}, true
}
