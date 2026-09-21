package cli

func requests(flags *runFlags) bool {
	// --print-request is the reason this is not folded into the early return --print-questions
	// takes. Section 10 has it include state when stdin or --state or --state-file supplied one, so
	// it goes through input.Resolve and then needs a question only body rather than a failure when
	// nothing did.
	return !flags.printQuestions && !flags.printRequest
}
