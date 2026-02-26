package pattern

// --------------------------------------------------------------- TYPES

// VistraKind discriminates the kind of a VistraAST node (atom, class, concat, union, repeat, nest, empty).
//
//go:generate stringer -type VistraKind
type VistraKind uint8

const (
	VistraAtom VistraKind = iota + 1
	VistraClass
	VistraConcat
	VistraUnion
	VistraRepeat
	VistraNest
	VistraEmpty
)

// --------------------------------------------------------------- VISTRA

/*
VistraAST is an abstract syntax tree for structural patterns with nesting and repetition.
Leaf atoms are either a literal sequence (VistraAtom) or a character class (VistraClass).
Other nodes: concatenation, union, repeat, nest (push/pop stack symbols), mode, empty.
Used as a DSL to build patterns that can be compiled to automata (e.g. DPDA) with explicit stack behaviour.

Two phases:
  - Raw: observations stored in the AST (symbols, callSymbols, returnSymbols, class). Used by the factory and before binding.
  - Bound: symbol IDs stored (symbolIDs, callSymbolIDs, returnSymbolIDs, classSymID). Used by the compiler after a binding pass.

Do not use bound fields until a binding pass has run. Pipeline: Raw AST → (shared symbol collection + alphabet build) → Bind IDs into AST → Bound AST → Automaton (e.g. DPDA).
*/
type VistraAST[TObservation any] struct {
	kind         VistraKind
	data         any // concrete payload type
	annotationID *AnnotationID
}

type vistraAtomData[TObservation any] struct {
	rawSymbols []TObservation
	symbolIDs  []logicalID
}

type vistraClassData[TObservation any] struct {
	rawClass   charClass[TObservation]
	classSymID logicalID
}

type vistraBinaryData[TObservation any] struct {
	left, right *VistraAST[TObservation]
}

type vistraRepeatData[TObservation any] struct {
	sub      *VistraAST[TObservation]
	min, max int
}

type vistraNestData[TObservation any] struct {
	rawCallSymbols   []TObservation
	rawReturnSymbols []TObservation
	callSymbolIDs    []logicalID
	returnSymbolIDs  []logicalID
	body             *VistraAST[TObservation]
}

/* WithAnnotationID sets the annotation id for this node. */
func (v *VistraAST[TObservation]) WithAnnotationID(id AnnotationID) *VistraAST[TObservation] {
	v.annotationID = &id
	return v
}

/*
	Then concatenates two patterns, matching the left pattern followed by the right pattern.

The resulting pattern matches input sequences where the first part matches v and the second
part matches b, in that order. This is equivalent to the concatenation operator in regular
expressions (e.g., "ab" matches pattern "a" then "b").

Use cases:
- Building sequential patterns (e.g., keyword followed by identifier)
- Constructing structured formats (e.g., prefix + body + suffix)
- Creating multi-part token patterns

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- Both v and b must be valid VistraAST patterns

Edge cases:
- Concatenating empty patterns (epsilon) with other patterns is supported
- The resulting pattern maintains references to the original patterns
*/
func (v VistraAST[TObservation]) Then(b VistraAST[TObservation]) VistraAST[TObservation] {
	return VistraAST[TObservation]{
		kind: VistraConcat,
		data: vistraBinaryData[TObservation]{
			left:  &v,
			right: &b,
		},
	}
}

/*
	Or creates an alternation pattern that matches either the left or right pattern.

The resulting pattern matches input that matches either v or b. This is equivalent to the
alternation operator in regular expressions (e.g., "a|b" matches either "a" or "b").

Use cases:
- Creating keyword alternatives (e.g., "if" or "else" or "while")
- Building flexible token patterns with multiple valid forms
- Implementing optional variations in pattern matching

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- Both v and b must be valid VistraAST patterns

Edge cases:
- If both patterns can match the same input, the NFA will accept both paths
- The resulting pattern maintains references to the original patterns
*/
func (v VistraAST[TObservation]) Or(b VistraAST[TObservation]) VistraAST[TObservation] {
	return VistraAST[TObservation]{
		kind: VistraUnion,
		data: vistraBinaryData[TObservation]{
			left:  &v,
			right: &b,
		},
	}
}

