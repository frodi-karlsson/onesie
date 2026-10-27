package calibrate

import (
	"math"

	"github.com/frodi-karlsson/onesie/internal/num"
)

const z95 = 1.959963984540054

// Wilson returns hits of of as a share with its 95 percent Wilson score interval, each rounded to
// four places and never to 0 or 1 unless it is exactly that. A share of nothing is not Defined.
func Wilson(hits, of int) Share {
	if of <= 0 {
		return Share{Hits: hits, Of: of}
	}

	n := float64(of)
	rate := float64(hits) / n
	z2 := z95 * z95
	center := (rate + z2/(2*n)) / (1 + z2/n)
	half := z95 / (1 + z2/n) * math.Sqrt(rate*(1-rate)/n+z2/(4*n*n))

	low, high := math.Max(0, center-half), math.Min(1, center+half)
	if hits == 0 {
		low = 0
	}

	if hits == of {
		high = 1
	}

	return Share{
		Hits: hits, Of: of, Rate: roundedInside(rate), Low: roundedInside(low), High: roundedInside(high),
		Defined: true,
	}
}

// Share is hits out of of, with the rate and its 95 percent Wilson interval. Defined is false when
// of is zero, and the rate and interval are then zero.
type Share struct {
	Hits, Of        int
	Rate, Low, High float64
	Defined         bool
}

func roundedInside(fraction float64) float64 {
	// A share that is not all or none must not print as one, or 29999 of 30000 would read as every
	// record and pass a requirement of 1.
	rounded := num.Rounded(fraction)

	switch {
	case fraction > 0 && rounded <= 0:
		return 0.0001
	case fraction < 1 && rounded >= 1:
		return 0.9999
	default:
		return rounded
	}
}
