package pattern

import (
	"autarch"
	"cmp"
	"fmt"
	"hash/fnv"
	"memarch"
)

// ============================================================
// PUBLIC ENTRY
// ============================================================

/*
RegulaCompileToNFA compiles a regula pattern AST into a Non-Deterministic Finite Automaton (NFA).

This implementation follows Thompson's construction while explicitly separating:

• language acceptance (accepting states)
• semantic payload (outcomes attached only to accepting states)

Non-accepting states carry no semantic meaning.

Use cases:
- Regular expression execution
- Lexer rule compilation
- Pattern recognizers
- Protocol parsers

Time complexity: O(n) where n is AST nodes
Space complexity: O(s + t) where s = states, t = transitions

Acceptance model:
- Exactly one accepting state is produced by Thompson construction
- Acceptance is stored explicitly (no sentinel outcomes)

Outcome model:
- Only accepting states carry semantic payloads
- Non-accepting states' outcome values are ignored

Epsilon transitions are used for composition and can be removed via DFA conversion.
*/
func RegulaCompileToNFA[TObservation cmp.Ordered, TOutcome comparable](
	alloc memarch.AllocationFn,
	root RegulaAST[TObservation],
	acceptOutcome TOutcome,
) *autarch.NFA[TObservation, TOutcome] {

	builder := newSymbolBuilder[TObservation]()
	builder.collect(&root)

	alphabet := builder.buildAlphabet()
	indexer := autarch.SymbolIndexerBuild(alphabet)

	c := newThompsonCompiler(builder)
	frag := c.compile(&root)

	numStates := c.nextState

	// Semantic outcomes (only meaningful for accepting states)
	outcomes := make([]TOutcome, numStates)

	// Explicit acceptance flags
	accepting := make([]bool, numStates)
	accepting[frag.accept] = true
	outcomes[frag.accept] = acceptOutcome

	return autarch.NFACreate(
		alloc,
		alphabet,
		c.transitions,
		c.epsilonEdges,
		[]uint64{frag.start},
		accepting,
		outcomes,
		indexer,
	)
}

