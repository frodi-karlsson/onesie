package runner

import (
	"context"
	"fmt"
)

func text(err error) string {
	if described, isReasoned := err.(reasoned); isReasoned {
		return described.Reason()
	}

	return err.Error()
}

type reasoned interface {
	Reason() string
}

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}

	if noun == "retry" {
		return fmt.Sprintf("%d retries", count)
	}

	return fmt.Sprintf("%d %ss", count, noun)
}

func interrupted(ctx context.Context) bool {
	return ctx.Err() != nil
}
