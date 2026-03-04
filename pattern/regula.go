package pattern

import (
	"foundation/domain"
	"slices"
)

//go:generate stringer -type ExpressionKind
type ExpressionKind uint8

const (
	EXPRESSION_LITERAL ExpressionKind = iota
	EXPRESSION_CLASS
	EXPRESSION_CONCAT
	EXPRESSION_UNION
	EXPRESSION_REPEAT
	EXPRESSION_CAPTURE
)

type charRange[TObservation any] struct {
	lo TObservation
	hi TObservation // inclusive
}

type charClass[TObservation any] struct {
	ranges      []charRange[TObservation] // The resolved, positive ranges (used by your DFA)
	negatedFrom []charRange[TObservation] // The original intent (used by your Regex emitter)
}

type positionID uint64

/*
AnnotationID serves as a mapping key the client can use to associate nodes with a certain annotation.
These IDs are included in the compilation step and can thus serve to disambiguate and keep semantic context.
*/
type AnnotationID uint64

type RegulaAST[TObservation any] struct {
	kind ExpressionKind

	// Raw (used before binding)
	literals []TObservation
	class    charClass[TObservation]

	// symbol identity (shared, deduped)
	literalSymIDs []logicalID
	classSymID    logicalID

	// occurrence identity (unique per occurrence, for Glushkov)
	literalPosIDs []positionID
	classPosID    positionID

	// Bound (used after binding)
	literalBound bool
	classBound   bool

	left, right *RegulaAST[TObservation]

	min, max int
	sub      *RegulaAST[TObservation]

	annotationID *AnnotationID
}

/* WithAnnotationID sets the annotation id for this node. */
func (r *RegulaAST[TObservation]) WithAnnotationID(id AnnotationID) *RegulaAST[TObservation] {
	r.annotationID = &id
	return r
}

/*
	Then concatenates two patterns, matching the left pattern followed by the right pattern.

The resulting pattern matches input sequences where the first part matches r and the second
part matches b, in that order. This is equivalent to the concatenation operator in regular
expressions (e.g., "ab" matches pattern "a" then "b").

Use cases:
- Building sequential patterns (e.g., keyword followed by identifier)
- Constructing structured formats (e.g., prefix + body + suffix)
- Creating multi-part token patterns

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- Both r and b must be valid regulaAST patterns

Edge cases:
- Concatenating empty patterns (epsilon) with other patterns is supported
- The resulting pattern maintains references to the original patterns
*/
func (r RegulaAST[TObservation]) Then(b RegulaAST[TObservation]) RegulaAST[TObservation] {
	return RegulaAST[TObservation]{
		kind:  EXPRESSION_CONCAT,
		left:  &r,
		right: &b,
	}
}

/*
	Or creates an alternation pattern that matches either the left or right pattern.

The resulting pattern matches input that matches either r or b. This is equivalent to the
alternation operator in regular expressions (e.g., "a|b" matches either "a" or "b").

Use cases:
- Creating keyword alternatives (e.g., "if" or "else" or "while")
- Building flexible token patterns with multiple valid forms
- Implementing optional variations in pattern matching

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- Both r and b must be valid regulaAST patterns

Edge cases:
- If both patterns can match the same input, the NFA will accept both paths
- The resulting pattern maintains references to the original patterns
*/
func (r RegulaAST[TObservation]) Or(b RegulaAST[TObservation]) RegulaAST[TObservation] {
	return RegulaAST[TObservation]{
		kind:  EXPRESSION_UNION,
		left:  &r,
		right: &b,
	}
}

