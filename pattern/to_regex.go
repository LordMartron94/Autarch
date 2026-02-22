package pattern

import (
	"fmt"
	"strings"
	"unicode"
)

// ============================================================
// REGEX EMISSION CONFIG
// ============================================================

/*
ObservationRegexFormatter defines how an observation space is rendered to a textual regex.

You must provide BOTH literal emission and class-range emission, because:
  - regex literals have one set of escaping rules (outside [...])
  - class contents have another set (inside [...])

Contract:
  - Literal(o) must return a regex fragment that matches exactly one observation o.
  - ClassAtom(o) must return a fragment valid inside [...], matching exactly o.
  - ClassRange(lo, hi) must return a fragment valid inside [...], matching any o in [lo, hi].
    If the observation space has no notion of contiguous ranges, you should return an error
    unless lo==hi, in which case you can emit ClassAtom(lo).
*/
type ObservationRegexFormatter[T any] interface {
	Literal(o T) (string, error)
	ClassAtom(o T) (string, error)
	ClassRange(lo, hi T) (string, error)
}

/*
RegexEmitConfig controls how AST nodes are grouped in the emitted regex.
*/
type RegexEmitConfig[T any] struct {
	Formatter ObservationRegexFormatter[T]

	// If true, union nodes are always emitted as (?:a|b).
	// This is strongly recommended.
	GroupUnions bool

	// If true, epsilon is emitted as (?:). If false, epsilon is emitted as "".
	// (?:) is safer when concatenated in larger contexts and easier to debug.
	EpsilonAsNonCapturingGroup bool

	// If true, an empty class emits (?!) (never-match). If false, it errors.
	EmptyClassAsNeverMatch bool
}

/*
ToRegExWith emits a RE2/Go-regexp compatible regex string using an injected formatter policy.
*/
func (r RegulaAST[TObservation]) ToRegExWith(cfg RegexEmitConfig[TObservation]) (string, error) {
	if cfg.Formatter == nil {
		return "", fmt.Errorf("ToRegExWith: nil Formatter")
	}
	// sensible defaults
	if !cfg.GroupUnions {
		cfg.GroupUnions = true
	}
	if !cfg.EpsilonAsNonCapturingGroup {
		cfg.EpsilonAsNonCapturingGroup = true
	}
	if !cfg.EmptyClassAsNeverMatch {
		cfg.EmptyClassAsNeverMatch = true
	}

	var sb strings.Builder
	if err := emitRegexWith(&sb, &r, precLowest, cfg); err != nil {
		return "", err
	}
	return sb.String(), nil
}

/*
ToRegEx is a convenience wrapper:
  - Supports rune and byte only.
  - For anything else, you MUST call ToRegExWith and inject a formatter.
*/
func (r RegulaAST[TObservation]) ToRegEx() (string, error) {
	// Default formatter only for rune/byte.
	var zero TObservation
	switch any(zero).(type) {
	case rune:
		return r.ToRegExWith(RegexEmitConfig[TObservation]{
			Formatter: RuneFormatter[TObservation]{},
		})
	case byte:
		return r.ToRegExWith(RegexEmitConfig[TObservation]{
			Formatter: ByteFormatter[TObservation]{},
		})
	default:
		return "", fmt.Errorf("ToRegEx: unsupported observation type %T (use ToRegExWith + formatter)", zero)
	}
}

// ============================================================
// PRECEDENCE / EMISSION CORE
// ============================================================

// precedence: larger binds tighter
type regexPrec uint8

const (
	precLowest regexPrec = iota // outside / top
	precUnion                   // a|b
	precConcat                  // ab
	precRepeat                  // a*
	precAtom                    // literal(1) / class / grouped
)

func nodePrec[T comparable](n *RegulaAST[T]) regexPrec {
	if n == nil {
		return precAtom
	}
	switch n.kind {
	case EXPRESSION_UNION:
		return precUnion
	case EXPRESSION_CONCAT:
		return precConcat
	case EXPRESSION_REPEAT:
		return precRepeat
	case EXPRESSION_LITERAL, EXPRESSION_CLASS:
		return precAtom
	default:
		return precAtom
	}
}

func isRegexAtom[T comparable](n *RegulaAST[T]) bool {
	if n == nil {
		return true
	}
	switch n.kind {
	case EXPRESSION_CLASS:
		return true
	case EXPRESSION_LITERAL:
		return len(n.literals) == 1
	case EXPRESSION_REPEAT:
		return true
	default:
		return false
	}
}

