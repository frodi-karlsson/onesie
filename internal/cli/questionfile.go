package cli

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func readQuestionFile(settings rootSettings, value string) ([]byte, string, error) {
	data, err := settings.readFile(value)
	if err == nil {
		return data, value, nil
	}

	if !qfile.IsName(value) || !lookupPast(settings, value, err) {
		return nil, "", fmt.Errorf("onesie: reading %s: %w", value, err)
	}

	env, err := findEnv(settings)
	if err != nil {
		return nil, "", err
	}

	path, err := qfile.Find(value, env)
	if err != nil {
		return nil, "", err
	}

	data, err = settings.readFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("onesie: reading %s: %w", path, err)
	}

	return data, path, nil
}

func lookupPast(settings rootSettings, value string, readErr error) bool {
	if errors.Is(readErr, fs.ErrNotExist) {
		// A symlink whose target is gone reads as not found, but the user named a real entry.
		_, linkErr := settings.readlink(value)

		return linkErr != nil
	}

	info, err := settings.stat(value)

	return err == nil && info.IsDir()
}

func findEnv(settings rootSettings) (qfile.FindEnv, error) {
	workDir, err := settings.getwd()
	if err != nil {
		return qfile.FindEnv{}, fmt.Errorf("onesie: finding the working directory to look up a question file: %w", err)
	}

	return qfile.FindEnv{
		WorkDir:   workDir,
		ConfigDir: settings.configDir,
		ReadDir:   settings.readDir,
		Resolve:   settings.resolve,
	}, nil
}
