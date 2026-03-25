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
RegulaCompileToNFAThompson compiles multiple Regula patterns into a single, unified NFA
using Thompson’s construction over a shared alphabet and symbol space.

This function performs a unified compilation where all patterns are joined by a
common start state via epsilon transitions. This is the preferred entry point for
building lexers or multi-pattern matchers, as it produces a single state machine
ready for determinization.

The compilation pipeline:
 1. Collects all unique symbols across all patterns.
 2. Binds stable logical IDs and prepares a shared physical alphabet.
 3. Compiles each pattern into a Thompson fragment within a single compiler context.
 4. Links a global start state to each pattern's entry via epsilon (ε) edges.

---

### Use cases

  - Lexer generation (combining all token rules into one recognizer).
  - High-performance multi-pattern matching.
  - Contexts where epsilon-based NFA merging is more efficient than Glushkov.

---

### Outcome semantics

  - The resulting NFA contains one global start state (0).
  - Each pattern's terminal (accept) state carries its specific instruction Outcome.
  - All intermediate and non-accepting states carry the nonTerminalOutcome.
  - If multiple patterns match the same input, the downstream DFA resolution
    strategy (e.g., "first-match" or "highest priority") will determine the winner.

---

### Performance characteristics

  - Preparation (alphabet/symbol binding) is performed once for the batch.
  - Construction is O(N) where N is the total number of AST nodes across all patterns.
  - Produces an ε-NFA with O(N) states and transitions.

---

### Preconditions

  - instructions must be non-empty.
  - The shared compilation context must not be reused concurrently.

---

### Returns

  - A single *autarch.NFA representing the union of all provided patterns.
*/
func RegulaCompileToNFAThompson[TObs any, TOutcome comparable](
	alloc memarch.AllocationFn,
	instructions []PatternCompilationInstruction[TObs, TOutcome, RegulaAST[TObs]],
	ctx *SharedCompilationContext[TObs, RegulaAST[TObs]],
	nonTerminalOutcome TOutcome,
) ([]*autarch.NFA[TObs, AnnotatedOutcome[TOutcome]], error) {
	if len(instructions) == 0 {
		return nil, fmt.Errorf("instructions list must be non-empty")
	}

	patterns := make([]*RegulaAST[TObs], len(instructions))
	for i := range instructions {
		patterns[i] = instructions[i].Pattern
	}

	var nextPos positionID
	ctx.fullPrepare(
		patterns,
		regulaCollectSymbols[TObs],
		func(p *RegulaAST[TObs], feeder symbolFeeder[TObs], bindState interface{}) {
			regulaBindIDs(p, feeder, bindState.(*positionID))
		},
		&nextPos,
	)

	// Initialize a single compiler for the entire union
	c := newThompsonCompiler[TObs](ctx.expander, nonTerminalOutcome)

	// Create the global entry point for the union
	rootStart := c.newState()

	// Map terminal states to their respective outcomes and annotations
	terminalOutcomes := make(map[uint64]TOutcome)
	terminalAnnotations := make(map[uint64]AnnotationID)

	for _, instruction := range instructions {
		frag := c.compile(instruction.Pattern)

		// Wire the global root to this pattern's entry
		c.eps(rootStart, frag.start)

		terminalOutcomes[frag.accept] = instruction.Outcome

		if instruction.Pattern.annotationID != nil {
			terminalAnnotations[frag.accept] = *instruction.Pattern.annotationID
		}
	}

	numStates := c.nextState
	outcomes := make([]AnnotatedOutcome[TOutcome], numStates)

	for state := uint64(0); state < numStates; state++ {
		outcomes[state] = AnnotatedOutcome[TOutcome]{Value: nonTerminalOutcome}

		// Apply terminal outcomes
		if val, isTerminal := terminalOutcomes[state]; isTerminal {
			outcomes[state].Value = val

			// Priority: Node-level annotation > Compiler-captured annotation
			if ann, hasAnn := terminalAnnotations[state]; hasAnn {
				val := ann
				outcomes[state].Annotation = &val
			} else if ann, hasAnn := c.annotations[state]; hasAnn {
				val := ann
				outcomes[state].Annotation = &val
			}
		}
	}

	nfa := autarch.NFACreate(
		alloc,
		ctx.alphabet,
		c.transitions,
		c.epsilonEdges,
		[]uint64{rootStart},
		outcomes,
		ctx.nondeterministicResolve,
	)

	// Return as a single-element slice to satisfy the compiler interface signature
	return []*autarch.NFA[TObs, AnnotatedOutcome[TOutcome]]{nfa}, nil
}

