package cli

func requests(flags *runFlags) bool {
	// --print-request is why this is not folded into the early return --print-questions takes.
	// Section 10 has it include any state supplied, so it goes through input.Resolve and needs a
	// question only body when nothing was.
	return !flags.printQuestions && !flags.printRequest
}
