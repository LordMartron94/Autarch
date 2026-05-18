package pattern

import (
	"cmp"
	"fmt"

	"foundation/domain"
)

/*
PatternAlphabet is the set of observations a Regula pattern can match anywhere in its tree
(union of all literal runes and positive character-class ranges). Used to build conservative
negative lookaheads for literal-vs-dynamic disambiguation in syntax highlighting.
*/
type PatternAlphabet[TObservation cmp.Ordered] struct {
	ranges []CharRange[TObservation]
}

/* PatternAlphabetRanges returns a copy of the normalized ranges in this alphabet. */
func PatternAlphabetRanges[TObservation cmp.Ordered](a PatternAlphabet[TObservation]) []CharRange[TObservation] {
	if len(a.ranges) == 0 {
		return nil
	}
	out := make([]CharRange[TObservation], len(a.ranges))
	copy(out, a.ranges)
	return out
}

/* PatternIsPureLiteral reports whether the AST is a single literal sequence (no regex structure). */
func PatternIsPureLiteral[TObservation cmp.Ordered](ast RegulaAST[TObservation]) bool {
	return ast.kind == EXPRESSION_LITERAL
}

/*
PatternAlphabetCollect walks ast and returns the union of all literal runes and class ranges
the pattern can accept. Returns false when the walk yields no characters (empty pattern).
*/
func PatternAlphabetCollect[TObservation cmp.Ordered](ast RegulaAST[TObservation]) (PatternAlphabet[TObservation], bool) {
	order := observationOrdering[TObservation]()
	if order == nil {
		return PatternAlphabet[TObservation]{}, false
	}

	var acc []CharRange[TObservation]
	patternAlphabetWalk(&ast, &acc)
	if len(acc) == 0 {
		return PatternAlphabet[TObservation]{}, false
	}

	return PatternAlphabet[TObservation]{
		ranges: normalizeRanges(acc, order),
	}, true
}

/*
PatternAlphabetUnion merges multiple alphabets into one normalized range set.
*/
func PatternAlphabetUnion[TObservation cmp.Ordered](parts ...PatternAlphabet[TObservation]) PatternAlphabet[TObservation] {
	order := observationOrdering[TObservation]()
	if order == nil {
		return PatternAlphabet[TObservation]{}
	}

	var acc []CharRange[TObservation]
	for _, p := range parts {
		acc = append(acc, p.ranges...)
	}
	if len(acc) == 0 {
		return PatternAlphabet[TObservation]{}
	}
	return PatternAlphabet[TObservation]{ranges: normalizeRanges(acc, order)}
}

/*
PatternAlphabetToNegativeLookahead emits (?![class]) for the given alphabet.
TObservation must be rune or byte (supported by ToRegEx).
*/
func PatternAlphabetToNegativeLookahead[TObservation cmp.Ordered](a PatternAlphabet[TObservation]) (string, error) {
	if len(a.ranges) == 0 {
		return "", fmt.Errorf("PatternAlphabetToNegativeLookahead: empty alphabet")
	}

	var zero TObservation
	switch any(zero).(type) {
	case rune:
		ranges := make([]CharRange[rune], len(a.ranges))
		for i, r := range a.ranges {
			ranges[i] = CharRange[rune]{Lo: any(r.Lo).(rune), Hi: any(r.Hi).(rune)}
		}
		f := RegulaASTFactoryCreate(domain.DiscreteDomainRuneCreate())
		inner, err := f.Class(ranges...).ToRegEx()
		if err != nil {
			return "", err
		}
		return "(?!" + inner + ")", nil
	case byte:
		ranges := make([]CharRange[byte], len(a.ranges))
		for i, r := range a.ranges {
			ranges[i] = CharRange[byte]{Lo: any(r.Lo).(byte), Hi: any(r.Hi).(byte)}
		}
		f := RegulaASTFactoryCreate(domain.DiscreteDomainByteCreate())
		inner, err := f.Class(ranges...).ToRegEx()
		if err != nil {
			return "", err
		}
		return "(?!" + inner + ")", nil
	default:
		return "", fmt.Errorf("PatternAlphabetToNegativeLookahead: unsupported observation type %T", zero)
	}
}

func patternAlphabetWalk[TObservation cmp.Ordered](n *RegulaAST[TObservation], acc *[]CharRange[TObservation]) {
	if n == nil {
		return
	}

	switch n.kind {
	case EXPRESSION_LITERAL:
		for _, v := range n.literals {
			*acc = append(*acc, CharRange[TObservation]{Lo: v, Hi: v})
		}

	case EXPRESSION_CLASS:
		// ranges holds the positive match set (including inverted negated-class materialization).
		*acc = append(*acc, n.class.ranges...)

	case EXPRESSION_CONCAT, EXPRESSION_UNION:
		patternAlphabetWalk(n.left, acc)
		patternAlphabetWalk(n.right, acc)

	case EXPRESSION_REPEAT, EXPRESSION_CAPTURE:
		patternAlphabetWalk(n.sub, acc)
	}
}

func observationOrdering[TObservation cmp.Ordered]() func(a, b TObservation) int {
	var zero TObservation
	switch any(zero).(type) {
	case rune:
		return func(a, b TObservation) int {
			return cmp.Compare(any(a).(rune), any(b).(rune))
		}
	case byte:
		return func(a, b TObservation) int {
			return cmp.Compare(any(a).(byte), any(b).(byte))
		}
	default:
		return cmp.Compare[TObservation]
	}
}
