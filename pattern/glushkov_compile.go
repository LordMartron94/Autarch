package pattern

import (
	"autarch"
	"fmt"
	"memarch"
)

// positionSet is a dense bitset representing a set of position IDs.
// This is critical for performance in Glushkov's O(N^2) follow-set construction.
type positionSet []uint64

func newPositionSet(maxPos positionID) positionSet {
	return make(positionSet, (maxPos/64)+1)
}

func (s positionSet) add(p positionID) {
	s[p/64] |= 1 << (p % 64)
}

func (s positionSet) union(other positionSet) {
	for i := range other {
		s[i] |= other[i]
	}
}

func (s positionSet) has(p positionID) bool {
	return (s[p/64] & (1 << (p % 64))) != 0
}

func (s positionSet) iter(fn func(p positionID)) {
	for i, bucket := range s {
		if bucket == 0 {
			continue
		}
		for j := 0; j < 64; j++ {
			if bucket&(1<<j) != 0 {
				fn(positionID(i*64 + j))
			}
		}
	}
}

type glushkovInfo struct {
	nullable bool
	first    positionSet
	last     positionSet
}

/*
RegulaCompileToNFAGlushkov compiles a batch of Regula AST patterns into ε-free NFAs
using Glushkov’s construction (also known as the position automaton).

This algorithm constructs an automaton where:

  - There is exactly one start state (state 0)
  - Each symbol occurrence in the regular expression becomes one state (a “position”)
  - Transitions are labeled by the symbol of the destination position
  - No epsilon transitions exist

This form is particularly well-suited for large-scale lexer pipelines, as ε-free NFAs
determinize into smaller and faster DFAs compared to Thompson-style ε-NFAs.

────────────────────────────────────────────────────────────
Conceptual Overview
────────────────────────────────────────────────────────────

For each pattern, the compiler performs four major phases:

1) Structural normalization
2) Shared symbol binding
3) Follow-set analysis (Glushkov metadata computation)
4) State machine synthesis

The construction is based on computing, for every sub-expression:

  - nullable — whether the expression can match the empty string
  - first    — the set of positions that may appear first
  - last     — the set of positions that may appear last

Additionally, a global follow relation is built:

	follow[p] = all positions that may immediately follow position p

These sets are computed in a single structural traversal of the AST.

Once computed, the NFA is synthesized as:

  - Start state → all positions in first
  - For each p → q in follow, emit transition p → q
  - Accepting states = all positions in last
  - If nullable, start state is also accepting

────────────────────────────────────────────────────────────
Normalization Guarantees
────────────────────────────────────────────────────────────

Before analysis, patterns are normalized into Glushkov-compatible form:

  - Multi-symbol literals are rewritten into concatenations
    "abc" → (a . b) . c

  - Bounded repetitions are unrolled structurally
    a{2,4}, a?, a{3,}, etc.

  - Only the following repeat forms remain:
    sub*
    sub+

  - Epsilon is represented explicitly as an empty literal node

After normalization:

  - Every leaf node corresponds to exactly one symbol position
  - Each position has a globally unique ID
  - The AST becomes suitable for direct position-based automaton construction

────────────────────────────────────────────────────────────
Shared Compilation Context
────────────────────────────────────────────────────────────

All patterns in the instruction batch are compiled under a single
RegulaSharedCompilationContext.

The context is responsible for:

  - Assigning logical symbol IDs
  - Managing alphabet expansion (e.g. character classes → physical symbols)
  - Maintaining symbol indexing for DFA/NFA backends

This guarantees:

  - Symbol consistency across all compiled NFAs
  - Zero per-transition map lookups during construction
  - Efficient determinization downstream

Callers MUST reuse the same context when combining NFAs into DFAs.

────────────────────────────────────────────────────────────
Performance Characteristics
────────────────────────────────────────────────────────────

Let:

	P = number of symbol positions in the pattern

Then:

  - Follow-set construction is O(P² / word_size) using dense bitsets
  - AST traversal is linear in node count
  - NFA synthesis is linear in number of follow relations

This is the standard optimal complexity for Glushkov automata.

The resulting NFA contains:

  - P + 1 states
  - No epsilon transitions
  - Exactly one transition per follow relation (after symbol expansion)

────────────────────────────────────────────────────────────
Parameters
────────────────────────────────────────────────────────────

alloc:

	Allocation function used by the underlying autarch NFA backend.

instructions:

	A non-empty slice of RegulaNFAInstruction values, each containing:

	  • Pattern — Regula AST to compile
	  • Outcome — accepting state payload for that pattern

	Each pattern is compiled into its own NFA.

ctx:

	Shared compilation context that binds symbol identities, alphabet,
	expansion rules, and indexers.

	Must be prepared only via this function or compatible Regula
	compilation pipelines.

────────────────────────────────────────────────────────────
Returns
────────────────────────────────────────────────────────────

On success:

	A slice of ε-free NFAs, one per instruction, in the same order.

On failure:

	An error if the instruction list is empty or if normalization fails.

────────────────────────────────────────────────────────────
Invariants of Returned NFAs
────────────────────────────────────────────────────────────

For each returned NFA:

  - Exactly one start state (state 0)
  - No epsilon transitions
  - States correspond to symbol positions
  - Accepting states carry the provided outcome
  - Alphabet and symbol IDs match ctx

These NFAs are immediately suitable for:

  - Large union construction
  - Subset determinization
  - DFA minimization

────────────────────────────────────────────────────────────
Intended Use
────────────────────────────────────────────────────────────

This is the preferred Regula compilation pipeline for:

  - Lexer generation
  - Token recognizers
  - High-performance DFA frontends
  - Large pattern batches

Use Thompson ε-NFAs only when semantic structure or debugging clarity
is required.

────────────────────────────────────────────────────────────
Errors
────────────────────────────────────────────────────────────

Returns an error if:

  - instructions is empty

All normalized AST structures are assumed valid; semantic validation
must be performed upstream.
*/
func RegulaCompileToNFAGlushkov[TObs any, TOutcome comparable](
	alloc memarch.AllocationFn,
	instructions []RegulaNFAInstruction[TObs, TOutcome],
	ctx *RegulaSharedCompilationContext[TObs],
) ([]*autarch.NFA[TObs, AnnotatedOutcome[TOutcome]], error) {

	if len(instructions) == 0 {
		return nil, fmt.Errorf("instructions list must be non-empty")
	}

	// 1. Normalization & Shared Binding
	patterns := make([]*RegulaAST[TObs], len(instructions))
	for i := range instructions {
		normalized, _ := normalizeForGlushkov(instructions[i].Pattern)
		instructions[i].Pattern = normalized
		patterns[i] = normalized
	}

	ctx.fullPrepare(patterns)

	out := make([]*autarch.NFA[TObs, AnnotatedOutcome[TOutcome]], len(instructions))

	for i := range instructions {
		pat := instructions[i].Pattern
		outcome := instructions[i].Outcome

		// 2. Map positions to symbols
		pm := buildPosMetadataMap(pat)

		// Initialize follow-set table
		// follow[p] = set of positions that can follow position p
		follow := make([]positionSet, pm.maxPos+1)
		for i := range follow {
			follow[i] = newPositionSet(pm.maxPos)
		}

		// 3. Recursive Structural Analysis
		info := computeGlushkovInfo(pat, follow, pm.maxPos)

		// 4. State Machine Synthesis
		out[i] = buildGlushkovNFA(
			alloc,
			ctx,
			pm,
			follow,
			info,
			outcome,
		)
	}

	return out, nil
}