// vistraRepeat builds a VistraRepeat node with the given sub pattern and min/max bounds (-1 for unbounded max).
func vistraRepeat[TObservation any](sub VistraAST[TObservation], min, max int) VistraAST[TObservation] {
	return VistraAST[TObservation]{
		kind: VistraRepeat,
		data: vistraRepeatData[TObservation]{
			sub: &sub,
			min: min,
			max: max,
		},
	}
}

/*
	Star creates a Kleene star pattern that matches zero or more repetitions of the pattern.

The resulting pattern matches any number of repetitions of v, including zero. This is
equivalent to the Kleene star operator in regular expressions (e.g., "a*" matches "", "a", "aa", "aaa", etc.).

Use cases:
- Matching variable-length sequences (e.g., whitespace, digits, identifiers)
- Creating optional repeated patterns
- Building patterns that accept empty input

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- v must be a valid VistraAST pattern

Edge cases:
- Matches empty input (zero repetitions)
- Matches infinite repetitions (no upper bound)
- The resulting pattern maintains a reference to the original pattern
*/
func (v VistraAST[TObservation]) Star() VistraAST[TObservation] {
	return vistraRepeat(v, 0, -1)
}

/*
	Plus creates a pattern that matches one or more repetitions of the pattern.

The resulting pattern matches one or more repetitions of v. This is equivalent to the
plus operator in regular expressions (e.g., "a+" matches "a", "aa", "aaa", etc., but not "").

Use cases:
- Matching required sequences (e.g., at least one digit, at least one letter)
- Building patterns that require non-empty input
- Creating mandatory repeated patterns

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- v must be a valid VistraAST pattern

Edge cases:
- Does not match empty input (requires at least one repetition)
- Matches infinite repetitions (no upper bound)
- The resulting pattern maintains a reference to the original pattern
*/
func (v VistraAST[TObservation]) Plus() VistraAST[TObservation] {
	return vistraRepeat(v, 1, -1)
}

/*
	Optional creates a pattern that matches zero or one repetition of the pattern.

The resulting pattern matches either zero or one occurrence of v. This is equivalent to
the question mark operator in regular expressions (e.g., "a?" matches "" or "a").

Use cases:
- Creating optional components in patterns (e.g., optional sign in numbers)
- Building patterns with optional prefixes or suffixes
- Implementing optional whitespace or separators

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- v must be a valid VistraAST pattern

Edge cases:
- Matches empty input (zero repetitions)
- Matches exactly one repetition
- The resulting pattern maintains a reference to the original pattern
*/
func (v VistraAST[TObservation]) Optional() VistraAST[TObservation] {
	return vistraRepeat(v, 0, 1)
}

/*
	Repeat creates a pattern that matches a bounded number of repetitions of the pattern.

The resulting pattern matches between min and max repetitions of v (inclusive). If max is -1,
there is no upper bound. This is equivalent to bounded repetition in regular expressions
(e.g., "a{2,5}" matches "aa", "aaa", "aaaa", or "aaaaa").

Use cases:
- Matching fixed-length sequences (e.g., exactly 4 digits for years)
- Creating bounded repetition patterns (e.g., 1-3 digits for day numbers)
- Building patterns with minimum requirements (e.g., at least 8 characters)

Time complexity: O(1) - creates a new AST node
Space complexity: O(1) - constant memory overhead

Prerequisites:
- v must be a valid VistraAST pattern
- min must be >= 0
- max must be >= min or -1 (unbounded)

Edge cases:
- Panics if max != -1 and max < min
- If min == 0, matches empty input
- If max == -1, matches infinite repetitions
- The resulting pattern maintains a reference to the original pattern
*/
func (v VistraAST[TObservation]) Repeat(min, max int) VistraAST[TObservation] {
	if max != -1 && max < min {
		panic("invalid repeat bounds")
	}
	return vistraRepeat(v, min, max)
}

