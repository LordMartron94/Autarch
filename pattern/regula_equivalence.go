package pattern

/*
PatternEquivalent reports whether two RegulaAST trees are structurally equivalent:
same expression kind, same structure, and same observations at leaves (literals and class ranges).
Binding state (literalSymIDs, classSymID, positionIDs, annotationID) is ignored.

Use cases:
- Detecting duplicate token patterns in a lexing ruleset
- Deduplicating or validating pattern sets before compilation
- Testing and regression checks

Time complexity: O(n + m) where n and m are the number of nodes in each tree
Space complexity: O(max depth) for recursion

Prerequisites:
- obsEqual must be a proper equality predicate (reflexive, symmetric, transitive)

Edge cases:
- Two nil pointers are considered equivalent
- Nil and non-nil are not equivalent
- Empty literal and empty literal are equivalent (epsilon)
- Class with empty ranges and class with empty ranges are equivalent
*/
func PatternEquivalent[TObservation any](
	a, b *RegulaAST[TObservation],
	obsEqual func(TObservation, TObservation) bool,
) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.kind != b.kind {
		return false
	}

	switch a.kind {
	case EXPRESSION_LITERAL:
		if len(a.literals) != len(b.literals) {
			return false
		}
		for i := range a.literals {
			if !obsEqual(a.literals[i], b.literals[i]) {
				return false
			}
		}
		return true

	case EXPRESSION_CLASS:
		return charClassEquivalent(a.class, b.class, obsEqual)

	case EXPRESSION_CONCAT, EXPRESSION_UNION:
		return PatternEquivalent(a.left, b.left, obsEqual) &&
			PatternEquivalent(a.right, b.right, obsEqual)

	case EXPRESSION_REPEAT:
		return a.min == b.min &&
			a.max == b.max &&
			PatternEquivalent(a.sub, b.sub, obsEqual)

	case EXPRESSION_CAPTURE:
		return PatternEquivalent(a.sub, b.sub, obsEqual)

	default:
		return false
	}
}

func charClassEquivalent[TObservation any](
	a, b charClass[TObservation],
	obsEqual func(TObservation, TObservation) bool,
) bool {
	if len(a.ranges) != len(b.ranges) {
		return false
	}
	for i := range a.ranges {
		if !obsEqual(a.ranges[i].Lo, b.ranges[i].Lo) ||
			!obsEqual(a.ranges[i].Hi, b.ranges[i].Hi) {
			return false
		}
	}
	return true
}
