package pattern

import (
	"autarch"
	"foundation/domain"
	"foundation/hash"
)

/* AnnotatedOutcome represents an outcome mapped with annotation ID. */
type AnnotatedOutcome[TOutcome any] struct {
	Value      TOutcome
	Annotation *AnnotationID
}

/*
PatternCompilationInstruction represents a single pattern compilation request.

Each instruction pairs an AST with the semantic outcome that should be
produced when the resulting NFA accepts.

Instructions are compiled together in a single shared compilation context,
ensuring:

  - one unified alphabet
  - stable logical and physical symbol IDs
  - consistent indexing across all generated NFAs

This batching model is required for lexer construction, where multiple token
patterns must operate over the same symbol space.
*/
type PatternCompilationInstruction[TObservation, TOutcome, TPattern any] struct {
	/*
		Pattern is the AST describing the pattern to compile.
	*/
	Pattern *TPattern

	/*
		Outcome is the semantic value associated with the accepting state
		of the compiled pattern (e.g. token type, rule ID, payload, etc.).
	*/
	Outcome TOutcome
}

/*
SharedCompilationContext is a node-agnostic compilation context for pattern compilation.

It holds the unified symbol collector, alphabet, expander, and indexer. TPattern is the AST type
you will prepare (e.g. *RegulaAST[TObs] for Regula). Compilers call
ctx.fullPrepare(patterns, collectSymbols, bindIDs, bindState) with pattern-specific callbacks.
Create the context with the same TPattern as the compiler (e.g. CreateSharedCompilationContext[TObs, *RegulaAST[TObs]](...) for Regula).

Use cases:
- Compiling multiple Regula patterns with shared symbol IDs
- Building lexers where all rules in a state share one alphabet
- Reusing one context for mixed or future AST types
*/
type SharedCompilationContext[TObs, TPattern any] struct {
	observationDomain *domain.DiscreteDomain[TObs]

	collector *symbolCollector[TObs]

	alphabet                []autarch.SymbolDefinition[TObs]
	expander                symbolExpander
	deterministicResolver   autarch.DeterministicSymbolResolver[TObs]
	nondeterministicResolve autarch.NondeterministicSymbolResolver[TObs]
}

/*
CreateSharedCompilationContext creates an empty shared compilation context.
TPattern must match the AST type you will pass to fullPrepare (e.g. *RegulaAST[TObs] for Regula).
Compilers then call ctx.fullPrepare(patterns, collectSymbols, bindIDs, bindState) with the appropriate callbacks.
*/
func CreateSharedCompilationContext[TObservation, TPattern any](
	observationDomain *domain.DiscreteDomain[TObservation],
	formatter ObservationFormatter[TObservation],
) *SharedCompilationContext[TObservation, TPattern] {
	return &SharedCompilationContext[TObservation, TPattern]{
		observationDomain: observationDomain,
		collector:         newSymbolCollector(formatter),
	}
}

/*
getCollector returns the symbol feeder for this context. Pass it to AST-specific collect and bind
functions (e.g. regulaCollectSymbols, regulaBindIDs).
*/
func (ctx *SharedCompilationContext[TObs, TPattern]) getCollector() symbolFeeder[TObs] {
	return ctx.collector
}

/*
buildAlphabet builds the unified alphabet and indexer from all symbols collected so far.
Call once after all patterns have been fed via Collector(). Required before compiling any pattern.
*/
func (ctx *SharedCompilationContext[TObs, TPattern]) buildAlphabet() {
	result := buildAlphabet(
		ctx.collector.reqs,
		ctx.observationDomain,
	)

	ctx.alphabet = result.Definitions
	ctx.expander = newSymbolExpander(result.Mapping)
	ctx.deterministicResolver = result.DeterministicResolver
	ctx.nondeterministicResolve = result.NondeterministicResolve
}

/*
fullPrepare collects symbols from all patterns, builds the alphabet, then binds IDs into each pattern.
collectSymbols is called for each pattern with the context's feeder; then buildAlphabet runs once;
then bindIDs is called for each pattern with the same feeder and shared bindState. Use bindState
for AST-specific state (e.g. Regula passes *positionID so position IDs are unique across the batch).
*/
func (ctx *SharedCompilationContext[TObs, TPattern]) fullPrepare(
	patterns []*TPattern,
	collectSymbols func(pattern *TPattern, feeder symbolFeeder[TObs]),
	bindIDs func(pattern *TPattern, feeder symbolFeeder[TObs], bindState interface{}),
	bindState interface{},
) {
	feeder := ctx.getCollector()
	for _, pattern := range patterns {
		collectSymbols(pattern, feeder)
	}
	ctx.buildAlphabet()
	for _, pattern := range patterns {
		bindIDs(pattern, feeder, bindState)
	}
}

func SharedCompilationContextDeterministicResolverGet[TObs, TPattern any](
	ctx *SharedCompilationContext[TObs, TPattern],
) autarch.DeterministicSymbolResolver[TObs] {
	return ctx.deterministicResolver
}

func SharedCompilationContextNondeterministicResolverGet[TObs, TPattern any](
	ctx *SharedCompilationContext[TObs, TPattern],
) autarch.NondeterministicSymbolResolver[TObs] {
	return ctx.nondeterministicResolve
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

func hashRanges[T any](rs []CharRange[T], byteExtractorMany func([]T) []byte) uint64 {
	included := []T{}
	for _, r := range rs {
		included = append(included, r.Lo, r.Hi)
	}
	return hash.XXH3HasherHash64(xxh3Hasher, byteExtractorMany(included))
}