// --------------------------------------------------------------- FACTORY

// VistraASTFactory encapsulates the factory for constructing Vistra AST nodes.
// Use Nest to pass call/return symbols for push/pop stack behaviour.
type VistraASTFactory[TObservation any] struct {
	cmpFn func(a, b TObservation) int
}

/*
VistraASTFactoryCreate constructs a Vistra AST factory.
cmpFn is used for future Class/ranges support and keeps the API aligned with Regula.
*/
func VistraASTFactoryCreate[TObservation any](
	cmpFn func(a, b TObservation) int,
) *VistraASTFactory[TObservation] {
	return &VistraASTFactory[TObservation]{
		cmpFn: cmpFn,
	}
}

/*
	Empty returns a pattern that matches the empty input (epsilon).

Use cases:
- Building optional or nullable sub-patterns
- Base case for recursive or balanced trees

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- None

Edge cases:
- Matches only the empty sequence; no observations consumed
*/
func (f *VistraASTFactory[TObservation]) Empty() VistraAST[TObservation] {
	return VistraAST[TObservation]{kind: VistraEmpty}
}

/*
	Literal creates a pattern that matches a specific sequence of observations.

The resulting pattern matches input that exactly matches the provided sequence of values
in order. Literal("a","b") means the sequence "ab", not "a|b". Empty values list matches empty input (epsilon).

Use cases:
- Matching exact sequences (e.g., keywords, fixed strings)
- Building patterns for specific token values

Time complexity: O(1) - creates an AST node
Space complexity: O(n) where n is the number of values

Prerequisites:
- None

Edge cases:
- Empty values list creates a pattern matching empty input (epsilon)
- Single value creates a pattern matching exactly that observation
*/
func (f *VistraASTFactory[TObservation]) Literal(values ...TObservation) VistraAST[TObservation] {
	symbols := make([]TObservation, len(values))
	copy(symbols, values)
	return VistraAST[TObservation]{
		kind: VistraAtom,
		data: vistraAtomData[TObservation]{
			rawSymbols: symbols,
		},
	}
}

/*
	Range creates a character range for use in character class patterns.

Same semantics as Regula: lo and hi inclusive. Use with Class to build class patterns.
*/
func (f *VistraASTFactory[TObservation]) Range(lo, hi TObservation) charRange[TObservation] {
	return charRange[TObservation]{lo: lo, hi: hi}
}

/*
	Class creates a character class pattern that matches any observation within the specified ranges.

Same semantics as Regula: ranges are normalized (sorted and merged). Empty ranges list matches nothing.
*/
func (f *VistraASTFactory[TObservation]) Class(ranges ...charRange[TObservation]) VistraAST[TObservation] {
	normalized := normalizeRanges(ranges, f.cmpFn)
	return VistraAST[TObservation]{
		kind: VistraClass,
		data: vistraClassData[TObservation]{
			rawClass: charClass[TObservation]{ranges: normalized},
		},
	}
}

/*
	Sequence creates a concatenation pattern from multiple expressions in order.

The resulting pattern matches input sequences where each part matches the corresponding
expression in order. Equivalent to concatenating multiple patterns with Then.

Use cases:
- Building multi-part patterns (e.g., prefix + body + suffix)
- Creating structured formats with multiple required components

Time complexity: O(n) where n is the number of expressions
Space complexity: O(n) - creates intermediate AST nodes

Prerequisites:
- At least one expression must be provided

Edge cases:
- Panics if no expressions are provided
- Single expression returns that expression unchanged
*/
func (f *VistraASTFactory[TObservation]) Sequence(expressions ...VistraAST[TObservation]) VistraAST[TObservation] {
	if len(expressions) == 0 {
		panic("empty sequence")
	}
	return buildBalancedTree(expressions, func(l, r VistraAST[TObservation]) VistraAST[TObservation] {
		return l.Then(r)
	})
}