func computeGlushkovInfo[TObs any](
	n *RegulaAST[TObs],
	follow []positionSet,
	maxPos positionID,
) glushkovInfo {
	switch n.kind {
	case EXPRESSION_LITERAL:
		// Handle Epsilon (empty string literal)
		// This is generated by unrollBoundedRepeat for optionality.
		if len(n.literals) == 0 {
			return glushkovInfo{
				nullable: true,
				first:    newPositionSet(maxPos),
				last:     newPositionSet(maxPos),
			}
		}
		// Fallthrough for single-symbol literal
		fallthrough

	case EXPRESSION_CLASS:
		p := getSinglePosID(n)
		fs := newPositionSet(maxPos)
		ls := newPositionSet(maxPos)
		fs.add(p)
		ls.add(p)
		return glushkovInfo{nullable: false, first: fs, last: ls}

	case EXPRESSION_CONCAT:
		l := computeGlushkovInfo(n.left, follow, maxPos)
		r := computeGlushkovInfo(n.right, follow, maxPos)

		// Every position in L.last can be followed by every position in R.first
		l.last.iter(func(p positionID) {
			follow[p].union(r.first)
		})

		res := glushkovInfo{
			nullable: l.nullable && r.nullable,
			// Copy first/last to avoid mutating child results (though bitsets help)
			first: newPositionSet(maxPos),
			last:  newPositionSet(maxPos),
		}
		res.first.union(l.first)
		res.last.union(r.last)

		if l.nullable {
			res.first.union(r.first)
		}
		if r.nullable {
			res.last.union(l.last)
		}
		return res

	case EXPRESSION_UNION:
		l := computeGlushkovInfo(n.left, follow, maxPos)
		r := computeGlushkovInfo(n.right, follow, maxPos)

		res := glushkovInfo{
			nullable: l.nullable || r.nullable,
			first:    l.first,
			last:     l.last,
		}
		res.first.union(r.first)
		res.last.union(r.last)
		return res

	case EXPRESSION_REPEAT:
		// After normalization, this only handles sub* (min=0) or sub+ (min=1)
		sub := computeGlushkovInfo(n.sub, follow, maxPos)

		// Key repetition property: sub.last -> sub.first
		sub.last.iter(func(p positionID) {
			follow[p].union(sub.first)
		})

		return glushkovInfo{
			// Nullable if the repeat can be skipped (min=0) OR the sub-expression is nullable
			nullable: (n.min == 0) || sub.nullable,
			first:    sub.first,
			last:     sub.last,
		}
	}
	panic("Glushkov: unknown expression kind")
}

