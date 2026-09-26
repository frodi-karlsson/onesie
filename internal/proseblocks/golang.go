package proseblocks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

var licenseWords = regexp.MustCompile(`(?i)copyright|license`)

func goBlocks(path string, src []byte) ([]Block, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	var blocks []Block

	for _, group := range file.Comments {
		text := commentText(group)
		if group.End() < file.Package && licenseWords.MatchString(text) {
			continue
		}

		if block, ok := newBlock(path, fset.Position(group.Pos()).Line, text); ok {
			blocks = append(blocks, block)
		}
	}

	return blocks, nil
}

func commentText(group *ast.CommentGroup) string {
	kept := &ast.CommentGroup{}

	for _, comment := range group.List {
		// Text drops a directive such as //go:build or //nolint:errcheck, but not a bare //nolint,
		// which has no colon.
		if !isNolint(comment.Text) {
			kept.List = append(kept.List, comment)
		}
	}

	if len(kept.List) == 0 {
		return ""
	}

	return kept.Text()
}

func isNolint(comment string) bool {
	body, found := strings.CutPrefix(comment, "//")

	return found && strings.HasPrefix(strings.TrimSpace(body), "nolint")
}