/*
RegulaCompileToNFAWithBuilder compiles a regula AST into an NFA using a shared alphabet context.

This is required when compiling multiple lexer rules that must share symbol IDs.

Acceptance and semantic payloads are stored explicitly.

Use cases:
- Lexer construction with many rules
- Shared-alphabet automata
- Multi-pattern recognizers

Acceptance semantics:
- Thompson construction produces exactly one accepting state
- Acceptance is explicit (no sentinel values)

Only accepting states carry semantic outcomes.
*/
func RegulaCompileToNFAWithBuilder[TObservation cmp.Ordered, TOutcome comparable](
	alloc memarch.AllocationFn,
	root RegulaAST[TObservation],
	ctx *RegulaSharedCompilationContext[TObservation],
	acceptOutcome TOutcome,
) *autarch.NFA[TObservation, TOutcome] {

	c := newThompsonCompiler(ctx.builder)
	frag := c.compile(&root)

	numStates := c.nextState

	outcomes := make([]TOutcome, numStates)
	accepting := make([]bool, numStates)

	accepting[frag.accept] = true
	outcomes[frag.accept] = acceptOutcome

	return autarch.NFACreate(
		alloc,
		ctx.alphabet,
		c.transitions,
		c.epsilonEdges,
		[]uint64{frag.start},
		accepting,
		outcomes,
		ctx.indexer,
	)
}

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
type RegulaSharedCompilationContext[TObservation cmp.Ordered] struct {
	builder  *symbolBuilder[TObservation]
	alphabet []autarch.SymbolDefinition[TObservation]
	indexer  autarch.SymbolIndexer[TObservation]
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
func RegulaCreateSharedCompilationContext[TObservation cmp.Ordered]() *RegulaSharedCompilationContext[TObservation] {
	return &RegulaSharedCompilationContext[TObservation]{
		builder: newSymbolBuilder[TObservation](),
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
func (ctx *RegulaSharedCompilationContext[TObservation]) CollectPattern(pattern *RegulaAST[TObservation]) {
	ctx.builder.collect(pattern)
}

/*
BuildAlphabet builds the unified alphabet and indexer from all collected patterns.
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
func (ctx *RegulaSharedCompilationContext[TObservation]) BuildAlphabet() {
	ctx.alphabet = ctx.builder.buildAlphabet()
	ctx.indexer = autarch.SymbolIndexerBuild(ctx.alphabet)
}

//
// ============================================================
// SYMBOL BUILDER (dedup + stable IDs)
// ============================================================
//

type symbolBuilder[TObs cmp.Ordered] struct {
	nextID uint64
	keys   map[autarch.SymbolKey]uint64
	defs   []autarch.SymbolDefinition[TObs]
}

func newSymbolBuilder[TObs cmp.Ordered]() *symbolBuilder[TObs] {
	return &symbolBuilder[TObs]{
		keys: make(map[autarch.SymbolKey]uint64),
	}
}

func (b *symbolBuilder[TObs]) literal(v TObs) uint64 {
	h := hashValue(v)
	key := autarch.SymbolKey{Kind: autarch.SymbolKindLiteral, Hash: h}

	if id, ok := b.keys[key]; ok {
		return id
	}

	id := b.nextID
	b.nextID++

	b.keys[key] = id
	obs := v // Capture observation value for formatting
	b.defs = append(b.defs, autarch.SymbolDefinition[TObs]{
		ID:   id,
		Name: fmt.Sprintf("%v", v),
		Match: func(o TObs) bool {
			return o == v
		},
		Observation: &obs,
	})

	return id
}

func (b *symbolBuilder[TObs]) class(cls charClass[TObs]) uint64 {
	h := hashRanges(cls.ranges)
	key := autarch.SymbolKey{Kind: autarch.SymbolKindClass, Hash: h}

	if id, ok := b.keys[key]; ok {
		return id
	}

	id := b.nextID
	b.nextID++

	ranges := cls.ranges

	b.keys[key] = id
	b.defs = append(b.defs, autarch.SymbolDefinition[TObs]{
		ID:   id,
		Name: "class",
		Match: func(o TObs) bool {
			for _, r := range ranges {
				if o >= r.lo && o <= r.hi {
					return true
				}
			}
			return false
		},
	})

	return id
}

func (b *symbolBuilder[TObs]) collect(n *RegulaAST[TObs]) {
	if n == nil {
		return
	}

	switch n.kind {
	case EXPRESSION_LITERAL:
		for _, v := range n.literals {
			b.literal(v)
		}

	case EXPRESSION_CLASS:
		b.class(n.class)

	case EXPRESSION_CONCAT, EXPRESSION_UNION:
		b.collect(n.left)
		b.collect(n.right)

	case EXPRESSION_REPEAT:
		b.collect(n.sub)
	}
}

func (b *symbolBuilder[TObs]) buildAlphabet() []autarch.SymbolDefinition[TObs] {
	return b.defs
}

//
// ============================================================
// THOMPSON CORE
// ============================================================
//

type nfaFragment struct {
	start  uint64
	accept uint64
}

type thompsonCompiler[TObs cmp.Ordered] struct {
	nextState    uint64
	transitions  []autarch.Transition[TObs]
	epsilonEdges map[uint64][]uint64
	builder      *symbolBuilder[TObs]
}

func newThompsonCompiler[TObs cmp.Ordered](b *symbolBuilder[TObs]) *thompsonCompiler[TObs] {
	return &thompsonCompiler[TObs]{
		builder:      b,
		epsilonEdges: make(map[uint64][]uint64),
	}
}

func (c *thompsonCompiler[TObs]) newState() uint64 {
	s := c.nextState
	c.nextState++
	return s
}

func (c *thompsonCompiler[TObs]) newFrag() nfaFragment {
	return nfaFragment{c.newState(), c.newState()}
}

func (c *thompsonCompiler[TObs]) eps(a, b uint64) {
	c.epsilonEdges[a] = append(c.epsilonEdges[a], b)
}

func (c *thompsonCompiler[TObs]) symbol(a uint64, id uint64, b uint64) {
	c.transitions = append(c.transitions,
		autarch.Transition[TObs]{
			CurrentState: a,
			NextState:    b,
			Symbol:       autarch.SymbolCreate[TObs]("", id),
		},
	)
}

func (c *thompsonCompiler[TObs]) compile(n *RegulaAST[TObs]) nfaFragment {
	switch n.kind {

	case EXPRESSION_LITERAL:
		return c.literalSequence(n.literals)

	case EXPRESSION_CLASS:
		return c.class(n.class)

	case EXPRESSION_CONCAT:
		a := c.compile(n.left)
		b := c.compile(n.right)
		c.eps(a.accept, b.start)
		return nfaFragment{a.start, b.accept}

	case EXPRESSION_UNION:
		a := c.compile(n.left)
		b := c.compile(n.right)

		out := c.newFrag()
		c.eps(out.start, a.start)
		c.eps(out.start, b.start)
		c.eps(a.accept, out.accept)
		c.eps(b.accept, out.accept)
		return out

	case EXPRESSION_REPEAT:
		return c.repeat(n)

	default:
		panic("unknown regula node")
	}
}

func (c *thompsonCompiler[TObs]) literalSequence(vals []TObs) nfaFragment {
	if len(vals) == 0 {
		f := c.newFrag()
		c.eps(f.start, f.accept)
		return f
	}

	start := c.newState()
	cur := start

	for _, v := range vals {
		next := c.newState()
		id := c.builder.literal(v)
		c.symbol(cur, id, next)
		cur = next
	}

	return nfaFragment{start, cur}
}

func (c *thompsonCompiler[TObs]) class(cls charClass[TObs]) nfaFragment {
	f := c.newFrag()
	id := c.builder.class(cls)
	c.symbol(f.start, id, f.accept)
	return f
}

func (c *thompsonCompiler[TObs]) repeat(n *RegulaAST[TObs]) nfaFragment {

	if n.min == 0 && n.max == -1 {
		a := c.compile(n.sub)
		out := c.newFrag()

		c.eps(out.start, out.accept)
		c.eps(out.start, a.start)
		c.eps(a.accept, a.start)
		c.eps(a.accept, out.accept)

		return out
	}

	if n.min == 1 && n.max == -1 {
		a := c.compile(n.sub)
		out := c.newFrag()

		c.eps(out.start, a.start)
		c.eps(a.accept, a.start)
		c.eps(a.accept, out.accept)

		return out
	}

	if n.min == 0 && n.max == 1 {
		a := c.compile(n.sub)
		out := c.newFrag()

		c.eps(out.start, out.accept)
		c.eps(out.start, a.start)
		c.eps(a.accept, out.accept)

		return out
	}

	// bounded {m,n}

	out := c.newFrag()
	c.eps(out.start, out.accept)

	cur := out

	for i := 0; i < n.min; i++ {
		part := c.compile(n.sub)
		c.eps(cur.accept, part.start)
		cur = nfaFragment{cur.start, part.accept}
	}

	if n.max == n.min {
		return cur
	}

	for i := 0; i < n.max-n.min; i++ {
		part := c.compile(n.sub)
		opt := c.newFrag()

		c.eps(opt.start, opt.accept)
		c.eps(opt.start, part.start)
		c.eps(part.accept, opt.accept)

		c.eps(cur.accept, opt.start)
		cur = nfaFragment{cur.start, opt.accept}
	}

	return cur
}

//
// ============================================================
// HASH HELPERS
// ============================================================
//

func hashValue[T any](v T) uint64 {
	h := fnv.New64a()
	fmt.Fprint(h, v)
	return h.Sum64()
}

func hashRanges[T any](rs []charRange[T]) uint64 {
	h := fnv.New64a()
	for _, r := range rs {
		fmt.Fprint(h, r.lo, ":", r.hi, ";")
	}
	return h.Sum64()
}
