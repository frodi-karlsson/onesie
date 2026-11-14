package cli

import (
	"io"

	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/wording"
)

const maxCauses = 5

type causeList struct {
	w      io.Writer
	listed int
	more   int
}

func (c *causeList) name(name, message string) error {
	if c.listed == maxCauses {
		c.more++

		return nil
	}

	c.listed++

	return output.Notice(c.w, name+": "+message)
}

func (c *causeList) always(name, message string) error {
	return output.Notice(c.w, name+": "+message)
}

func (c *causeList) finish() error {
	if c.more == 0 {
		return nil
	}

	return output.Notice(c.w, "and "+wording.Plural(c.more, "more error", "more errors"))
}
