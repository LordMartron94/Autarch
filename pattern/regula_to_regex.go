package pattern

import (
	"fmt"
	"foundation/text"
	"strings"
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

	// GroupUnions dictates whether union nodes are always emitted as (?:a|b).
	// This is strongly recommended to prevent precedence leaking.
	GroupUnions bool

	// EpsilonAsNonCapturingGroup dictates whether epsilon is emitted as (?:).
	// If false, epsilon is emitted as "". (?:) is safer when concatenated.
	EpsilonAsNonCapturingGroup bool

	// EmptyClassAsNeverMatch dictates whether an empty class emits (?!) (never-match).
	// If false, attempting to emit an empty class yields an error.
	EmptyClassAsNeverMatch bool
}

/*
ToRegExWith emits a RE2/Go-regexp compatible regex string using an injected formatter policy.
*/
func (r RegulaAST[TObservation]) ToRegExWith(cfg RegexEmitConfig[TObservation]) (string, error) {
	if cfg.Formatter == nil {
		return "", fmt.Errorf("ToRegExWith: nil Formatter")
	}

	// Sensible defaults
	if !cfg.GroupUnions {
		cfg.GroupUnions = true
	}
	if !cfg.EpsilonAsNonCapturingGroup {
		cfg.EpsilonAsNonCapturingGroup = true
	}
	if !cfg.EmptyClassAsNeverMatch {
		cfg.EmptyClassAsNeverMatch = true
	}

	d := newRegexDecompiler(cfg)
	d.walk(&r, precLowest)

	if d.err != nil {
		return "", d.err
	}
	return d.out.String(), nil
}

/*
ToRegEx is a convenience wrapper for ASTs dealing with runes or bytes.
For anything else, you MUST call ToRegExWith and inject a formatter.
*/
func (r RegulaAST[TObservation]) ToRegEx() (string, error) {
	var zero TObservation
	switch any(zero).(type) {
	case rune:
		return r.ToRegExWith(RegexEmitConfig[TObservation]{
			Formatter:                  RuneFormatter[TObservation]{},
			GroupUnions:                true,
			EpsilonAsNonCapturingGroup: true,
			EmptyClassAsNeverMatch:     true,
		})
	case byte:
		return r.ToRegExWith(RegexEmitConfig[TObservation]{
			Formatter:                  ByteFormatter[TObservation]{},
			GroupUnions:                true,
			EpsilonAsNonCapturingGroup: true,
			EmptyClassAsNeverMatch:     true,
		})
	default:
		return "", fmt.Errorf("ToRegEx: unsupported observation type %T (use ToRegExWith + formatter)", zero)
	}
}

// ============================================================
// PRECEDENCE / DECOMPILER
// ============================================================

// regexPrec defines the precedence level; larger binds tighter
type regexPrec uint8

const (
	precLowest regexPrec = iota // outside / top
	precUnion                   // a|b
	precConcat                  // ab
	precRepeat                  // a*
	precAtom                    // literal(1) / class / grouped
)

func isRegexAtom[T any](n *RegulaAST[T]) bool {
	if n == nil {
		return true
	}
	switch n.kind {
	case EXPRESSION_CLASS, EXPRESSION_CAPTURE:
		return true
	case EXPRESSION_LITERAL:
		return len(n.literals) == 1
	case EXPRESSION_REPEAT:
		return true
	default:
		return false
	}
}

/*
regexDecompiler manages the state of the AST serialization process.
It tracks errors and hierarchical precedence to ensure structural integrity.
*/
type regexDecompiler[T any] struct {
	out        *strings.Builder
	cfg        RegexEmitConfig[T]
	parentPrec regexPrec
	err        error
}

func newRegexDecompiler[T any](cfg RegexEmitConfig[T]) *regexDecompiler[T] {
	return &regexDecompiler[T]{
		out:        &strings.Builder{},
		cfg:        cfg,
		parentPrec: precLowest,
	}
}

/*
walk executes the visitor pattern while managing precedence state transitions.
*/
func (d *regexDecompiler[T]) walk(n *RegulaAST[T], targetPrec regexPrec) {
	if d.err != nil {
		return
	}

	if n == nil {
		if d.cfg.EpsilonAsNonCapturingGroup {
			d.out.WriteString("(?:)")
		}
		return
	}

	oldPrec := d.parentPrec
	d.parentPrec = targetPrec
	n.Accept(d.visitor())
	d.parentPrec = oldPrec
}

func (d *regexDecompiler[T]) visitor() RegulaVisitor[T] {
	return RegulaVisitor[T]{
		VisitLiteral: d.visitLiteral,
		VisitClass:   d.visitClass,
		VisitConcat:  d.visitConcat,
		VisitUnion:   d.visitUnion,
		VisitRepeat:  d.visitRepeat,
		VisitCapture: d.visitCapture,
	}
}

func (d *regexDecompiler[T]) visitLiteral(values []T) {
	if len(values) == 0 {
		if d.cfg.EpsilonAsNonCapturingGroup {
			d.out.WriteString("(?:)")
		}
		return
	}

	for _, v := range values {
		s, err := d.cfg.Formatter.Literal(v)
		if err != nil {
			d.err = err
			return
		}
		d.out.WriteString(s)
	}
}

