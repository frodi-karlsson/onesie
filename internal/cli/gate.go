package cli

func requests(flags *runFlags) bool {
	// Inert today, since --print-questions returns before the no state check reaches this. It is
	// here for --print-request, which cannot take that early return: section 10 has it include
	// state when stdin or --state or --state-file supplied one, so it goes through input.Resolve
	// and then needs a question only body rather than a failure when nothing did.
	return !flags.printQuestions
}
