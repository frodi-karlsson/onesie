package settings

import (
	"errors"
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
)

const requireName settingrow.Name = "require"

func calibrateRules() []Rule {
	return []Rule{
		calibrating("calibrate-reads-records", checkCalibrateInput),
		calibrating("offline-reads-out", checkOffline),
		calibrating("calibrate-needs-map", func(v View) error {
			if typed(v, Map.Name()) {
				return nil
			}

			return errors.New("calibrate needs --map. Without it the whole record, label included, " +
				"would be the state, and the report would flatter the question")
		}),
		calibrating("calibrate-report", checkReport),
		calibrating("cuts-parse", func(v View) error {
			return v.cutsError
		}),
		calibrating("calibrate-resumes-by-id", func(v View) error {
			// A dry run is left to the resume rule, whose message drops --resume rather than asking for
			// an --id it would then refuse as well.
			if Get(v.Merged, Resume) && !typed(v, ID.Name()) && !Get(v.Merged, PrintRequest) {
				return errors.New("calibrate resumes by id, since which records are asked follows the labels. Pass --id")
			}

			return nil
		}),
		calibrating("calibrate-dry-run-out", func(v View) error {
			// A resume is left to the resume rule, whose message names --resume as the flag with
			// nothing to do.
			if Get(v.Merged, PrintRequest) && Get(v.Merged, Out) != "" && !Get(v.Merged, Resume) {
				return errors.New("--print-request writes request bodies to stdout, " +
					"so --out has no answers to hold. Drop --out")
			}

			return nil
		}),
		calibrating("calibrate-usage", checkCalibrateUsage),
		calibrating("requires-parse", func(v View) error {
			return v.requirementsError
		}),
	}
}

func calibrating(id string, check func(View) error) Rule {
	return Rule{
		ID: id,
		Check: func(v View) error {
			if v.Facts.Command != settingrow.CommandCalibrate {
				return nil
			}

			return check(v)
		},
	}
}

func checkCalibrateInput(v View) error {
	if !typed(v, Input.Name()) {
		return errors.New("calibrate needs -i jsonl, csv or tsv, since only a record has fields to label")
	}

	switch mode := inputModeOf(v); mode {
	case input.JSONL, input.CSV, input.TSV:
		if typed(v, Delimiter.Name()) && mode != input.CSV {
			return fmt.Errorf("--delimiter applies to -i csv. -i %s %s", inputName(v), delimiterlessReason(mode))
		}

		return nil
	default:
		return fmt.Errorf("calibrate needs -i jsonl, csv or tsv. -i %s carries no field to label", inputName(v))
	}
}

func checkOffline(v View) error {
	switch {
	case !Get(v.Merged, Offline):
		return nil
	case mocked(v):
		return fmt.Errorf("--offline reads every answer from --out, and %s answers from a file of "+
			"its own. Drop one", mockSpelled(v))
	case Get(v.Merged, PrintRequest):
		return errors.New("--offline reads answers, and --print-request sends no request. Drop one")
	case Get(v.Merged, Out) == "":
		return errors.New("--offline reads every answer from --out. Pass --out FILE --resume")
	case !Get(v.Merged, Resume):
		return errors.New("--offline needs --resume, since it reads the answers --out already holds")
	case Get(v.Merged, Prune):
		return errors.New("--offline never rewrites --out, so --prune has nothing to drop. Drop one")
	default:
		return nil
	}
}

func checkReport(v View) error {
	switch report := Get(v.Merged, Output); report {
	case "", "auto", "table", "json":
	default:
		return fmt.Errorf("calibrate -o takes table, json or auto, got %s", report)
	}

	if Get(v.Merged, PrintRequest) && typed(v, Output.Name()) {
		name, reason := modeWording(settingrow.ModePrintRequest)

		return fmt.Errorf("-o does not apply to %s, which %s", name, reason)
	}

	return nil
}

func checkCalibrateUsage(v View) error {
	// A dry run is left to the dry run rule, whose message names --print-request as the reason.
	if Get(v.Merged, Usage) && !Get(v.Merged, PrintRequest) && Get(v.Merged, Out) == "" && Get(v.Merged, Output) != "json" {
		return errors.New("--usage adds token counts to the answers file or the json report, " +
			"and this run writes neither")
	}

	return nil
}

func calibrationCuts(v View) ([]float64, error) {
	if !typed(v, Cuts.Name()) {
		return calibrate.DefaultCuts(), nil
	}
	cuts, err := calibrate.ParseCuts(Get(v.Merged, Cuts))
	if err != nil {
		return nil, fmt.Errorf("--cuts %w", err)
	}
	return cuts, nil
}

func calibrationRequirements(v View) ([]calibrate.Requirement, error) {
	var requirements []calibrate.Requirement
	for _, text := range listOf(v, requireName) {
		req, err := calibrate.ParseRequirement(text)
		if err != nil {
			return nil, fmt.Errorf("--require '%s' %w", text, err)
		}
		requirements = append(requirements, req)
	}
	return requirements, nil
}
