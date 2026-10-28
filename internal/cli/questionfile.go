package cli

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/frodi-karlsson/onesie/internal/patherr"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func readQuestionFile(settings rootSettings, value string) (data []byte, path, hides string, err error) {
	data, err = settings.readFile(value)
	if err == nil {
		return data, value, "", nil
	}

	if !qfile.IsName(value) || !lookupPast(settings, value, err) {
		return nil, "", "", fmt.Errorf("reading %s: %w", value, patherr.Inner(err))
	}

	env, err := findEnv(settings)
	if err != nil {
		return nil, "", "", err
	}

	source, err := qfile.Find(value, env)
	if err != nil {
		return nil, "", "", err
	}

	if source.BuiltIn != "" {
		builtIn, ok := settings.readBuiltIn(source.BuiltIn)
		if !ok {
			return nil, "", "", fmt.Errorf("the built in set %s could not be read", source.BuiltIn)
		}

		return builtIn, "built in " + source.BuiltIn, "", nil
	}

	data, err = settings.readFile(source.Path)
	if err != nil {
		return nil, "", "", fmt.Errorf("reading %s: %w", source.Path, patherr.Inner(err))
	}

	if _, ok := settings.readBuiltIn(value); ok {
		hides = value
	}

	return data, source.Path, hides, nil
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
		return qfile.FindEnv{}, fmt.Errorf("finding the working directory to look up a question file: %w", err)
	}

	return qfile.FindEnv{
		WorkDir:   workDir,
		ConfigDir: settings.configDir,
		ReadDir:   settings.readDir,
		Resolve:   settings.resolve,
		BuiltIns:  settings.builtIns,
		BuiltIn: func(name string) bool {
			_, ok := settings.readBuiltIn(name)

			return ok
		},
	}, nil
}

func loadQuestionFile(data []byte, path, hides string, alone bool) (*qfile.File, error) {
	loaded, err := qfile.Load(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	// A file with no question beside --ask only adds nothing, unless it hides the built in set the
	// user meant to add.
	if len(loaded.Questions) > 0 || (!alone && hides == "") {
		return loaded, nil
	}

	if hides != "" {
		return nil, fmt.Errorf("the question file %s holds no questions. Remove it, and -f %s takes "+
			"the built in set again", path, hides)
	}

	return nil, fmt.Errorf("the question file %s holds no questions", path)
}