func emitRegexWith[T comparable](
	sb *strings.Builder,
	n *RegulaAST[T],
	parentPrec regexPrec,
	cfg RegexEmitConfig[T],
) error {
	if n == nil {
		if cfg.EpsilonAsNonCapturingGroup {
			sb.WriteString("(?:)")
		}
		return nil
	}

	switch n.kind {
	case EXPRESSION_LITERAL:
		return emitLiteralWith(sb, n.literals, cfg)

	case EXPRESSION_CLASS:
		return emitClassWith(sb, n.class, cfg)

	case EXPRESSION_CONCAT:
		if n.left == nil || n.right == nil {
			return fmt.Errorf("concat node has nil child")
		}
		needGroup := nodePrec(n) < parentPrec
		if needGroup {
			sb.WriteString("(?:")
		}
		if err := emitRegexWith(sb, n.left, precConcat, cfg); err != nil {
			return err
		}
		if err := emitRegexWith(sb, n.right, precConcat, cfg); err != nil {
			return err
		}
		if needGroup {
			sb.WriteString(")")
		}
		return nil

	case EXPRESSION_UNION:
		if n.left == nil || n.right == nil {
			return fmt.Errorf("union node has nil child")
		}

		// Strong recommendation: always group unions.
		if cfg.GroupUnions {
			sb.WriteString("(?:")
		} else {
			// Only group if precedence requires it.
			if nodePrec(n) < parentPrec {
				sb.WriteString("(?:")
				cfg.GroupUnions = true // local effect
			}
		}

		if err := emitRegexWith(sb, n.left, precUnion, cfg); err != nil {
			return err
		}
		sb.WriteString("|")
		if err := emitRegexWith(sb, n.right, precUnion, cfg); err != nil {
			return err
		}

		if cfg.GroupUnions {
			sb.WriteString(")")
		}
		return nil

	case EXPRESSION_REPEAT:
		if n.sub == nil {
			return fmt.Errorf("repeat node has nil sub")
		}

		subIsAtom := isRegexAtom(n.sub)
		if !subIsAtom {
			sb.WriteString("(?:")
		}
		if err := emitRegexWith(sb, n.sub, precRepeat, cfg); err != nil {
			return err
		}
		if !subIsAtom {
			sb.WriteString(")")
		}

		switch {
		case n.min == 0 && n.max == -1:
			sb.WriteString("*")
		case n.min == 1 && n.max == -1:
			sb.WriteString("+")
		case n.min == 0 && n.max == 1:
			sb.WriteString("?")
		case n.max == -1:
			if n.min < 0 {
				return fmt.Errorf("invalid repeat bounds: min=%d max=%d", n.min, n.max)
			}
			sb.WriteString("{")
			sb.WriteString(fmt.Sprintf("%d", n.min))
			sb.WriteString(",}")
		case n.min == n.max:
			if n.min < 0 {
				return fmt.Errorf("invalid repeat bounds: min=%d max=%d", n.min, n.max)
			}
			sb.WriteString("{")
			sb.WriteString(fmt.Sprintf("%d", n.min))
			sb.WriteString("}")
		default:
			if n.min < 0 || n.max < n.min {
				return fmt.Errorf("invalid repeat bounds: min=%d max=%d", n.min, n.max)
			}
			sb.WriteString("{")
			sb.WriteString(fmt.Sprintf("%d,%d", n.min, n.max))
			sb.WriteString("}")
		}
		return nil

	default:
		return fmt.Errorf("unknown regula node kind: %v", n.kind)
	}
}

func emitLiteralWith[T comparable](sb *strings.Builder, vals []T, cfg RegexEmitConfig[T]) error {
	if len(vals) == 0 {
		if cfg.EpsilonAsNonCapturingGroup {
			sb.WriteString("(?:)")
		}
		return nil
	}

	for _, v := range vals {
		s, err := cfg.Formatter.Literal(v)
		if err != nil {
			return err
		}
		sb.WriteString(s)
	}
	return nil
}

func emitClassWith[T comparable](sb *strings.Builder, cls charClass[T], cfg RegexEmitConfig[T]) error {
	if len(cls.ranges) == 0 {
		if cfg.EmptyClassAsNeverMatch {
			sb.WriteString("(?!)")
			return nil
		}
		return fmt.Errorf("empty char class")
	}

	sb.WriteString("[")
	for _, r := range cls.ranges {
		frag, err := cfg.Formatter.ClassRange(r.lo, r.hi)
		if err != nil {
			return err
		}
		sb.WriteString(frag)
	}
	sb.WriteString("]")
	return nil
}