/*
	Star creates a Kleene star pattern that matches zero or more repetitions of the pattern.

The resulting pattern matches any number of repetitions of r, including zero. This is
equivalent to the Kleene star operator in regular expressions (e.g., "a*" matches "", "a", "aa", "aaa", etc.).

Use cases:
- Matching variable-length sequences (e.g., whitespace, digits, identifiers)
- Creating optional repeated patterns
- Building patterns that accept empty input

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- r must be a valid regulaAST pattern

Edge cases:
- Matches empty input (zero repetitions)
- Matches infinite repetitions (no upper bound)
- The resulting pattern maintains a reference to the original pattern
*/
func (r RegulaAST[TObservation]) Star() RegulaAST[TObservation] {
	return repeat(r, 0, -1)
}

/*
	Plus creates a pattern that matches one or more repetitions of the pattern.

The resulting pattern matches one or more repetitions of r. This is equivalent to the
plus operator in regular expressions (e.g., "a+" matches "a", "aa", "aaa", etc., but not "").

Use cases:
- Matching required sequences (e.g., at least one digit, at least one letter)
- Building patterns that require non-empty input
- Creating mandatory repeated patterns

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- r must be a valid regulaAST pattern

Edge cases:
- Does not match empty input (requires at least one repetition)
- Matches infinite repetitions (no upper bound)
- The resulting pattern maintains a reference to the original pattern
*/
func (r RegulaAST[TObservation]) Plus() RegulaAST[TObservation] {
	return repeat(r, 1, -1)
}

/*
	Optional creates a pattern that matches zero or one repetition of the pattern.

The resulting pattern matches either zero or one occurrence of r. This is equivalent to
the question mark operator in regular expressions (e.g., "a?" matches "" or "a").

Use cases:
- Creating optional components in patterns (e.g., optional sign in numbers)
- Building patterns with optional prefixes or suffixes
- Implementing optional whitespace or separators

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- r must be a valid regulaAST pattern

Edge cases:
- Matches empty input (zero repetitions)
- Matches exactly one repetition
- The resulting pattern maintains a reference to the original pattern
*/
func (r RegulaAST[TObservation]) Optional() RegulaAST[TObservation] {
	return repeat(r, 0, 1)
}

/*
	Repeat creates a pattern that matches a bounded number of repetitions of the pattern.

The resulting pattern matches between min and max repetitions of r (inclusive). If max is -1,
there is no upper bound. This is equivalent to bounded repetition in regular expressions
(e.g., "a{2,5}" matches "aa", "aaa", "aaaa", or "aaaaa").

Use cases:
- Matching fixed-length sequences (e.g., exactly 4 digits for years)
- Creating bounded repetition patterns (e.g., 1-3 digits for day numbers)
- Building patterns with minimum requirements (e.g., at least 8 characters)

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- r must be a valid regulaAST pattern
- min must be >= 0
- max must be >= min or -1 (unbounded)

Edge cases:
- Panics if max != -1 and max < min
- If min == 0, matches empty input
- If max == -1, matches infinite repetitions
- The resulting pattern maintains a reference to the original pattern
*/
func (r RegulaAST[TObservation]) Repeat(min, max int) RegulaAST[TObservation] {
	if max != -1 && max < min {
		panic("invalid repeat bounds")
	}
	return repeat(r, min, max)
}

/*
Capture wraps the pattern in a capturing group.

Warning: This has zero effect on the lexarch DFA execution. It exists
strictly to map sub-expressions into PCRE capture groups during ToRegEx emission.
*/
func (r RegulaAST[TObservation]) Capture() RegulaAST[TObservation] {
	return RegulaAST[TObservation]{
		kind: EXPRESSION_CAPTURE,
		sub:  &r,
	}
}

/* RegulaASTFactory encapsulates the factory for constructing Regula AST. */
type RegulaASTFactory[TObservation any] struct {
	observationDomain *domain.DiscreteDomain[TObservation]
}

/* RegulaASTFactoryCreate constructs a regula AST factory from a discrete observation domain. */
func RegulaASTFactoryCreate[TObservation any](
	observationDomain *domain.DiscreteDomain[TObservation],
) *RegulaASTFactory[TObservation] {
	return &RegulaASTFactory[TObservation]{
		observationDomain: observationDomain,
	}
}

