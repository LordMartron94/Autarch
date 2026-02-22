package pattern

import (
	"autarch"
	"fmt"
	"memarch"
)

// ============================================================
// PUBLIC ENTRY
// ============================================================

/*
RegulaCompileToNFAThompson compiles multiple Regula patterns into NFAs using
Thompson’s construction over a shared alphabet and symbol space.

All provided patterns are:

 1. collected into a unified symbol set
 2. bound to stable logical symbol IDs
 3. expanded into a single physical alphabet
 4. compiled into individual NFAs that share that alphabet

This guarantees symbol coherence across all automata — a hard requirement
for lexer and multi-pattern recognition systems.

Unlike lower-level APIs, callers do NOT need to manually:

  - collect symbols
  - build alphabets
  - bind logical IDs

The shared compilation context handles the full preparation pipeline internally.

---

### Use cases

  - Lexer rule compilation (token NFAs per rule)
  - Multi-pattern recognizers
  - Shared-alphabet automata pipelines
  - Static analyzer pattern sets

---

### Acceptance semantics

  - Each compiled NFA has exactly one accepting state
  - The accepting state carries the instruction’s Outcome
  - No sentinel values or implicit acceptance markers are used

---

### Performance characteristics

Symbol preparation is performed once for the entire instruction batch.

Compilation is linear in:

  - total AST size across all patterns
  - number of emitted transitions

---

### Preconditions

  - instructions must be non-empty
  - each instruction must contain a valid Regula AST
  - the shared compilation context must not be reused concurrently

---

### Error cases

  - Returns an error if the instruction list is empty
  - Panics only on internal invariants (invalid AST structure)

---

### Returns

  - One NFA per instruction, in the same order as provided
  - All NFAs share the same alphabet and symbol indexer
*/
func RegulaCompileToNFAThompson[TObs any, TOutcome comparable](
	alloc memarch.AllocationFn,
	instructions []RegulaNFAInstruction[TObs, TOutcome],
	ctx *RegulaSharedCompilationContext[TObs],
) ([]*autarch.NFA[TObs, TOutcome], error) {
	if len(instructions) == 0 {
		return nil, fmt.Errorf("instructions list must be non-empty")
	}

	patterns := make([]*RegulaAST[TObs], len(instructions))
	for i, instruction := range instructions {
		patterns[i] = instruction.Pattern
	}

	ctx.fullPrepare(patterns)
	out := make([]*autarch.NFA[TObs, TOutcome], len(patterns))

	for i, instruction := range instructions {
		c := newThompsonCompiler[TObs](ctx.expander)
		frag := c.compile(instruction.Pattern)

		numStates := c.nextState

		outcomes := make([]TOutcome, numStates)
		accepting := make([]bool, numStates)

		accepting[frag.accept] = true
		outcomes[frag.accept] = instruction.Outcome

		patternNFA := autarch.NFACreate(
			alloc,
			ctx.alphabet,
			c.transitions,
			c.epsilonEdges,
			[]uint64{frag.start},
			accepting,
			outcomes,
			ctx.indexer,
		)

		out[i] = patternNFA
	}

	return out, nil
}

// ============================================================
// THOMPSON CORE
// ============================================================

type nfaFragment struct {
	start  uint64
	accept uint64
}

type thompsonCompiler[TObs any] struct {
	nextState uint64

	// Dense hot path: append-only transitions.
	transitions []autarch.Transition[TObs]

	// Sparse adjacency: epsilon edges.
	epsilonEdges map[uint64][]uint64

	// Logical → physical symbol expansion is now strategy-agnostic.
	expander symbolExpander
}

func newThompsonCompiler[TObs any](expander symbolExpander) *thompsonCompiler[TObs] {
	return &thompsonCompiler[TObs]{
		expander:     expander,
		epsilonEdges: make(map[uint64][]uint64),
	}
}

func (c *thompsonCompiler[TObs]) newState() uint64 {
	s := c.nextState
	c.nextState++
	return s
}

func (c *thompsonCompiler[TObs]) newFrag() nfaFragment {
	return nfaFragment{start: c.newState(), accept: c.newState()}
}

