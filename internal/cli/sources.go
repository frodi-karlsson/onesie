package cli

import (
	"strings"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie/internal/qfile"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func mergeRun(
	settings rootSettings, flags runConfig, given *setting.Given, command settingrow.Command, loaded *qfile.File,
) (readSettings, error) {
	rows := settingrow.Table()
	src := setting.Sources{
		Given:     given,
		Parsed:    flags.merged,
		File:      fileValues(loaded),
		LookupEnv: settings.lookupEnv,
		Command:   command,
	}

	// A refused choice waits for the stage, so a flag clash beside it still reports first. The stage
	// reads every other setting meanwhile.
	chosen, refusal := settings.holder.chosen()
	if refusal != nil {
		src.Choice = &setting.Choice{}
		layers, err := setting.Read(withoutChoice(rows), src)
		if err != nil {
			return readSettings{}, err
		}

		merged, _ := setting.Merge(rows, layers, chosen)

		return readSettings{config: flags.withMerged(merged), refusal: refusal}, nil
	}

	run, err := loadedFor(settings, flags)
	if err != nil {
		return readSettings{}, err
	}

	chosen = run.chosen
	chosen.DefaultProfile = run.fallback
	src.Profile, src.Choice = run.values(), &chosen

	layers, err := setting.Read(rows, src)
	if err != nil {
		return readSettings{}, err
	}

	merged, notices := setting.Merge(rows, layers, chosen)
	config := flags.withMerged(merged)

	return readSettings{config: config, notices: announced(notices, setting.Get(config.merged, setting.PrintRequest) || config.mockPath != "")}, nil
}

type readSettings struct {
	config  runConfig
	notices []setting.Notice
	refusal error
}

func withoutChoice(rows []settingrow.Row) []settingrow.Row {
	var kept []settingrow.Row

	for _, row := range rows {
		if !choosesRun(row.Name) {
			kept = append(kept, row)
		}
	}

	return kept
}

func loadedFor(settings rootSettings, flags runConfig) (loadedRun, error) {
	if !setting.Get(flags.merged, setting.PrintQuestions) && !flags.comparing() {
		return settings.holder.loaded()
	}

	// --print-questions prints no provider, model or key, and --compare asks nothing, so neither loads
	// a profile or converts credentials.json. A provider choice still names the default it runs without.
	chosen, err := settings.holder.chosen()
	if err != nil || chosen.Provider == "" {
		return loadedRun{chosen: chosen}, err
	}

	return loadedRun{chosen: chosen, fallback: defaultName(settings)}, nil
}

func fileValues(loaded *qfile.File) *setting.FileValues {
	if loaded == nil {
		return nil
	}

	values := &setting.FileValues{Body: loaded.IsBody, TopLevel: map[string]any{}}

	for key, value := range map[string]string{
		"assert": loaded.Assert, "abstain_if": loaded.AbstainIf,
	} {
		if value != "" {
			values.TopLevel[key] = value
		}
	}

	if strings.TrimSpace(loaded.Model) != "" {
		values.TopLevel["model"] = loaded.Model
	}

	if loaded.LongContext {
		values.TopLevel["long_context"] = true
	}

	return values
}

func announced(notices []setting.Notice, sendsNothing bool) []setting.Notice {
	var kept []setting.Notice

	for _, notice := range notices {
		if sendsNothing && connects(notice.Setting) {
			continue
		}

		kept = append(kept, notice)
	}

	return kept
}

func connects(name settingrow.Name) bool {
	return name == setting.APIEndpoint.Name() || name == setting.APIKey.Name()
}

func choosesRun(name settingrow.Name) bool {
	return name == setting.Provider.Name() || name == setting.Profile.Name()
}
