// Package proseblocks finds the pieces of prose a change touches in Go and Markdown files: one block
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

// Extract returns the prose blocks of every changed .go and .md file that hold an added line, sorted
// by path and then by line. It skips a missing file and a path of any other kind.
func Extract(files FileReader, changes []Change) ([]Block, error) {
	sorted := slices.Clone(changes)
	slices.SortStableFunc(sorted, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })

	var blocks []Block

	for _, change := range sorted {
		found, err := fileBlocks(files, change.Path)
		if err != nil {
			return nil, err
		}

		for _, block := range found {
			if block.overlaps(change.Added) {
				blocks = append(blocks, block.Block)
			}
		}
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

func fileBlocks(files FileReader, path string) ([]span, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".go" && ext != ".md" {
		return nil, nil
	}

	src, err := files.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if ext == ".md" {
		return markdownBlocks(path, src), nil
	}

	return goBlocks(path, src)
}

type span struct {
	Block

	first, last int
}

func (s span) overlaps(added []Lines) bool {
	return slices.ContainsFunc(added, func(lines Lines) bool {
		return lines.First <= s.last && s.first <= lines.Last
	})
}

func newBlock(path string, first, last int, text string) (span, bool) {
	text = strings.Join(strings.Fields(text), " ")
	if len(strings.Fields(text)) < minWords {
		return span{}, false
	}

	return span{Block: Block{ID: fmt.Sprintf("%s:%d", path, first), Text: text}, first: first, last: last}, true
}
