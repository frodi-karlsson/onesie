// Package patherr strips the operation and path an *fs.PathError carries, for a message that names
// the path itself.
package patherr

import (
	"errors"
	"io/fs"
)

// Inner returns the error inside a *fs.PathError, and any other error as it is.
func Inner(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}

	return err
}