/*
	Sequence creates a concatenation pattern from multiple expressions in order.

The resulting pattern matches input sequences where each part matches the corresponding
expression in order. This is equivalent to concatenating multiple patterns with Then,
but provides a more convenient syntax for sequences of three or more patterns.

Use cases:
- Building multi-part patterns (e.g., prefix + body + suffix)
- Creating structured formats with multiple required components
- Constructing complex sequential token patterns

Time complexity: O(n) where n is the number of expressions
Space complexity: O(n) - creates intermediate AST nodes

Prerequisites:
- At least one expression must be provided
- All expressions must be valid regulaAST patterns

Edge cases:
- Panics if no expressions are provided
- Single expression returns that expression unchanged
- The resulting pattern maintains references to all input patterns
*/
func (r *RegulaASTFactory[TObservation]) Sequence(expressions ...RegulaAST[TObservation]) RegulaAST[TObservation] {
	if len(expressions) == 0 {
		panic("empty sequence")
	}
	return buildBalancedTree(expressions, func(l, r RegulaAST[TObservation]) RegulaAST[TObservation] {
		return l.Then(r)
	})
}

/*
	AnyOf creates an alternation pattern from multiple expressions.

The resulting pattern matches input that matches any of the provided expressions.
This is equivalent to creating alternations with Or, but provides a more convenient
syntax for three or more alternatives.

Use cases:
- Creating keyword alternatives (e.g., "if", "else", "while", "for")
- Building flexible token patterns with multiple valid forms
- Implementing pattern matching with multiple acceptable inputs

Time complexity: O(n) where n is the number of expressions
Space complexity: O(n) - creates intermediate AST nodes

Prerequisites:
- At least one expression must be provided
- All expressions must be valid regulaAST patterns

Edge cases:
- Panics if no expressions are provided
- Single expression returns that expression unchanged
- If multiple expressions can match the same input, the NFA will accept all matching paths
- The resulting pattern maintains references to all input patterns
*/
func (r *RegulaASTFactory[TObservation]) AnyOf(expressions ...RegulaAST[TObservation]) RegulaAST[TObservation] {
	if len(expressions) == 0 {
		panic("empty alternation")
	}
	return buildBalancedTree(expressions, func(l, r RegulaAST[TObservation]) RegulaAST[TObservation] {
		return l.Or(r)
	})
}

func buildBalancedTree[T any](
	items []T,
	join func(l, r T) T,
) T {
	count := len(items)
	if count == 1 {
		return items[0]
	}

	mid := count / 2
	left := buildBalancedTree(items[:mid], join)
	right := buildBalancedTree(items[mid:], join)

	return join(left, right)
}

/*
	Range creates a character range for use in character class patterns.

The range includes all observations from lo to hi (inclusive). Ranges are used to
define character classes that match any observation within the specified bounds.
Multiple ranges can be combined using Class to create complex character classes.

Use cases:
- Defining character classes (e.g., digits '0'-'9', letters 'a'-'z')
- Creating inclusive ranges for pattern matching
- Building character sets for lexical analysis

Time complexity: O(1) - creates a range struct
Space complexity: O(1) - constant memory overhead

Prerequisites:
- lo and hi must be any using any
- lo should be <= hi for logical consistency (not enforced)

Edge cases:
- If lo > hi, the range will never match any observation
- If lo == hi, the range matches exactly one observation
- Ranges are normalized and merged when used in Class
*/
func (r *RegulaASTFactory[TObservation]) Range(lo, hi TObservation) charRange[TObservation] {
	return charRange[TObservation]{
		lo: lo,
		hi: hi,
	}
}

