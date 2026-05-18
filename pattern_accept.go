package autarch

/*
PatternAcceptOutcome is a minimal accept/reject payload for language-theoretic DFA operations
(intersection, prefix continuation). Clients interpret Accept == true as membership in the language.
*/
type PatternAcceptOutcome struct {
	Accept bool
}

/* PatternAcceptOutcomeIsAccept reports whether the outcome denotes acceptance. */
func PatternAcceptOutcomeIsAccept(out PatternAcceptOutcome) bool {
	return out.Accept
}
