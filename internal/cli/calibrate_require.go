package cli

import (
	"fmt"
	"io"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func parseRequirements(texts []string) ([]calibrate.Requirement, error) {
	reqs := make([]calibrate.Requirement, 0, len(texts))

	for _, text := range texts {
		req, err := calibrate.ParseRequirement(text)
		if err != nil {
			return nil, fmt.Errorf("--require '%s' %w", text, err)
		}

		reqs = append(reqs, req)
	}

	return reqs, nil
}

func writeUnmet(w io.Writer, results []calibrate.Result) (bool, error) {
	unmet := false

	for _, result := range results {
		if result.Held {
			continue
		}

		unmet = true

		if err := output.Notice(w, result.String()); err != nil {
			return unmet, err
		}
	}

	return unmet, nil
}