/*
	Class creates a character class pattern that matches any observation within the specified ranges.

The resulting pattern matches any single observation that falls within any of the provided
ranges. Ranges are automatically normalized (sorted and merged) to eliminate overlaps and
create an efficient representation. This is equivalent to character classes in regular
expressions (e.g., "[0-9a-z]" matches any digit or lowercase letter).

Use cases:
- Matching character sets (e.g., digits, letters, whitespace)
- Creating flexible single-character patterns
- Building token patterns for lexical analysis

Time complexity: O(n log n) where n is the number of ranges (due to sorting and merging)
Space complexity: O(n) - stores normalized ranges

Prerequisites:
- All ranges must have any bounds using any
- At least one range should be provided (empty class matches nothing)

Edge cases:
- Empty ranges list creates a pattern that never matches
- Overlapping ranges are automatically merged
- Ranges are sorted by lower bound during normalization
- The resulting pattern maintains normalized ranges internally
*/
func (r *RegulaASTFactory[TObservation]) Class(ranges ...charRange[TObservation]) RegulaAST[TObservation] {
	normalized := normalizeRanges(ranges, r.observationDomain.OrderingCmp)
	return RegulaAST[TObservation]{
		kind: EXPRESSION_CLASS,
		class: charClass[TObservation]{
			ranges:      normalized,
			negatedFrom: nil,
		},
	}
}

/*
NegatedClass creates a character class that matches any single observation outside the provided ranges.

The result is the complement of the given ranges within the factory's DiscreteDomain: it matches
any observation in [Min, Max] that does not fall inside any of the input ranges. Input ranges are
normalized (sorted and merged), then inverted into one or more gaps between currentPos and the
domain ceiling, using the domain's OrderingCmp and NextFn/PreviousFn.

Use cases:
- "Any character except these" (e.g. not a quote, not a delimiter)
- Building negated character classes (regex-style [^0-9], [^a-z])
- Matching single observations outside a known set for lexers and parsers

Time complexity: O(n log n) where n is the number of ranges (normalize then linear pass to invert).
Space complexity: O(n) - stores the inverted range list.

Prerequisites:
- The factory must have a non-nil DiscreteDomain with valid Min, Max, OrderingCmp, NextFn, PreviousFn.

Edge cases:
- Empty ranges list: matches the entire domain (inverted is the single range [Min, Max]).
- Ranges covering the whole domain: inverted is empty; the resulting pattern never matches a single observation.
- Overlapping or unsorted ranges are normalized before inversion.
*/
func (r *RegulaASTFactory[TObservation]) NegatedClass(ranges ...charRange[TObservation]) RegulaAST[TObservation] {
	normalized := normalizeRanges(ranges, r.observationDomain.OrderingCmp)
	inverted := r.invertRanges(normalized)

	return RegulaAST[TObservation]{
		kind: EXPRESSION_CLASS,
		class: charClass[TObservation]{
			ranges:      inverted,
			negatedFrom: normalized, // Preserve semantic intent
		},
	}
}

func (r *RegulaASTFactory[TObservation]) invertRanges(normalized []charRange[TObservation]) []charRange[TObservation] {
	d := r.observationDomain
	var inverted []charRange[TObservation]

	// Start at the absolute floor of the domain
	currentPos := d.Min

	for _, gap := range normalized {
		// If there is space between currentPos and the start of the range, that's a match
		if d.OrderingCmp(currentPos, gap.lo) < 0 {
			prev, _ := d.PreviousFn(gap.lo)
			inverted = append(inverted, charRange[TObservation]{lo: currentPos, hi: prev})
		}

		// Move currentPos to the first valid observation AFTER this range
		next, exists := d.NextFn(gap.hi)
		if !exists {
			// We've hit the end of the domain (gap.hi was d.Max)
			return inverted
		}
		currentPos = next
	}

	// Final check: Is there a gap between the last range and d.Max?
	if d.OrderingCmp(currentPos, d.Max) <= 0 {
		inverted = append(inverted, charRange[TObservation]{lo: currentPos, hi: d.Max})
	}

	return inverted
}

