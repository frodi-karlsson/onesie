package calibrate

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

const defaultCutSteps = 20

// DefaultCuts returns the cuts a report uses when --cuts is not given, 0.05 to 0.95 in steps of
// 0.05, each built as k/20 so that 0.15 is exact.
func DefaultCuts() []float64 {
	cuts := make([]float64, 0, defaultCutSteps-1)
	for k := 1; k < defaultCutSteps; k++ {
		cuts = append(cuts, float64(k)/defaultCutSteps)
	}

	return cuts
}

// ParseCuts reads a --cuts list, whose items are numbers in [0,1]. The result is sorted and holds
// each value once.
func ParseCuts(text string) ([]float64, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("takes a comma separated list of numbers between 0 and 1, got nothing")
	}

	items := strings.Split(text, ",")
	cuts := make([]float64, 0, len(items))

	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("has an empty item in '%s'", text)
		}

		cut, err := strconv.ParseFloat(item, 64)
		if err != nil {
			return nil, fmt.Errorf("takes numbers between 0 and 1, got '%s'", item)
		}

		if math.IsNaN(cut) || cut < 0 || cut > 1 {
			return nil, fmt.Errorf("takes numbers between 0 and 1, got %s", item)
		}

		cuts = append(cuts, cut)
	}

	slices.Sort(cuts)

	return slices.Compact(cuts), nil
}
