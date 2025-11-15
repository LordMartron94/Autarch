package regex

import (
	"autarch"
	"fmt"
)

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

	case astBound:
		return buildBound(ast, stateCounter)

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

	case astDot:
		return buildDot(ast, stateCounter)
	case astClassSet:
		return buildClassSet(ast, stateCounter)

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
	if !ast.negated {
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

	s := newState(c)
	t := newState(c)

	symbol := autarch.SymbolCreate[rune](
		string(ast.class),
		autarch.AutarchWildcardID,
	)

	trans := autarch.Transition[rune]{
		Symbol:       symbol,
		CurrentState: s,
		NextState:    t,
	}

	return nfaFragment{
		start:       s,
		accept:      t,
		transitions: []autarch.Transition[rune]{trans},
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

func buildDot(ast *regexAST, c *uint64) nfaFragment {
	s := newState(c)
	t := newState(c)

	dotTransition := autarch.Transition[rune]{
		Symbol: autarch.SymbolCreate[rune](
			".",
			autarch.AutarchWildcardID,
		),
		CurrentState: s,
		NextState:    t,
	}

	return nfaFragment{
		start:       s,
		accept:      t,
		transitions: []autarch.Transition[rune]{dotTransition},
	}
}

// buildClassSet expands \d, \s, etc. into a real char class
func buildClassSet(ast *regexAST, c *uint64) nfaFragment {
	var runes []rune
	switch ast.value {
	case 'd':
		runes = []rune("0123456789")
	case 's':
		runes = []rune(" \t\n\r\f\v")
	default:
		panic(fmt.Sprintf("unknown class set: %c", ast.value))
	}

	return buildCharClass(&regexAST{
		kind:  astCharClass,
		class: runes,
	}, c)
}

func fragmentConcat(left, right nfaFragment) nfaFragment {
	trans := append(left.transitions,
		append([]autarch.Transition[rune]{epsilonTransition(left.accept, right.start)},
			right.transitions...)...)

	return nfaFragment{
		start:       left.start,
		accept:      right.accept,
		transitions: trans,
	}
}

func buildBound(ast *regexAST, c *uint64) nfaFragment {
	min := ast.min
	max := ast.max
	subAST := ast.left

	if min == 0 && max == 0 {
		s := newState(c)
		return nfaFragment{
			start:       s,
			accept:      s,
			transitions: []autarch.Transition[rune]{},
		}
	}

	var finalFragment nfaFragment

	if min > 0 {
		finalFragment = thompsonBuild(subAST, c)
		for i := 1; i < min; i++ {
			nextFrag := thompsonBuild(subAST, c)
			finalFragment = fragmentConcat(finalFragment, nextFrag)
		}
	}

	if min == max {
		return finalFragment
	}

	if max == -1 {
		starAST := &regexAST{kind: astStar, left: subAST}
		starFrag := thompsonBuild(starAST, c)

		if min == 0 {
			return starFrag
		}
		return fragmentConcat(finalFragment, starFrag)
	}

	if min == 0 {
		finalFragment = thompsonBuild(&regexAST{kind: astQuestion, left: subAST}, c)
		for i := 1; i < max; i++ {
			optFrag := thompsonBuild(&regexAST{kind: astQuestion, left: subAST}, c)
			finalFragment = fragmentConcat(finalFragment, optFrag)
		}
		return finalFragment
	}

	for i := 0; i < (max - min); i++ {
		optFrag := thompsonBuild(&regexAST{kind: astQuestion, left: subAST}, c)
		finalFragment = fragmentConcat(finalFragment, optFrag)
	}
	return finalFragment
}

// ------------------------------------------------------------
// NEW STATE HELPER
// ------------------------------------------------------------

func newState(c *uint64) uint64 {
	s := *c
	*c++
	return s
}
