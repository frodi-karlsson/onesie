package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/patherr"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func readQuestionFile(settings rootSettings, value string) ([]byte, string, error) {
	data, err := settings.readFile(value)
	if err == nil {
		return data, value, refuseEmptyFile(data, value, "")
	}

	if !qfile.IsName(value) || !lookupPast(settings, value, err) {
		return nil, "", fmt.Errorf("onesie: reading %s: %w", value, patherr.Inner(err))
	}

	env, err := findEnv(settings)
	if err != nil {
		return nil, "", err
	}

	source, err := qfile.Find(value, env)
	if err != nil {
		return nil, "", err
	}

	if source.BuiltIn != "" {
		builtIn, ok := settings.readBuiltIn(source.BuiltIn)
		if !ok {
			return nil, "", fmt.Errorf("onesie: the built-in set %s could not be read", source.BuiltIn)
		}

		return builtIn, "built-in " + source.BuiltIn, nil
	}

	path := source.Path

	data, err = settings.readFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("onesie: reading %s: %w", path, patherr.Inner(err))
	}

	hidden := ""
	if settings.readBuiltIn != nil {
		if _, ok := settings.readBuiltIn(value); ok {
			hidden = value
		}
	}

	return data, path, refuseEmptyFile(data, path, hidden)
}

func refuseEmptyFile(data []byte, path, hiddenBuiltIn string) error {
	if len(bytes.TrimSpace(data)) > 0 {
		return nil
	}

	if hiddenBuiltIn != "" {
		return fmt.Errorf("onesie: the question file %s is empty. Remove it, and -f %s takes the built-in set again",
			path, hiddenBuiltIn)
	}

	return fmt.Errorf("onesie: the question file %s is empty", path)
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

	env := qfile.FindEnv{
		WorkDir:   workDir,
		ConfigDir: settings.configDir,
		ReadDir:   settings.readDir,
		Resolve:   settings.resolve,
		BuiltIns:  settings.builtIns,
	}

	if settings.readBuiltIn != nil {
		env.BuiltIn = func(name string) bool {
			_, ok := settings.readBuiltIn(name)

			return ok
		}
	}

	return env, nil
}

func loadQuestionFile(data []byte, path string, alone bool) (*qfile.File, error) {
	loaded, err := qfile.Load(data)
	if err != nil {
		// A file of only comments reads as null, which holds no more than an empty mapping does.
		decoded, decodeErr := qfile.DecodeOrdered(data)
		if decodeErr != nil || decoded != nil {
			return nil, fmt.Errorf("onesie: %s: %s", path, strings.TrimPrefix(err.Error(), "onesie: "))
		}

		loaded = &qfile.File{}
	}

	// An empty file beside --ask only adds nothing, which is fine. Alone it is the whole run, so
	// the file is what to name.
	if alone && len(loaded.Questions) == 0 {
		return nil, noQuestionsIn(path)
	}

	return loaded, nil
}

func noQuestionsIn(path string) error {
	return fmt.Errorf("onesie: the question file %s holds no questions", path)
}
