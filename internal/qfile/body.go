package qfile

import (
	"errors"

	"github.com/goccy/go-yaml"
)

func loadBody(yaml.MapSlice) (*File, error) {
	return nil, errors.New("jev: a request body through -f is not available yet")
}