/*
	Literal creates a pattern that matches a specific sequence of observations.

The resulting pattern matches input that exactly matches the provided sequence of values
in order. This is equivalent to literal strings in regular expressions (e.g., "abc" matches
exactly the sequence 'a', 'b', 'c').

Use cases:
- Matching exact sequences (e.g., keywords, fixed strings, magic numbers)
- Building patterns for specific token values
- Creating fixed-prefix or fixed-suffix patterns

Time complexity: O(1) - creates an AST node
Space complexity: O(n) where n is the number of values (stores the sequence)

Prerequisites:
- All values must be any using any
- At least one value should be provided (empty literal matches empty input)

Edge cases:
- Empty values list creates a pattern that matches empty input (epsilon)
- Single value creates a pattern matching exactly that observation
- Multiple values create a pattern matching the exact sequence
- The resulting pattern maintains a copy of the input values
*/
func (r *RegulaASTFactory[TObservation]) Literal(values ...TObservation) RegulaAST[TObservation] {
	return RegulaAST[TObservation]{
		kind:     EXPRESSION_LITERAL,
		literals: values,
	}
}

func LiteralString(
	f *RegulaASTFactory[rune],
	s string,
) RegulaAST[rune] {
	runes := []rune(s)
	return f.Literal(runes...)
}

func WordLiteral(factory *RegulaASTFactory[rune], s string) RegulaAST[rune] {
	return LiteralString(factory, s)
}

func OneOf(factory *RegulaASTFactory[rune], chars string) RegulaAST[rune] {
	runes := []rune(chars)
	ranges := make([]charRange[rune], len(runes))
	for i, r := range runes {
		ranges[i] = factory.Range(r, r)
	}
	return factory.Class(ranges...)
}

func repeat[TObservation any](sub RegulaAST[TObservation], min, max int) RegulaAST[TObservation] {
	return RegulaAST[TObservation]{
		kind: EXPRESSION_REPEAT,
		sub:  &sub,
		min:  min,
		max:  max,
	}
}

func normalizeRanges[T any](
	in []charRange[T],
	cmpFn func(a, b T) int,
) []charRange[T] {
	if len(in) == 0 {
		return nil
	}

	ranges := make([]charRange[T], len(in))
	copy(ranges, in)

	slices.SortFunc(ranges, func(a, b charRange[T]) int {
		return cmpFn(a.lo, b.lo)
	})

	out := make([]charRange[T], 0, len(ranges))

	cur := ranges[0]

	for i := 1; i < len(ranges); i++ {
		r := ranges[i]

		// if r.lo <= cur.hi
		if cmpFn(r.lo, cur.hi) <= 0 {

			// if r.hi > cur.hi
			if cmpFn(r.hi, cur.hi) > 0 {
				cur.hi = r.hi
			}

		} else {
			out = append(out, cur)
			cur = r
		}
	}

	out = append(out, cur)
	return out
}

// ------------------------------------------------------- REGULA SYMBOL COLLECTION & BINDING

/*
regulaCollectSymbols walks a Regula AST and feeds all literals and classes into the symbol feeder.
Call this before building the alphabet so the collector's reqs are populated. AST-agnostic;
the feeder is obtained from SharedCompilationContext.Collector().
*/
func regulaCollectSymbols[TObs any](n *RegulaAST[TObs], feeder symbolFeeder[TObs]) {
	if n == nil {
		return
	}

	switch n.kind {
	case EXPRESSION_LITERAL:
		for _, v := range n.literals {
			feeder.addLiteral(v)
		}

	case EXPRESSION_CLASS:
		feeder.addClass(n.class)

	case EXPRESSION_CONCAT, EXPRESSION_UNION:
		regulaCollectSymbols(n.left, feeder)
		regulaCollectSymbols(n.right, feeder)

	case EXPRESSION_REPEAT, EXPRESSION_CAPTURE:
		regulaCollectSymbols(n.sub, feeder)
	}
}