// ============================================================
// THOMPSON CORE
// ============================================================

type nfaFragment struct {
	start  uint64
	accept uint64
}

type thompsonCompiler[TObs any, TOutcome any] struct {
	nextState uint64

	transitions  []autarch.Transition[TObs]
	epsilonEdges map[uint64][]uint64
	expander     symbolExpander
	baseOutcome  TOutcome
	annotations  map[uint64]AnnotationID
}

func newThompsonCompiler[TObs any, TOutcome any](expander symbolExpander, base TOutcome) *thompsonCompiler[TObs, TOutcome] {
	return &thompsonCompiler[TObs, TOutcome]{
		expander:     expander,
		epsilonEdges: make(map[uint64][]uint64),
		annotations:  make(map[uint64]AnnotationID),
		baseOutcome:  base,
	}
}

func (c *thompsonCompiler[TObs, TOutcome]) newState() uint64 {
	s := c.nextState
	c.nextState++
	return s
}

func (c *thompsonCompiler[TObs, TOutcome]) newFrag() nfaFragment {
	return nfaFragment{start: c.newState(), accept: c.newState()}
}

func (c *thompsonCompiler[TObs, TOutcome]) eps(from, to uint64) {
	c.epsilonEdges[from] = append(c.epsilonEdges[from], to)
}

func (c *thompsonCompiler[TObs, TOutcome]) emitLogical(from uint64, lid logicalID, to uint64) {
	phys := c.expander.expand(lid)
	if len(phys) == 0 {
		return
	}

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

func (c *thompsonCompiler[TObs, TOutcome]) compile(n *RegulaAST[TObs]) nfaFragment {
	var frag nfaFragment

	switch n.kind {
	case EXPRESSION_LITERAL:
		frag = c.literalSequence(n.literalSymIDs)
	case EXPRESSION_CLASS:
		frag = c.class(n.classSymID)
	case EXPRESSION_CONCAT:
		a := c.compile(n.left)
		b := c.compile(n.right)
		c.eps(a.accept, b.start)
		frag = nfaFragment{a.start, b.accept}
	case EXPRESSION_UNION:
		a := c.compile(n.left)
		b := c.compile(n.right)
		out := c.newFrag()
		c.eps(out.start, a.start)
		c.eps(out.start, b.start)
		c.eps(a.accept, out.accept)
		c.eps(b.accept, out.accept)
		frag = out
	case EXPRESSION_REPEAT:
		frag = c.repeat(n)
	case EXPRESSION_CAPTURE:
		// Explicit pass-through. Without this, regex parsers dropping capture groups will fail.
		frag = c.compile(n.sub)
	default:
		panic("unknown node kind")
	}

	// PROPAGATION: If this node is annotated, tag the fragment's exit state.
	if n.annotationID != nil {
		c.annotations[frag.accept] = *n.annotationID
	}

	return frag
}

func (c *thompsonCompiler[TObs, TOutcome]) literalSequence(ids []logicalID) nfaFragment {
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

func (c *thompsonCompiler[TObs, TOutcome]) class(lid logicalID) nfaFragment {
	f := c.newFrag()
	c.emitLogical(f.start, lid, f.accept)
	return f
}

func (c *thompsonCompiler[TObs, TOutcome]) repeat(n *RegulaAST[TObs]) nfaFragment {
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

	out := c.newFrag()
	c.eps(out.start, out.accept) // allow 0 total

	cur := out

	for i := 0; i < n.min; i++ {
		part := c.compile(n.sub)
		c.eps(cur.accept, part.start)
		cur = nfaFragment{start: cur.start, accept: part.accept}
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
		cur = nfaFragment{start: cur.start, accept: opt.accept}
	}

	return cur
}
