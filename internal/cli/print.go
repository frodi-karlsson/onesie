package cli

import (
	"io"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func printQuestions(w io.Writer, questions []plan.Question) error {
	out, err := qfile.Write(questions)
	if err != nil {
		return err
	}

	_, err = w.Write(out)

	return err
}