func buildGlushkovNFA[TObs any, TOutcome comparable](
	alloc memarch.AllocationFn,
	ctx *RegulaSharedCompilationContext[TObs],
	pm posMetadataMap,
	follow []positionSet,
	info glushkovInfo,
	baseOutcome TOutcome,
) *autarch.NFA[TObs, AnnotatedOutcome[TOutcome]] {

	numStates := uint64(pm.maxPos) + 1
	transitions := make([]autarch.Transition[TObs], 0)

	// Pre-expand logical symbols to physical IDs to avoid map lookups in inner loops
	expansionCache := make([][]physicalID, len(ctx.alphabet)+1)
	getExpansion := func(lid logicalID) []physicalID {
		if expansionCache[lid] == nil {
			expansionCache[lid] = ctx.expander.expand(lid)
		}
		return expansionCache[lid]
	}

	emit := func(from uint64, toPos positionID) {
		phys := getExpansion(pm.symByPos[toPos])
		for _, pid := range phys {
			transitions = append(transitions, autarch.Transition[TObs]{
				CurrentState: from,
				NextState:    uint64(toPos),
				Symbol:       autarch.SymbolCreate[TObs]("", uint64(pid)),
			})
		}
	}

	// 1. Initial Transitions: Start State -> First Positions
	// Transition label is defined by the DESTINATION position's symbol.
	info.first.iter(func(p positionID) {
		emit(0, p)
	})

	// 2. Follow Transitions: Pos P -> Pos Q
	for p := positionID(1); p <= pm.maxPos; p++ {
		follow[p].iter(func(q positionID) {
			emit(uint64(p), q)
		})
	}

	// 3. Terminal States
	accepting := make([]bool, numStates)
	outcomes := make([]AnnotatedOutcome[TOutcome], numStates)

	// Helper to build the specific outcome for a state
	resolveOutcome := func(p positionID) AnnotatedOutcome[TOutcome] {
		ann := AnnotationID(0)
		if a, ok := pm.annByPos[p]; ok {
			ann = a
		}
		return AnnotatedOutcome[TOutcome]{
			Value:      baseOutcome,
			Annotation: ann,
		}
	}

	// 1. Handle Nullable (Start state accepting)
	if info.nullable {
		accepting[0] = true
		// Note: The start state doesn't correspond to a position,
		// so it usually gets the "default" annotation of the root node.
		outcomes[0] = AnnotatedOutcome[TOutcome]{Value: baseOutcome}
	}

	// 2. Handle Position States
	info.last.iter(func(p positionID) {
		accepting[p] = true
		outcomes[p] = resolveOutcome(p)
	})

	return autarch.NFACreate(
		alloc,
		ctx.alphabet,
		transitions,
		nil, // No epsilons in Glushkov
		[]uint64{0},
		accepting,
		outcomes,
		ctx.indexer,
	)
}