/*
regulaBindIDs walks a Regula AST and assigns logical symbol IDs and position IDs into each node.
Uses the same feeder so logical IDs are stable and deduplicated. nextPos is incremented
for each symbol occurrence and must be shared across the full tree walk.
*/
func regulaBindIDs[TObs any](n *RegulaAST[TObs], feeder symbolFeeder[TObs], nextPos *positionID) {
	if n == nil {
		return
	}

	switch n.kind {

	case EXPRESSION_LITERAL:
		l := len(n.literals)

		n.literalSymIDs = make([]logicalID, l)
		n.literalPosIDs = make([]positionID, l)

		for i, v := range n.literals {
			n.literalSymIDs[i] = feeder.addLiteral(v)

			*nextPos++
			n.literalPosIDs[i] = *nextPos
		}

		n.literalBound = true

	case EXPRESSION_CLASS:
		n.classSymID = feeder.addClass(n.class)

		*nextPos++
		n.classPosID = *nextPos

		n.classBound = true

	case EXPRESSION_CONCAT, EXPRESSION_UNION:
		regulaBindIDs(n.left, feeder, nextPos)
		regulaBindIDs(n.right, feeder, nextPos)

	case EXPRESSION_REPEAT, EXPRESSION_CAPTURE:
		regulaBindIDs(n.sub, feeder, nextPos)
	}
}

// -------------------------------------------------------------- TEMPLATES

/*
============================================================
TEMPLATES (ASCII / rune-based)
============================================================

This file defines high-level, reusable Regula pattern templates.

Important:
- These templates are *ASCII/rune-oriented*. They intentionally depend on
  rune literals like 'a', '0', '\n', etc.
- Therefore, they cannot be generic over arbitrary observation types.
  We enforce this by constraining TObservation to be rune-compatible.

If you need templates for other alphabets (e.g. byte-based, token-based),
define separate template sets with appropriate constraints and constants.
*/

/*
RegulaTemplates provides factory-bound, reusable patterns for common ASCII constructs.

Design:
  - All templates are constructed through RegulaASTFactory to ensure the factory’s
    invariants apply (range normalization, comparator semantics, future adjacency rules).
  - TObservation is constrained to be rune-compatible, because these templates use
    rune literals ('a', '0', '\n', etc.).

Type constraints:
- `~rune` means the underlying type is compatible with rune (alias of int32).
*/
type RegulaTemplates[TObservation ~rune] struct {
	f *RegulaASTFactory[TObservation]
}

/*
RegulaTemplatesCreate constructs a template set bound to a specific Regula AST factory.

Why factory-bound:
- Prevents bypassing normalization and comparator logic
- Keeps template construction consistent with the rest of the Regula pipeline

Panics:
- Panics if f is nil, because templates cannot function without a factory.
*/
func RegulaTemplatesCreate[TObservation ~rune](
	f *RegulaASTFactory[TObservation],
) *RegulaTemplates[TObservation] {
	if f == nil {
		panic("RegulaTemplatesCreate: nil factory")
	}
	return &RegulaTemplates[TObservation]{f: f}
}

/*
Digit matches a single ASCII digit: [0-9].

Use cases:
- Integers / numeric literals
- Version components
- Lexical tokenization for numbers

Time:  O(1) to build node (range normalization is trivial here)
Space: O(1)
*/
func (t *RegulaTemplates[TObservation]) Digit() RegulaAST[TObservation] {
	return t.f.Class(
		t.f.Range(TObservation('0'), TObservation('9')),
	)
}

/*
Lower matches a single ASCII lowercase letter: [a-z].

Time:  O(1)
Space: O(1)
*/
func (t *RegulaTemplates[TObservation]) Lower() RegulaAST[TObservation] {
	return t.f.Class(
		t.f.Range(TObservation('a'), TObservation('z')),
	)
}

