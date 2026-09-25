//go:build !windows

package cli

import (
	"errors"
	"os"
)

func removeOwned(path string, owned bool, remove func(string) error) error {
	if !owned {
		return nil
	}

	err := remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}
