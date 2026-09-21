package engine

import (
	"context"
	"errors"
)

func runUnordered[T any](context.Context, Config[T]) (Result, error) {
	return Result{}, errors.New("jev: --unordered is not available yet")
}