// ------------------------------------------------------------ SUPPORT CODE

type posMetadataMap struct {
	symByPos map[positionID]logicalID
	annByPos map[positionID]AnnotationID
	maxPos   positionID
}

func buildPosMetadataMap[TObs any](n *RegulaAST[TObs]) posMetadataMap {
	pm := posMetadataMap{
		symByPos: make(map[positionID]logicalID),
		annByPos: make(map[positionID]AnnotationID),
	}
	var walk func(*RegulaAST[TObs])
	walk = func(x *RegulaAST[TObs]) {
		if x == nil {
			return
		}

		switch x.kind {
		case EXPRESSION_LITERAL:
			for i, p := range x.literalPosIDs {
				pm.symByPos[p] = x.literalSymIDs[i]
				if x.annotationID != nil {
					pm.annByPos[p] = *x.annotationID
				}
				if p > pm.maxPos {
					pm.maxPos = p
				}
			}
		case EXPRESSION_CLASS:
			p := x.classPosID
			pm.symByPos[p] = x.classSymID
			if x.annotationID != nil {
				pm.annByPos[p] = *x.annotationID
			}
			if p > pm.maxPos {
				pm.maxPos = p
			}
		case EXPRESSION_CONCAT, EXPRESSION_UNION:
			walk(x.left)
			walk(x.right)
		case EXPRESSION_REPEAT:
			walk(x.sub)
		}
	}
	walk(n)
	return pm
}

func getSinglePosID[TObs any](n *RegulaAST[TObs]) positionID {
	if n.kind == EXPRESSION_LITERAL {
		return n.literalPosIDs[0]
	}
	return n.classPosID
}

func normalizeForGlushkov[TObs any](n *RegulaAST[TObs]) (*RegulaAST[TObs], bool) {
	if n == nil {
		return nil, false
	}

	// Capture the annotation from the source node to propagate it
	ann := n.annotationID

	switch n.kind {
	case EXPRESSION_LITERAL:
		if len(n.literals) <= 1 {
			return n, false
		}
		// "abc" -> ((a . b) . c)
		// Only 'c' should carry the annotation of the whole string.
		var cur *RegulaAST[TObs]
		for i := 0; i < len(n.literals); i++ {
			leaf := &RegulaAST[TObs]{
				kind:     EXPRESSION_LITERAL,
				literals: []TObs{n.literals[i]},
			}
			// Only the very last leaf gets the parent's annotation
			if i == len(n.literals)-1 {
				leaf.annotationID = ann
			}

			if cur == nil {
				cur = leaf
			} else {
				cur = &RegulaAST[TObs]{kind: EXPRESSION_CONCAT, left: cur, right: leaf}
			}
		}
		return cur, true

	case EXPRESSION_CLASS:
		return n, false

	case EXPRESSION_CONCAT:
		l2, _ := normalizeForGlushkov(n.left)
		r2, _ := normalizeForGlushkov(n.right)
		// Propagate annotation to the right child (the tail)
		if ann != nil {
			r2.annotationID = ann
		}
		return &RegulaAST[TObs]{kind: n.kind, left: l2, right: r2}, true

	case EXPRESSION_UNION:
		l2, _ := normalizeForGlushkov(n.left)
		r2, _ := normalizeForGlushkov(n.right)
		// If the union is annotated, both paths are terminal
		if ann != nil {
			l2.annotationID = ann
			r2.annotationID = ann
		}
		return &RegulaAST[TObs]{kind: n.kind, left: l2, right: r2}, true

	case EXPRESSION_REPEAT:
		subNormalized, _ := normalizeForGlushkov(n.sub)

		if n.max == -1 {
			if n.min == 0 || n.min == 1 {
				res := &RegulaAST[TObs]{
					kind: EXPRESSION_REPEAT,
					sub:  subNormalized,
					min:  n.min,
					max:  -1,
				}
				res.annotationID = ann // Annotation on a star/plus triggers on every iteration
				return res, true
			}
			return unrollInfiniteRepeat(subNormalized, n.min, ann), true
		}

		return unrollBoundedRepeat(subNormalized, n.min, n.max, ann), true
	}

	return n, false
}

