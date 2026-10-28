package cli

// WithExprRuns calls runs as each --map or --id run starts, and the function it returns as it ends.
func WithExprRuns(runs func() (ended func())) RootOption {
	return func(s *rootSettings) {
		s.exprRuns = runs
	}
}
