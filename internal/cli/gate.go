package cli

func requests(flags *runFlags) bool {
	// The dry run paths need no key and no state, and a check that fires for them turns a flag
	// whose whole point is working offline into one that does not. The key itself is required by
	// jev.New, which only the requesting paths reach, so nothing about it is decided here.
	return !flags.printQuestions
}
