// Package num writes and rounds the numbers onesie prints, so every output mode shows the same one.
package num

import (
	"encoding/json"
	"math"
	"strconv"
)

// Format writes a number as -o json does, in the shortest form that reads back as the same number
// and never rounded, so every output mode shows the number a gate compares.
func Format(value float64) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return strconv.FormatFloat(value, 'g', -1, 64)
	}

	return string(encoded)
}

// Rounded rounds a number onesie computes itself to four places, so a gate, -o json and the table
// all see the same value rather than float noise.
func Rounded(value float64) float64 {
	return math.Round(value*1e4) / 1e4
}