func unrollInfiniteRepeat[TObs any](sub *RegulaAST[TObs], min int, ann *AnnotationID) *RegulaAST[TObs] {
	plusNode := &RegulaAST[TObs]{
		kind: EXPRESSION_REPEAT,
		sub:  sub,
		min:  1,
		max:  -1,
	}
	plusNode.annotationID = ann // Tag the infinite tail

	if min == 0 {
		// a* case (already handled in main switch, but here for safety)
		res := sub.Star()
		res.annotationID = ann
		return &res
	}

	// a{3,} -> (a . (a . a+))
	return concatChain(sub, min-1, plusNode, nil)
}

func unrollBoundedRepeat[TObs any](sub *RegulaAST[TObs], min, max int, ann *AnnotationID) *RegulaAST[TObs] {
	if max == 0 {
		epsilon := &RegulaAST[TObs]{kind: EXPRESSION_LITERAL, literals: nil}
		epsilon.annotationID = ann
		return epsilon
	}

	epsilon := &RegulaAST[TObs]{kind: EXPRESSION_LITERAL, literals: nil}

	makeOptional := func(node *RegulaAST[TObs], currentAnn *AnnotationID) *RegulaAST[TObs] {
		// When making optional, the epsilon path and the node path are both terminal
		optEpsilon := &RegulaAST[TObs]{kind: EXPRESSION_LITERAL, literals: nil}
		optEpsilon.annotationID = currentAnn
		node.annotationID = currentAnn

		return &RegulaAST[TObs]{
			kind:  EXPRESSION_UNION,
			left:  node,
			right: optEpsilon,
		}
	}

	if min == 0 {
		var cur = epsilon
		for i := 0; i < max; i++ {
			if i == 0 {
				cur = makeOptional(sub, ann)
			} else {
				// (sub . cur)?
				// Annotation only goes on the 'outer' optional shell for the sequence
				cur = makeOptional(&RegulaAST[TObs]{
					kind:  EXPRESSION_CONCAT,
					left:  sub,
					right: cur,
				}, ann)
			}
		}
		return cur
	}

	fixedPart := concatChain(sub, min, nil, nil)
	optionalPart := unrollBoundedRepeat(sub, 0, max-min, ann)

	return &RegulaAST[TObs]{
		kind:  EXPRESSION_CONCAT,
		left:  fixedPart,
		right: optionalPart,
	}
}

func concatChain[TObs any](sub *RegulaAST[TObs], count int, tail *RegulaAST[TObs], ann *AnnotationID) *RegulaAST[TObs] {
	if count <= 0 {
		if tail != nil && ann != nil {
			tail.annotationID = ann
		}
		return tail
	}

	var res *RegulaAST[TObs]
	if tail != nil {
		res = tail
	} else {
		res = sub
		if count == 1 {
			res.annotationID = ann
		}
		count--
	}

	for i := 0; i < count; i++ {
		// As we build the chain backwards, the first "res" we create is the tail
		newRes := &RegulaAST[TObs]{
			kind:  EXPRESSION_CONCAT,
			left:  sub,
			right: res,
		}
		// If this is the outer-most concat of the sequence, it's not terminal,
		// but the right-most child inside it is.
		res = newRes
	}
	return res
}