/*
Upper matches a single ASCII uppercase letter: [A-Z].

Time:  O(1)
Space: O(1)
*/
func (t *RegulaTemplates[TObservation]) Upper() RegulaAST[TObservation] {
	return t.f.Class(
		t.f.Range(TObservation('A'), TObservation('Z')),
	)
}

/*
Alpha matches a single ASCII letter: [a-zA-Z].

Time:  O(1)
Space: O(1)
*/
func (t *RegulaTemplates[TObservation]) Alpha() RegulaAST[TObservation] {
	return t.f.Class(
		t.f.Range(TObservation('a'), TObservation('z')),
		t.f.Range(TObservation('A'), TObservation('Z')),
	)
}

/*
Alnum matches a single ASCII alphanumeric: [a-zA-Z0-9].

Time:  O(1)
Space: O(1)
*/
func (t *RegulaTemplates[TObservation]) Alnum() RegulaAST[TObservation] {
	return t.f.Class(
		t.f.Range(TObservation('a'), TObservation('z')),
		t.f.Range(TObservation('A'), TObservation('Z')),
		t.f.Range(TObservation('0'), TObservation('9')),
	)
}

/*
Word matches a single ASCII “word” character: [a-zA-Z0-9_].

This matches the conventional lexer definition for identifiers.

Time:  O(1)
Space: O(1)
*/
func (t *RegulaTemplates[TObservation]) Word() RegulaAST[TObservation] {
	return t.f.Class(
		t.f.Range(TObservation('a'), TObservation('z')),
		t.f.Range(TObservation('A'), TObservation('Z')),
		t.f.Range(TObservation('0'), TObservation('9')),
		t.f.Range(TObservation('_'), TObservation('_')),
	)
}

/*
Whitespace matches a single ASCII whitespace character: [ \t\n\r].

Note:
- This is “common whitespace” for many DSLs.
- If you need vertical tab, form feed, Unicode spaces, etc., define a separate template.

Time:  O(1)
Space: O(1)
*/
func (t *RegulaTemplates[TObservation]) Whitespace() RegulaAST[TObservation] {
	return t.f.Class(
		t.f.Range(TObservation(' '), TObservation(' ')),
		t.f.Range(TObservation('\t'), TObservation('\t')),
		t.f.Range(TObservation('\n'), TObservation('\n')),
		t.f.Range(TObservation('\r'), TObservation('\r')),
	)
}

/*
Identifier matches a conventional ASCII identifier:

	start = [a-zA-Z_]
	rest  = [a-zA-Z0-9_]*

So the full pattern is:

	start rest

Time:  O(1) to build AST (node count constant)
Space: O(1)

Semantics:
- Does not accept empty input
- Does not accept leading digit
*/
func (t *RegulaTemplates[TObservation]) Identifier() RegulaAST[TObservation] {
	start := t.f.Class(
		t.f.Range(TObservation('a'), TObservation('z')),
		t.f.Range(TObservation('A'), TObservation('Z')),
		t.f.Range(TObservation('_'), TObservation('_')),
	)

	rest := t.Word().Star()

	return start.Then(rest)
}

/*
Integer matches one or more ASCII digits:

	[0-9]+

Time:  O(1)
Space: O(1)

Semantics:
- Does not accept empty input
*/
func (t *RegulaTemplates[TObservation]) Integer() RegulaAST[TObservation] {
	return t.Digit().Plus()
}

/*
SignedInteger matches an optional sign followed by an integer:

	[+-]?[0-9]+

Implementation note:
- We build the sign set directly via Class/Range to avoid relying on rune-only helpers.

Time:  O(1)
Space: O(1)
*/
func (t *RegulaTemplates[TObservation]) SignedInteger() RegulaAST[TObservation] {
	sign := t.f.Class(
		t.f.Range(TObservation('+'), TObservation('+')),
		t.f.Range(TObservation('-'), TObservation('-')),
	).Optional()

	return sign.Then(t.Integer())
}