func (c *thompsonCompiler[TObs]) eps(from, to uint64) {
	c.epsilonEdges[from] = append(c.epsilonEdges[from], to)
}

// emitLogical expands one logical symbol to all covering physical symbol IDs and emits transitions.
// This is the ordering you want for efficiency:
//  1. expand once,
//  2. fast loop append transitions.
func (c *thompsonCompiler[TObs]) emitLogical(from uint64, lid logicalID, to uint64) {
	phys := c.expander.expand(lid)
	if len(phys) == 0 {
		return
	}

	// Reserve exactly what we will append (best-effort; avoids repeated growth).
	base := len(c.transitions)
	c.transitions = append(c.transitions, make([]autarch.Transition[TObs], len(phys))...)
	out := c.transitions[base:]

	for i, pid := range phys {
		out[i] = autarch.Transition[TObs]{
			CurrentState: from,
			NextState:    to,
			Symbol:       autarch.SymbolCreate[TObs]("", uint64(pid)),
		}
	}
}

func (c *thompsonCompiler[TObs]) compile(n *RegulaAST[TObs]) nfaFragment {
	switch n.kind {
	case EXPRESSION_LITERAL:
		return c.literalSequence(n.literalSymIDs)

	case EXPRESSION_CLASS:
		return c.class(n.classSymID)

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
		panic("unknown regula node kind")
	}
}

func (c *thompsonCompiler[TObs]) literalSequence(ids []logicalID) nfaFragment {
	if len(ids) == 0 {
		f := c.newFrag()
		c.eps(f.start, f.accept)
		return f
	}

	start := c.newState()
	cur := start

	for _, lid := range ids {
		next := c.newState()
		c.emitLogical(cur, lid, next)
		cur = next
	}

	return nfaFragment{start, cur}
}

func (c *thompsonCompiler[TObs]) class(lid logicalID) nfaFragment {
	f := c.newFrag()
	c.emitLogical(f.start, lid, f.accept)
	return f
}

func (c *thompsonCompiler[TObs]) repeat(n *RegulaAST[TObs]) nfaFragment {
	// Fast-path common quantifiers first (hot).
	if n.min == 0 && n.max == -1 { // *
		a := c.compile(n.sub)
		out := c.newFrag()

		c.eps(out.start, out.accept)
		c.eps(out.start, a.start)
		c.eps(a.accept, a.start)
		c.eps(a.accept, out.accept)

		return out
	}

	if n.min == 1 && n.max == -1 { // +
		a := c.compile(n.sub)
		out := c.newFrag()

		c.eps(out.start, a.start)
		c.eps(a.accept, a.start)
		c.eps(a.accept, out.accept)

		return out
	}

	if n.min == 0 && n.max == 1 { // ?
		a := c.compile(n.sub)
		out := c.newFrag()

		c.eps(out.start, out.accept)
		c.eps(out.start, a.start)
		c.eps(a.accept, out.accept)

		return out
	}

	// Bounded {m,n}
	// Implementation notes:
	// - Build the mandatory chain of m occurrences.
	// - Then append (n-m) optional occurrences, each wrapped by an "optional" fragment.
	//
	// Ordering for efficiency:
	// - avoid rebuilding "out := newFrag(); eps(out.start, out.accept)" unless needed
	// - keep ε appends localized.

	out := c.newFrag()
	c.eps(out.start, out.accept) // allow 0 total (used when min==0), harmless otherwise

	cur := out

	// Mandatory part
	for i := 0; i < n.min; i++ {
		part := c.compile(n.sub)
		c.eps(cur.accept, part.start)
		cur = nfaFragment{start: cur.start, accept: part.accept}
	}

	// Exactly {m,m}
	if n.max == n.min {
		return cur
	}

	// Optional tail
	for i := 0; i < n.max-n.min; i++ {
		part := c.compile(n.sub)

		opt := c.newFrag()
		c.eps(opt.start, opt.accept)
		c.eps(opt.start, part.start)
		c.eps(part.accept, opt.accept)

		c.eps(cur.accept, opt.start)
		cur = nfaFragment{start: cur.start, accept: opt.accept}
	}

	return cur
}