func (d *regexDecompiler[T]) visitClass(ranges []CharRange[T], negatedFrom []CharRange[T]) {
	if len(ranges) == 0 && len(negatedFrom) == 0 {
		if d.cfg.EmptyClassAsNeverMatch {
			d.out.WriteString("(?!)")
		} else {
			d.err = fmt.Errorf("empty char class")
		}
		return
	}

	d.out.WriteString("[")

	if len(negatedFrom) > 0 {
		d.out.WriteString("^")
		for _, r := range negatedFrom {
			frag, err := d.cfg.Formatter.ClassRange(r.Lo, r.Hi)
			if err != nil {
				d.err = err
				return
			}
			d.out.WriteString(frag)
		}
	} else {
		for _, r := range ranges {
			frag, err := d.cfg.Formatter.ClassRange(r.Lo, r.Hi)
			if err != nil {
				d.err = err
				return
			}
			d.out.WriteString(frag)
		}
	}

	d.out.WriteString("]")
}

func (d *regexDecompiler[T]) visitConcat(left, right *RegulaAST[T]) {
	if left == nil || right == nil {
		d.err = fmt.Errorf("concat node has nil child")
		return
	}

	needGroup := precConcat < d.parentPrec
	if needGroup {
		d.out.WriteString("(?:")
	}

	d.walk(left, precConcat)
	d.walk(right, precConcat)

	if needGroup {
		d.out.WriteString(")")
	}
}

func (d *regexDecompiler[T]) visitUnion(left, right *RegulaAST[T]) {
	if left == nil || right == nil {
		d.err = fmt.Errorf("union node has nil child")
		return
	}

	needGroup := d.cfg.GroupUnions || precUnion < d.parentPrec
	if needGroup {
		d.out.WriteString("(?:")
	}

	d.walk(left, precUnion)
	d.out.WriteString("|")
	d.walk(right, precUnion)

	if needGroup {
		d.out.WriteString(")")
	}
}

func (d *regexDecompiler[T]) visitRepeat(sub *RegulaAST[T], min, max int) {
	if sub == nil {
		d.err = fmt.Errorf("repeat node has nil sub")
		return
	}

	subIsAtom := isRegexAtom(sub)
	if !subIsAtom {
		d.out.WriteString("(?:")
	}

	d.walk(sub, precRepeat)

	if !subIsAtom {
		d.out.WriteString(")")
	}

	switch {
	case min == 0 && max == -1:
		d.out.WriteString("*")
	case min == 1 && max == -1:
		d.out.WriteString("+")
	case min == 0 && max == 1:
		d.out.WriteString("?")
	case max == -1:
		if min < 0 {
			d.err = fmt.Errorf("invalid repeat bounds: min=%d max=%d", min, max)
			return
		}
		d.out.WriteString(fmt.Sprintf("{%d,}", min))
	case min == max:
		if min < 0 {
			d.err = fmt.Errorf("invalid repeat bounds: min=%d max=%d", min, max)
			return
		}
		d.out.WriteString(fmt.Sprintf("{%d}", min))
	default:
		if min < 0 || max < min {
			d.err = fmt.Errorf("invalid repeat bounds: min=%d max=%d", min, max)
			return
		}
		d.out.WriteString(fmt.Sprintf("{%d,%d}", min, max))
	}
}

func (d *regexDecompiler[T]) visitCapture(sub *RegulaAST[T]) {
	if sub == nil {
		d.err = fmt.Errorf("capture node has nil sub")
		return
	}

	d.out.WriteString("(")
	d.walk(sub, precLowest)
	d.out.WriteString(")")
}

// ============================================================
// DEFAULT FORMATTERS (rune / byte)
// ============================================================

/*
RuneFormatter implements ObservationRegexFormatter for rune using the foundation/text encoding.
*/
type RuneFormatter[T any] struct{}

func (f RuneFormatter[T]) Literal(o T) (string, error) {
	r, ok := any(o).(rune)
	if !ok {
		return "", fmt.Errorf("RuneFormatter: expected rune, got %T", o)
	}
	return text.RegexLiteralEscaper.EscapeRune(r), nil
}

func (f RuneFormatter[T]) ClassAtom(o T) (string, error) {
	r, ok := any(o).(rune)
	if !ok {
		return "", fmt.Errorf("RuneFormatter: expected rune, got %T", o)
	}
	return text.RegexClassEscaper.EscapeRune(r), nil
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
	return text.RegexClassEscaper.EscapeRune(rlo) + "-" + text.RegexClassEscaper.EscapeRune(rhi), nil
}

/*
ByteFormatter implements ObservationRegexFormatter for byte.
It delegates to the foundation/text regex escapers by casting the byte to a rune.
*/
type ByteFormatter[T any] struct{}

func (f ByteFormatter[T]) Literal(o T) (string, error) {
	b, ok := any(o).(byte)
	if !ok {
		return "", fmt.Errorf("ByteFormatter: expected byte, got %T", o)
	}
	return text.RegexLiteralEscaper.EscapeRune(rune(b)), nil
}

func (f ByteFormatter[T]) ClassAtom(o T) (string, error) {
	b, ok := any(o).(byte)
	if !ok {
		return "", fmt.Errorf("ByteFormatter: expected byte, got %T", o)
	}
	return text.RegexClassEscaper.EscapeRune(rune(b)), nil
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
	return text.RegexClassEscaper.EscapeRune(rune(blo)) + "-" + text.RegexClassEscaper.EscapeRune(rune(bhi)), nil
}
