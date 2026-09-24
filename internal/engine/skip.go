package engine

// Skip drops the first n records of a source, which is how a resumed stream passes over the records
// an earlier run already answered.
func Skip[R any](source Source[R], n int) Source[R] {
	return &skipping[R]{source: source, left: n}
}

type skipping[R any] struct {
	source Source[R]
	left   int
}

func (s *skipping[R]) Next() (R, bool, error) {
	for s.left > 0 {
		s.left--

		if _, ok, err := s.source.Next(); err != nil || !ok {
			var zero R

			return zero, ok, err
		}
	}

	return s.source.Next()
}