/*
	AnyOf creates an alternation pattern from multiple expressions.

The resulting pattern matches input that matches any of the provided expressions.
Equivalent to creating alternations with Or.

Use cases:
- Creating keyword alternatives (e.g., "if", "else", "while")
- Building flexible token patterns with multiple valid forms

Time complexity: O(n) where n is the number of expressions
Space complexity: O(n) - creates intermediate AST nodes

Prerequisites:
- At least one expression must be provided

Edge cases:
- Panics if no expressions are provided
- Single expression returns that expression unchanged
*/
func (f *VistraASTFactory[TObservation]) AnyOf(expressions ...VistraAST[TObservation]) VistraAST[TObservation] {
	if len(expressions) == 0 {
		panic("empty alternation")
	}
	return buildBalancedTree(expressions, func(l, r VistraAST[TObservation]) VistraAST[TObservation] {
		return l.Or(r)
	})
}

/*
	Nest creates a nested pattern with explicit push/pop stack behaviour.

callSymbols are the observations that trigger a stack push (e.g. open bracket);
returnSymbols are the observations that trigger a stack pop (e.g. close bracket).
body is the pattern matched inside the nested scope. The client or a downstream
compiler (e.g. to a DPDA) uses these symbol sets to drive stack operations.

Use cases:
- Parsing nested structures (parentheses, brackets, blocks)
- Building context-free patterns with explicit call/return symbols

Time complexity: O(1) - creates an AST node
Space complexity: O(len(callSymbols)+len(returnSymbols)) - stores copies

Prerequisites:
- body must be a valid VistraAST pattern

Edge cases:
- callSymbols and returnSymbols may be empty or nil (stored as copied slices)
- The resulting pattern maintains a reference to body
*/
func (f *VistraASTFactory[TObservation]) Nest(
	callSymbols, returnSymbols []TObservation,
	body VistraAST[TObservation],
) VistraAST[TObservation] {
	call := make([]TObservation, len(callSymbols))
	copy(call, callSymbols)
	ret := make([]TObservation, len(returnSymbols))
	copy(ret, returnSymbols)
	return VistraAST[TObservation]{
		kind: VistraNest,
		data: vistraNestData[TObservation]{
			rawCallSymbols:   call,
			rawReturnSymbols: ret,
			body:             &body,
		},
	}
}

/*
	Star creates a Kleene star pattern that matches zero or more repetitions of sub.

Use cases:
- Matching variable-length sequences
- Optional repeated sub-patterns

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- sub must be a valid VistraAST pattern

Edge cases:
- Matches empty input (zero repetitions); no upper bound on repetitions
*/
func (f *VistraASTFactory[TObservation]) Star(sub VistraAST[TObservation]) VistraAST[TObservation] {
	return vistraRepeat(sub, 0, -1)
}

/*
	Plus creates a pattern that matches one or more repetitions of sub.

Use cases:
- Matching required repeated sequences
- At least one occurrence of sub

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- sub must be a valid VistraAST pattern

Edge cases:
- Does not match empty input; no upper bound on repetitions
*/
func (f *VistraASTFactory[TObservation]) Plus(sub VistraAST[TObservation]) VistraAST[TObservation] {
	return vistraRepeat(sub, 1, -1)
}

/*
	Optional creates a pattern that matches zero or one repetition of sub.

Use cases:
- Optional components (e.g. optional sign or suffix)
- Zero or one occurrence of sub

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- sub must be a valid VistraAST pattern

Edge cases:
- Matches empty input or exactly one repetition of sub
*/
func (f *VistraASTFactory[TObservation]) Optional(sub VistraAST[TObservation]) VistraAST[TObservation] {
	return vistraRepeat(sub, 0, 1)
}

