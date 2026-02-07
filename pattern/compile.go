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

The function uses Thompson's construction algorithm to convert a regular expression pattern
represented as an abstract syntax tree into an executable NFA. The resulting NFA can be used
for pattern matching, lexical analysis, or further converted to a DFA for efficient execution.

Use cases:
- Building pattern matchers from regular expression-like syntax
- Constructing lexers with complex token recognition rules
- Creating state machines for protocol parsing
- Implementing string matching engines

Time complexity: O(n) where n is the number of nodes in the AST
Space complexity: O(s + t) where s is states and t is transitions in the resulting NFA

Prerequisites:
- alloc must be a valid memory allocation function
- root must be a valid regulaAST constructed using the pattern builder functions
- acceptOutcome and invalidOutcome must be distinct values

Edge cases:
- Empty patterns (epsilon) are supported and create a single accepting state
- Patterns with no matches create an NFA that never accepts
- Large patterns may create NFAs with many states (exponential in worst case for alternations)

The compilation process:
1. Collects all symbols (literals and character classes) from the AST
2. Builds a deduplicated alphabet with stable symbol IDs
3. Constructs NFA transitions using Thompson's algorithm
4. Marks the final accepting state with acceptOutcome
5. Marks all other states with invalidOutcome

The resulting NFA uses epsilon transitions for alternation, concatenation, and repetition operations.
These can be eliminated by converting to a DFA using NFAToDFA.
*/
func RegulaCompileToNFA[TObservation cmp.Ordered, TOutcome comparable](
	alloc memarch.AllocationFn,
	root regulaAST[TObservation],
	acceptOutcome TOutcome,
	invalidOutcome TOutcome,
) *autarch.NFA[TObservation, TOutcome] {

	builder := newSymbolBuilder[TObservation]()
	builder.collect(&root)

	alphabet := builder.buildAlphabet()
	indexer := autarch.SymbolIndexerBuild(alphabet)

	c := newThompsonCompiler(builder)
	frag := c.compile(&root)

	states := make([]TOutcome, c.nextState)
	for i := range states {
		states[i] = invalidOutcome
	}
	states[frag.accept] = acceptOutcome

	return autarch.NFACreate(
		alloc,
		alphabet,
		c.transitions,
		[]uint64{frag.start},
		states,
		indexer,
	)
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
	b.defs = append(b.defs, autarch.SymbolDefinition[TObs]{
		ID:   id,
		Name: fmt.Sprintf("%v", v),
		Match: func(o TObs) bool {
			return o == v
		},
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

func (b *symbolBuilder[TObs]) collect(n *regulaAST[TObs]) {
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
	nextState   uint64
	transitions []autarch.Transition[TObs]
	builder     *symbolBuilder[TObs]
}

func newThompsonCompiler[TObs cmp.Ordered](b *symbolBuilder[TObs]) *thompsonCompiler[TObs] {
	return &thompsonCompiler[TObs]{builder: b}
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
	c.transitions = append(c.transitions,
		autarch.Transition[TObs]{
			CurrentState: a,
			NextState:    b,
			Symbol:       autarch.EpsilonSymbolCreate[TObs](),
		},
	)
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

func (c *thompsonCompiler[TObs]) compile(n *regulaAST[TObs]) nfaFragment {
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

func (c *thompsonCompiler[TObs]) repeat(n *regulaAST[TObs]) nfaFragment {

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
