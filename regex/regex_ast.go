package regex

// ------------------------------------------------------------
// AST NODE TYPES
// ------------------------------------------------------------

type regexASTKind int

const (
	astLiteral   regexASTKind = iota // 'a', 'b', 'x'
	astConcat                        // AB
	astAlt                           // A|B
	astStar                          // A*
	astPlus                          // A+
	astQuestion                      // A?
	astCharClass                     // [abc] or [a-z]
	astDot
	astClassSet
	astBound
)

// regexAST represents a node in the regex syntax tree.
type regexAST struct {
	kind regexASTKind

	// literal or class data
	value rune
	class []rune

	min int
	max int

	// child nodes
	left  *regexAST
	right *regexAST

	negated bool
}

// ------------------------------------------------------------
// CONSTRUCTORS
// ------------------------------------------------------------

func astLiteralNode(r rune) *regexAST {
	return &regexAST{
		kind:  astLiteral,
		value: r,
	}
}

func astConcatNode(a, b *regexAST) *regexAST {
	return &regexAST{
		kind:  astConcat,
		left:  a,
		right: b,
	}
}

func astAltNode(a, b *regexAST) *regexAST {
	return &regexAST{
		kind:  astAlt,
		left:  a,
		right: b,
	}
}

func astStarNode(a *regexAST) *regexAST {
	return &regexAST{
		kind: astStar,
		left: a,
	}
}

func astPlusNode(a *regexAST) *regexAST {
	return &regexAST{
		kind: astPlus,
		left: a,
	}
}

func astQuestionNode(a *regexAST) *regexAST {
	return &regexAST{
		kind: astQuestion,
		left: a,
	}
}

func astClassNode(chars []rune, negated bool) *regexAST {
	return &regexAST{
		kind:    astCharClass,
		class:   chars,
		negated: negated,
	}
}

func astDotNode() *regexAST {
	return &regexAST{kind: astDot}
}

func astClassSetNode(r rune) *regexAST {
	return &regexAST{
		kind:  astClassSet,
		value: r,
	}
}

func astBoundNode(a *regexAST, min, max int) *regexAST {
	return &regexAST{
		kind: astBound,
		left: a,
		min:  min,
		max:  max,
	}
}