/*
	Repeat creates a pattern that matches between min and max repetitions of sub (inclusive).

If max is -1, there is no upper bound.

Use cases:
- Bounded repetition (e.g. exactly n, or between n and m)
- Fixed- or variable-length sequences with bounds

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- sub must be a valid VistraAST pattern
- min must be >= 0
- max must be >= min or -1 (unbounded)

Edge cases:
- Panics if max != -1 and max < min
- If min == 0, matches empty input; if max == -1, no upper bound
*/
func (f *VistraASTFactory[TObservation]) Repeat(sub VistraAST[TObservation], min, max int) VistraAST[TObservation] {
	if max != -1 && max < min {
		panic("invalid repeat bounds")
	}
	return vistraRepeat(sub, min, max)
}

// ------------------------------------------------------- VISTRA SYMBOL COLLECTION & BINDING

/*
vistraCollectSymbols walks a Vistra AST and feeds all literals and classes into the symbol feeder.
Pass SharedCompilationContext.Collector() so the same context can be used for Regula and Vistra.
*/
func vistraCollectSymbols[TObs any](n *VistraAST[TObs], feeder symbolFeeder[TObs]) {
	if n == nil {
		return
	}

	switch n.kind {
	case VistraAtom:
		d := n.data.(vistraAtomData[TObs])
		for _, v := range d.rawSymbols {
			feeder.addLiteral(v)
		}

	case VistraClass:
		d := n.data.(vistraClassData[TObs])
		feeder.addClass(d.rawClass)

	case VistraConcat, VistraUnion:
		d := n.data.(vistraBinaryData[TObs])
		vistraCollectSymbols(d.left, feeder)
		vistraCollectSymbols(d.right, feeder)

	case VistraRepeat:
		d := n.data.(vistraRepeatData[TObs])
		vistraCollectSymbols(d.sub, feeder)

	case VistraNest:
		d := n.data.(vistraNestData[TObs])
		for _, v := range d.rawCallSymbols {
			feeder.addLiteral(v)
		}
		for _, v := range d.rawReturnSymbols {
			feeder.addLiteral(v)
		}
		vistraCollectSymbols(d.body, feeder)

	case VistraEmpty:
	}
}

/*
vistraBindIDs walks a Vistra AST and assigns logical symbol IDs into each node.
Call after VistraCollectSymbols and after the shared alphabet has been built (ctx.BuildAlphabet()).
Vistra does not use position IDs; only symbol IDs are bound.
*/
func vistraBindIDs[TObs any](n *VistraAST[TObs], feeder symbolFeeder[TObs]) {
	if n == nil {
		return
	}

	switch n.kind {
	case VistraAtom:
		d := n.data.(vistraAtomData[TObs])
		ids := make([]logicalID, len(d.rawSymbols))
		for i, v := range d.rawSymbols {
			ids[i] = feeder.addLiteral(v)
		}
		n.data = vistraAtomData[TObs]{rawSymbols: d.rawSymbols, symbolIDs: ids}

	case VistraClass:
		d := n.data.(vistraClassData[TObs])
		n.data = vistraClassData[TObs]{
			rawClass:   d.rawClass,
			classSymID: feeder.addClass(d.rawClass),
		}

	case VistraConcat, VistraUnion:
		d := n.data.(vistraBinaryData[TObs])
		vistraBindIDs(d.left, feeder)
		vistraBindIDs(d.right, feeder)

	case VistraRepeat:
		d := n.data.(vistraRepeatData[TObs])
		vistraBindIDs(d.sub, feeder)
		n.data = d

	case VistraNest:
		d := n.data.(vistraNestData[TObs])
		callIDs := make([]logicalID, len(d.rawCallSymbols))
		for i, v := range d.rawCallSymbols {
			callIDs[i] = feeder.addLiteral(v)
		}
		returnIDs := make([]logicalID, len(d.rawReturnSymbols))
		for i, v := range d.rawReturnSymbols {
			returnIDs[i] = feeder.addLiteral(v)
		}
		vistraBindIDs(d.body, feeder)
		n.data = vistraNestData[TObs]{
			rawCallSymbols:   d.rawCallSymbols,
			rawReturnSymbols: d.rawReturnSymbols,
			callSymbolIDs:    callIDs,
			returnSymbolIDs:  returnIDs,
			body:             d.body,
		}

	case VistraEmpty:
	}
}
