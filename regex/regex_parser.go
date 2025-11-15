package regex

import (
	"fmt"
)

// ------------------------------------------------------------
// PARSER ENTRYPOINT
// ------------------------------------------------------------

func parseRegex(tokens []regexToken) (*regexAST, error) {
	withConcat, err := insertConcatOperators(tokens)
	if err != nil {
		return nil, err
	}

	return shuntingYard(withConcat)
}

// ------------------------------------------------------------
// IMPLICIT CONCATENATION OPERATOR INSERTION
//
// Conditions where concatenation is inserted:
//   literal literal
//   literal (
//   literal class
//   class literal
//   class class
//   ) literal
//   ) (
//   ) class
//   * literal
//   * (
//   * class
//   + literal
//   + (
//   + class
//   ? literal
//   ? (
//   ? class
// ------------------------------------------------------------

func insertConcatOperators(tokens []regexToken) ([]regexToken, error) {
	out := make([]regexToken, 0, len(tokens)*2)

	for i := 0; i < len(tokens)-1; i++ {
		cur := tokens[i]
		next := tokens[i+1]

		out = append(out, cur)

		if canEndExpr(cur.kind) && canStartExpr(next.kind) {
			out = append(out, regexToken{kind: tokConcat})
		}
	}

	// append last real token
	out = append(out, tokens[len(tokens)-1])
	return out, nil
}

func canEndExpr(kind regexTokenKind) bool {
	switch kind {
	case tokLiteral, tokCharClass, tokRParen, tokStar, tokPlus, tokQuestion, tokBound, tokDot, tokClassSet:
		return true
	default:
		return false
	}
}

func canStartExpr(kind regexTokenKind) bool {
	switch kind {
	case tokLiteral, tokCharClass, tokLParen, tokDot, tokClassSet:
		return true
	default:
		return false
	}
}

// ------------------------------------------------------------
// OPERATOR PRECEDENCE / ASSOCIATIVITY
// ------------------------------------------------------------

func precedence(kind regexTokenKind) int {
	switch kind {
	case tokBound:
		return 4
	case tokStar, tokPlus, tokQuestion:
		return 3
	case tokConcat:
		return 2
	case tokAlt:
		return 1
	default:
		return 0
	}
}

func isLeftAssociative(kind regexTokenKind) bool {
	// postfix unary (* + ?) are left-associative in this usage
	switch kind {
	case tokBound, tokStar, tokPlus, tokQuestion:
		return true
	case tokConcat, tokAlt:
		return true
	default:
		return false
	}
}

func isOperator(kind regexTokenKind) bool {
	switch kind {
	case tokBound, tokStar, tokPlus, tokQuestion, tokConcat, tokAlt:
		return true
	default:
		return false
	}
}

// ------------------------------------------------------------
// SHUNTING-YARD PARSER
// ------------------------------------------------------------

func shuntingYard(tokens []regexToken) (*regexAST, error) {
	// operand stack
	var output []*regexAST

	// operator stack
	var ops []regexToken

	pushOp := func(tok regexToken) {
		ops = append(ops, tok)
	}
	popOp := func() (regexToken, bool) {
		if len(ops) == 0 {
			return regexToken{}, false
		}
		op := ops[len(ops)-1]
		ops = ops[:len(ops)-1]
		return op, true
	}
	peekOp := func() (regexToken, bool) {
		if len(ops) == 0 {
			return regexToken{}, false
		}
		return ops[len(ops)-1], true
	}

	applyOp := func(op regexToken) error {
		switch op.kind {

		case tokBound:
			if len(output) < 1 {
				return fmt.Errorf("bound {%d,%d} missing operand", op.min, op.max)
			}
			a := output[len(output)-1]
			output[len(output)-1] = astBoundNode(a, op.min, op.max)

		case tokLiteral:
			return fmt.Errorf("unexpected literal in operator context")

		case tokCharClass:
			return fmt.Errorf("unexpected charclass in operator context")

		case tokConcat:
			// binary
			if len(output) < 2 {
				return fmt.Errorf("concat missing operands")
			}
			b := output[len(output)-1]
			a := output[len(output)-2]
			output = output[:len(output)-2]
			output = append(output, astConcatNode(a, b))

		case tokAlt:
			// binary
			if len(output) < 2 {
				return fmt.Errorf("alt missing operands")
			}
			b := output[len(output)-1]
			a := output[len(output)-2]
			output = output[:len(output)-2]
			output = append(output, astAltNode(a, b))

		case tokStar:
			// unary postfix
			if len(output) < 1 {
				return fmt.Errorf("star missing operand")
			}
			a := output[len(output)-1]
			output[len(output)-1] = astStarNode(a)

		case tokPlus:
			if len(output) < 1 {
				return fmt.Errorf("plus missing operand")
			}
			a := output[len(output)-1]
			output[len(output)-1] = astPlusNode(a)

		case tokQuestion:
			if len(output) < 1 {
				return fmt.Errorf("question missing operand")
			}
			a := output[len(output)-1]
			output[len(output)-1] = astQuestionNode(a)
		default:
			return fmt.Errorf("unknown operator kind: %v", op.kind)
		}
		return nil
	}

	for _, tok := range tokens {

		switch tok.kind {

		// ------------------------------
		// Literals & classes go to output
		// ------------------------------
		case tokLiteral:
			output = append(output, astLiteralNode(tok.value))

		case tokCharClass:
			output = append(output, astClassNode(tok.class, tok.negated))

		// ------------------------------
		// Parentheses
		// ------------------------------
		case tokLParen:
			pushOp(tok)

		case tokRParen:
			// pop until matching (
			for {
				op, ok := popOp()
				if !ok {
					return nil, fmt.Errorf("unmatched ')'")
				}
				if op.kind == tokLParen {
					break
				}
				if err := applyOp(op); err != nil {
					return nil, err
				}
			}

		// ------------------------------
		// Operators
		// ------------------------------
		case tokStar, tokPlus, tokQuestion, tokConcat, tokAlt, tokBound:
			for {
				top, ok := peekOp()
				if !ok || top.kind == tokLParen {
					break
				}

				// apply operator with higher or equal precedence
				if isOperator(top.kind) &&
					(precedence(top.kind) > precedence(tok.kind) ||
						(precedence(top.kind) == precedence(tok.kind) && isLeftAssociative(tok.kind))) {

					_, _ = popOp()
					if err := applyOp(top); err != nil {
						return nil, err
					}
				} else {
					break
				}
			}
			pushOp(tok)

		case tokDot:
			output = append(output, astDotNode())
		case tokClassSet:
			output = append(output, astClassSetNode(tok.value))

		case tokEOF:
			continue

		default:
			return nil, fmt.Errorf("unexpected token kind: %v", tok.kind)
		}
	}

	// apply remaining operators
	for {
		op, ok := popOp()
		if !ok {
			break
		}
		if op.kind == tokLParen {
			return nil, fmt.Errorf("unmatched '('")
		}
		if err := applyOp(op); err != nil {
			return nil, err
		}
	}

	if len(output) != 1 {
		return nil, fmt.Errorf("invalid regex: final AST stack size = %d", len(output))
	}

	return output[0], nil
}
