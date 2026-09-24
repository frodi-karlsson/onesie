package assert

import "slices"

func fold(name string) (func([]float64) float64, bool) {
	known := functions()

	at := slices.IndexFunc(known, func(f function) bool {
		return f.name == name
	})
	if at < 0 {
		return nil, false
	}

	return known[at].apply, true
}

func functionNames() []string {
	known := functions()

	names := make([]string, 0, len(known))
	for _, f := range known {
		names = append(names, f.name)
	}

	return names
}

func functions() []function {
	return []function{
		{name: "avg", apply: mean},
		{name: "max", apply: slices.Max[[]float64]},
		{name: "min", apply: slices.Min[[]float64]},
		{name: "sum", apply: total},
	}
}

type function struct {
	name  string
	apply func([]float64) float64
}

func mean(numbers []float64) float64 {
	return total(numbers) / float64(len(numbers))
}

func total(numbers []float64) float64 {
	var sum float64
	for _, number := range numbers {
		sum += number
	}

	return sum
}
