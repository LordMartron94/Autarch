package regex

import "autarch"

// Thompson ε-NFA fragment.
type nfaFragment struct {
	start       uint64
	accept      uint64
	transitions []autarch.Transition[rune]
}

// Helper to create ε transition
func epsilonTransition(from, to uint64) autarch.Transition[rune] {
	return autarch.Transition[rune]{
		Symbol:       autarch.EpsilonSymbolCreate[rune](),
		CurrentState: from,
		NextState:    to,
	}
}

// Helper to create literal transition
func literalTransition(from, to uint64, r rune) autarch.Transition[rune] {
	return autarch.Transition[rune]{
		Symbol:       autarch.SymbolCreate[rune](string(r), 0), // final symbolID fixed later
		CurrentState: from,
		NextState:    to,
	}
}

// ------------------------------------------------------------
// THOMPSON BUILDER (MAIN ENTRYPOINT)
// ------------------------------------------------------------
//
// stateCounter is a uint64 counter that increments for each new state.
// The caller provides:
//      var counter uint64 = 0
// and the final number of states is counter.
//
// ------------------------------------------------------------

func thompsonBuild(ast *regexAST, stateCounter *uint64) nfaFragment {
	switch ast.kind {

	case astLiteral:
		return buildLiteral(ast, stateCounter)

	case astCharClass:
		return buildCharClass(ast, stateCounter)

	case astConcat:
		return buildConcat(ast, stateCounter)

	case astAlt:
		return buildAlt(ast, stateCounter)

	case astStar:
		return buildStar(ast, stateCounter)

	case astPlus:
		return buildPlus(ast, stateCounter)

	case astQuestion:
		return buildQuestion(ast, stateCounter)

	default:
		panic("unknown AST kind in thompsonBuild")
	}
}

// ------------------------------------------------------------
// LITERAL
// ------------------------------------------------------------

func buildLiteral(ast *regexAST, c *uint64) nfaFragment {
	s := newState(c)
	t := newState(c)

	return nfaFragment{
		start:  s,
		accept: t,
		transitions: []autarch.Transition[rune]{
			literalTransition(s, t, ast.value),
		},
	}
}

// ------------------------------------------------------------
// CHARACTER CLASS
// ------------------------------------------------------------
//
// A character class [abc] is compiled as a branching structure:
//
//      (s)
//       |-- 'a' --> (t)
//       |-- 'b' --> (t)
//       |-- 'c' --> (t)
//
// ------------------------------------------------------------

func buildCharClass(ast *regexAST, c *uint64) nfaFragment {
	s := newState(c)
	t := newState(c)

	trans := make([]autarch.Transition[rune], 0, len(ast.class))
	for _, r := range ast.class {
		trans = append(trans, literalTransition(s, t, r))
	}

	return nfaFragment{
		start:       s,
		accept:      t,
		transitions: trans,
	}
}

// ------------------------------------------------------------
// CONCATENATION (A B)
//
// A: (sA) --> (tA)
// B: (sB) --> (tB)
//
// Result:
//
//   (sA) --> (tA) --ε--> (sB) --> (tB)
//
// ------------------------------------------------------------

func buildConcat(ast *regexAST, c *uint64) nfaFragment {
	left := thompsonBuild(ast.left, c)
	right := thompsonBuild(ast.right, c)

	trans := append(left.transitions,
		append([]autarch.Transition[rune]{epsilonTransition(left.accept, right.start)},
			right.transitions...)...)

	return nfaFragment{
		start:       left.start,
		accept:      right.accept,
		transitions: trans,
	}
}

// ------------------------------------------------------------
// ALTERNATION (A | B)
//
//        ε             ε
//    --> (A) -------> (accept)
//   /                ^
// (start)            |
//   \                |
//    --> (B) -------> ε
//
// ------------------------------------------------------------

func buildAlt(ast *regexAST, c *uint64) nfaFragment {
	left := thompsonBuild(ast.left, c)
	right := thompsonBuild(ast.right, c)

	s := newState(c)
	t := newState(c)

	trans := []autarch.Transition[rune]{
		epsilonTransition(s, left.start),
		epsilonTransition(s, right.start),
		epsilonTransition(left.accept, t),
		epsilonTransition(right.accept, t),
	}

	trans = append(trans, left.transitions...)
	trans = append(trans, right.transitions...)

	return nfaFragment{
		start:       s,
		accept:      t,
		transitions: trans,
	}
}

// ------------------------------------------------------------
// KLEENE STAR (A*)
//
//           ┌────ε────┐
//           v         |
//     (s) --ε--> (A) --ε--> (t)
//      \              ^
//       -----ε---------
//
// ------------------------------------------------------------

func buildStar(ast *regexAST, c *uint64) nfaFragment {
	sub := thompsonBuild(ast.left, c)

	s := newState(c)
	t := newState(c)

	trans := []autarch.Transition[rune]{
		epsilonTransition(s, sub.start),          // enter loop
		epsilonTransition(s, t),                  // skip A entirely
		epsilonTransition(sub.accept, t),         // exit
		epsilonTransition(sub.accept, sub.start), // repeat
	}
	trans = append(trans, sub.transitions...)

	return nfaFragment{
		start:       s,
		accept:      t,
		transitions: trans,
	}
}

// ------------------------------------------------------------
// PLUS (A+)
//
// Equivalent to: A CONCAT (A*)
//
// ------------------------------------------------------------

func buildPlus(ast *regexAST, c *uint64) nfaFragment {
	// A+
	// = A · A*
	innerStar := &regexAST{
		kind: astStar,
		left: ast.left,
	}
	concat := &regexAST{
		kind:  astConcat,
		left:  ast.left,
		right: innerStar,
	}
	return thompsonBuild(concat, c)
}

// ------------------------------------------------------------
// OPTIONAL (A?)
//
// Correct implementation:
//
//         ┌─────ε─────┐
//         |           v
//    (s) --ε--> (A) --ε--> (t)
//
// ------------------------------------------------------------

func buildQuestion(ast *regexAST, c *uint64) nfaFragment {
	sub := thompsonBuild(ast.left, c)

	s := newState(c)
	t := newState(c)

	trans := []autarch.Transition[rune]{
		epsilonTransition(s, sub.start),  // Enter A's fragment
		epsilonTransition(s, t),          // Skip A entirely
		epsilonTransition(sub.accept, t), // Exit A's fragment
	}
	trans = append(trans, sub.transitions...)

	return nfaFragment{
		start:       s,
		accept:      t,
		transitions: trans,
	}
}

// ------------------------------------------------------------
// NEW STATE HELPER
// ------------------------------------------------------------

func newState(c *uint64) uint64 {
	s := *c
	*c++
	return s
}
