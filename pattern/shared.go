package pattern

import (
	"autarch"
	"foundation/hash"
)

/*
RegulaNFAInstruction represents a single pattern compilation request.

Each instruction pairs a Regula AST with the semantic outcome that should be
produced when the resulting NFA accepts.

Instructions are compiled together in a single shared compilation context,
ensuring:

  - one unified alphabet
  - stable logical and physical symbol IDs
  - consistent indexing across all generated NFAs

This batching model is required for lexer construction, where multiple token
patterns must operate over the same symbol space.
*/
type RegulaNFAInstruction[TObservation any, TOutcome comparable] struct {
	/*
		Pattern is the Regula AST describing the pattern to compile.
	*/
	Pattern *RegulaAST[TObservation]

	/*
		Outcome is the semantic value associated with the accepting state
		of the compiled NFA (e.g. token type, rule ID, payload, etc.).
	*/
	Outcome TOutcome
}

/*
SuccessorFn defines how to get the next discrete value in the observation space.
Example for rune: func(r rune) rune { return r + 1 }
Example for float: return nil (or a fn that indicates no discrete successor)

If this returns a value equal to 'next', the interval (curr, next) is considered empty.
*/
type SuccessorFn[T any] func(curr T) (next T, exists bool)

/*
RegulaSharedCompilationContext represents a shared compilation context for multiple patterns.
This allows multiple patterns to be compiled with consistent symbol IDs, which is required
for proper lexer compilation where all rules in a state must share one alphabet.

Use cases:
- Compiling multiple patterns with shared symbol IDs for lexer rulesets
- Building lexers where all rules in a state share one alphabet
- Ensuring symbol coherence across multiple pattern compilations

The context should be created once per lexer state, then used to compile all patterns
in that state.
*/
type RegulaSharedCompilationContext[TObs any] struct {
	successorFn SuccessorFn[TObs]
	cmpFn       func(a, b TObs) int

	collector *symbolCollector[TObs]

	alphabet []autarch.SymbolDefinition[TObs]
	expander symbolExpander
	indexer  autarch.SymbolIndexer[TObs]
}

/*
RegulaCreateSharedCompilationContext creates a shared compilation context for multiple patterns.
All patterns should be collected into this context before building the alphabet, then each
pattern can be compiled using RegulaCompileToNFAWithBuilder.

Use cases:
- Setting up lexer state compilation with shared symbols
- Preparing context for compiling multiple patterns with consistent symbol IDs

Time complexity: O(1) - just creates the context
Space complexity: O(1) - context structure only

Prerequisites:
- None (empty context)

Edge cases:
- Context must have patterns collected before building alphabet
- Alphabet should be built once after all patterns are collected
*/
func RegulaCreateSharedCompilationContext[TObservation any](
	successorFn SuccessorFn[TObservation],
	cmpFn func(a, b TObservation) int,
	formatter ObservationFormatter[TObservation],
) *RegulaSharedCompilationContext[TObservation] {
	return &RegulaSharedCompilationContext[TObservation]{
		successorFn: successorFn,
		cmpFn:       cmpFn,
		collector:   newSymbolCollector(formatter),
	}
}

/*
CollectPattern collects symbols from a pattern AST into the shared context.
This should be called for all patterns before building the alphabet.

Use cases:
- Collecting symbols from multiple patterns into shared context
- Preparing for unified alphabet construction

Time complexity: O(n) where n is nodes in the AST
Space complexity: O(s) where s is unique symbols collected

Prerequisites:
- context must be a valid shared compilation context
- pattern must be a valid RegulaAST

Edge cases:
- Can be called multiple times for different patterns
- Symbols are deduplicated automatically
*/
func (ctx *RegulaSharedCompilationContext[TObservation]) collectPattern(pattern *RegulaAST[TObservation]) {
	ctx.collector.collect(pattern)
}

/*
buildAlphabet builds the unified alphabet and indexer from all collected patterns.
This should be called once after all patterns have been collected.

Use cases:
- Finalizing the shared alphabet after pattern collection
- Preparing for pattern compilation with shared symbols

Time complexity: O(s) where s is number of unique symbols
Space complexity: O(s) for alphabet storage

Prerequisites:
- At least one pattern should have been collected
- Should be called once after all CollectPattern calls

Edge cases:
- Empty alphabet if no patterns collected
- Alphabet is deduplicated automatically
*/
func (ctx *RegulaSharedCompilationContext[TObs]) buildAlphabet() {
	result := buildAlphabet(
		ctx.collector.reqs,
		func(a, b TObs) bool {
			return ctx.cmpFn(a, b) < 0
		},
		ctx.successorFn,
	)

	ctx.alphabet = result.Definitions
	ctx.expander = newSymbolExpander(result.Mapping)
	ctx.indexer = autarch.SymbolIndexerBuild(ctx.alphabet)
}

func (ctx *RegulaSharedCompilationContext[TObs]) bindLogicalIDs(pattern *RegulaAST[TObs]) {
	bindLogicalIDs(pattern, ctx.collector)
}

func (ctx *RegulaSharedCompilationContext[TObs]) fullPrepare(patterns []*RegulaAST[TObs]) {
	for _, pattern := range patterns {
		ctx.collector.collect(pattern)
	}

	ctx.buildAlphabet()

	for _, pattern := range patterns {
		ctx.bindLogicalIDs(pattern)
	}
}

//
// ============================================================
// HASH HELPERS
// ============================================================
//

var xxh3Hasher = hash.XXH3HasherCreateWithSeed(42)

func hashValue[T any](v T, byteExtractor func(T) []byte) uint64 {
	return hash.XXH3HasherHash64(xxh3Hasher, byteExtractor(v))
}

func hashRanges[T any](rs []charRange[T], byteExtractorMany func([]T) []byte) uint64 {
	included := []T{}
	for _, r := range rs {
		included = append(included, r.lo, r.hi)
	}
	return hash.XXH3HasherHash64(xxh3Hasher, byteExtractorMany(included))
}
