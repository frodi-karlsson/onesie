//go:build !windows

package cli

import (
	"errors"
	"os"
)

func removeOwned(path string, owned bool) error {
	if !owned {
		return nil
	}

	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}