// ============================================================
// DEFAULT FORMATTERS (rune / byte)
// ============================================================

// RuneFormatter implements ObservationRegexFormatter for rune.
type RuneFormatter[T any] struct{}

func (RuneFormatter[T]) Literal(o T) (string, error) {
	r, ok := any(o).(rune)
	if !ok {
		return "", fmt.Errorf("RuneFormatter: expected rune, got %T", o)
	}
	return escapeLiteralRune(r), nil
}

func (RuneFormatter[T]) ClassAtom(o T) (string, error) {
	r, ok := any(o).(rune)
	if !ok {
		return "", fmt.Errorf("RuneFormatter: expected rune, got %T", o)
	}
	return escapeClassRune(r), nil
}

func (f RuneFormatter[T]) ClassRange(lo, hi T) (string, error) {
	rlo, ok := any(lo).(rune)
	if !ok {
		return "", fmt.Errorf("RuneFormatter: expected rune lo, got %T", lo)
	}
	rhi, ok := any(hi).(rune)
	if !ok {
		return "", fmt.Errorf("RuneFormatter: expected rune hi, got %T", hi)
	}
	if rlo == rhi {
		return f.ClassAtom(lo)
	}
	return escapeClassRune(rlo) + "-" + escapeClassRune(rhi), nil
}

// ByteFormatter implements ObservationRegexFormatter for byte.
type ByteFormatter[T any] struct{}

func (ByteFormatter[T]) Literal(o T) (string, error) {
	b, ok := any(o).(byte)
	if !ok {
		return "", fmt.Errorf("ByteFormatter: expected byte, got %T", o)
	}
	return escapeLiteralByte(b), nil
}

func (ByteFormatter[T]) ClassAtom(o T) (string, error) {
	b, ok := any(o).(byte)
	if !ok {
		return "", fmt.Errorf("ByteFormatter: expected byte, got %T", o)
	}
	return escapeClassByte(b), nil
}

func (f ByteFormatter[T]) ClassRange(lo, hi T) (string, error) {
	blo, ok := any(lo).(byte)
	if !ok {
		return "", fmt.Errorf("ByteFormatter: expected byte lo, got %T", lo)
	}
	bhi, ok := any(hi).(byte)
	if !ok {
		return "", fmt.Errorf("ByteFormatter: expected byte hi, got %T", hi)
	}
	if blo == bhi {
		return f.ClassAtom(lo)
	}
	return escapeClassByte(blo) + "-" + escapeClassByte(bhi), nil
}

func escapeLiteralRune(r rune) string {
	if r == '\n' {
		return `\n`
	}
	if r == '\r' {
		return `\r`
	}
	if r == '\t' {
		return `\t`
	}
	switch r {
	case '\\', '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '^', '$':
		return `\` + string(r)
	}
	if !isPrintRune(r) {
		return fmt.Sprintf(`\x{%X}`, r)
	}
	return string(r)
}

func escapeLiteralByte(b byte) string {
	r := rune(b)
	if r == '\n' {
		return `\n`
	}
	if r == '\r' {
		return `\r`
	}
	if r == '\t' {
		return `\t`
	}
	switch r {
	case '\\', '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '^', '$':
		return `\` + string(r)
	}
	if r < 0x20 || r == 0x7F {
		return fmt.Sprintf(`\x%02X`, b)
	}
	return string(r)
}

func escapeClassRune(r rune) string {
	switch r {
	case '\\', '-', ']', '^':
		return `\` + string(r)
	}
	if r == '\n' {
		return `\n`
	}
	if r == '\r' {
		return `\r`
	}
	if r == '\t' {
		return `\t`
	}
	if !isPrintRune(r) {
		return fmt.Sprintf(`\x{%X}`, r)
	}
	return string(r)
}

func escapeClassByte(b byte) string {
	r := rune(b)
	switch r {
	case '\\', '-', ']', '^':
		return `\` + string(r)
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	}
	if r < 0x20 || r == 0x7F {
		return fmt.Sprintf(`\x%02X`, b)
	}
	return string(r)
}

func isPrintRune(r rune) bool {
	if r >= 0x20 && r != 0x7F && r <= 0x7E {
		return true
	}
	return unicode.IsPrint(r)
}
