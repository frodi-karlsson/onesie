package cli

import (
	"io"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func printQuestions(w io.Writer, built *plan.Plan) error {
	out, err := qfile.Write(built.Questions)
	if err != nil {
		return err
	}

	_, err = w.Write(out)

	return err
}
