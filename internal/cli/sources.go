package cli

import (
	"strings"

	"github.com/frodi-karlsson/onesie/internal/qfile"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func mergeRun(
	settings rootSettings, flags runConfig, given *setting.Given, command setting.Command, loaded *qfile.File,
) (readSettings, error) {
	rows := setting.Table()
	src := setting.Sources{
		Given:     given,
		File:      fileValues(loaded),
		LookupEnv: settings.lookupEnv,
		Facts:     setting.Facts{Command: command, Events: flags.events},
	}

	// A refused choice waits for the stage, so a flag clash beside it still reports first. The stage
	// reads every other setting meanwhile.
	if _, refusal := setting.Choose(rows, src); refusal != nil {
		layers, err := setting.Read(withoutChoice(rows), src)
		if err != nil {
			return readSettings{}, err
		}

		merged, _ := setting.Merge(rows, layers, nil)

		return readSettings{config: flags.withMerged(merged), refusal: refusal}, nil
	}

	run, err := loadedFor(settings, flags)
	if err != nil {
		return readSettings{}, err
	}

	src.Profile, src.DefaultProfile = run.values(), run.fallback

	layers, err := setting.Read(rows, src)
	if err != nil {
		return readSettings{}, err
	}

	merged, notices := setting.Merge(rows, layers, nil)

	return readSettings{config: flags.withMerged(merged), notices: announced(notices)}, nil
}

type readSettings struct {
	config  runConfig
	notices []setting.Notice
	refusal error
}

func withoutChoice(rows []setting.Row) []setting.Row {
	var kept []setting.Row

	for _, row := range rows {
		if !choosesRun(row.Name) {
			kept = append(kept, row)
		}
	}

	return kept
}

func loadedFor(settings rootSettings, flags runConfig) (loadedRun, error) {
	// --print-questions prints no provider, model or key, so it reads no profile and converts no
	// credentials.json.
	if flags.printQuestions {
		return loadedRun{}, nil
	}

	return settings.holder.loaded()
}

func fileValues(loaded *qfile.File) *setting.FileValues {
	if loaded == nil {
		return nil
	}

	values := &setting.FileValues{Body: loaded.IsBody, TopLevel: map[string]any{}}

	for key, value := range map[string]string{
		"assert": loaded.Assert, "abstain_if": loaded.AbstainIf, "model": loaded.Model,
	} {
		if strings.TrimSpace(value) != "" {
			values.TopLevel[key] = value
		}
	}

	if loaded.LongContext {
		values.TopLevel["long_context"] = true
	}

	return values
}

func announced(notices []setting.Notice) []setting.Notice {
	var kept []setting.Notice

	for _, notice := range notices {
		if row, found := setting.Lookup(notice.Setting); found && (row.Flag.Repeat || choosesRun(row.Name)) {
			continue
		}

		kept = append(kept, notice)
	}

	return kept
}

func choosesRun(name setting.Name) bool {
	return name == setting.Provider.Name() || name == setting.Profile.Name()
}
