package pattern

import (
	"autarch"
	"fmt"
	"memarch"
	"sort"
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
SharedCompilationContext.

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
	instructions []PatternCompilationInstruction[TObs, TOutcome, RegulaAST[TObs]],
	ctx *SharedCompilationContext[TObs, RegulaAST[TObs]],
	nonTerminalOutcome TOutcome,
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

	var nextPos positionID
	ctx.fullPrepare(
		patterns,
		regulaCollectSymbols[TObs],
		func(p *RegulaAST[TObs], feeder symbolFeeder[TObs], bindState interface{}) {
			regulaBindIDs(p, feeder, bindState.(*positionID))
		},
		&nextPos,
	)

	out := make([]*autarch.NFA[TObs, AnnotatedOutcome[TOutcome]], len(instructions))

	for i := range instructions {
		pat := instructions[i].Pattern
		outcome := instructions[i].Outcome

		// 2. Map positions to symbols AND downward-inherit annotations
		pm := buildPosMetadataMap(pat)

		follow := make([]positionSet, pm.maxPos+1)
		for j := range follow {
			follow[j] = newPositionSet(pm.maxPos)
		}

		// 3. Mathematical Analysis (No semantic pollution)
		info := computeGlushkovInfo(pat, follow, pm.maxPos)

		// 4. Synthesis
		out[i] = buildGlushkovNFA(alloc, ctx, pm, follow, info, outcome, nonTerminalOutcome)
	}

	return out, nil
}

func computeGlushkovInfo[TObs any](n *RegulaAST[TObs], follow []positionSet, maxPos positionID) glushkovInfo {
	switch n.kind {
	case EXPRESSION_LITERAL:
		return computeLiteralInfo(n, maxPos)
	case EXPRESSION_CLASS:
		return computeClassInfo(n, maxPos)
	case EXPRESSION_CONCAT:
		return computeConcatInfo(n, follow, maxPos)
	case EXPRESSION_UNION:
		return computeUnionInfo(n, follow, maxPos)
	case EXPRESSION_REPEAT:
		return computeRepeatInfo(n, follow, maxPos)
	default:
		return emptyGlushkovInfo(maxPos)
	}
}

func computeLiteralInfo[TObs any](n *RegulaAST[TObs], maxPos positionID) glushkovInfo {
	if len(n.literals) == 0 {
		return glushkovInfo{
			nullable: true,
			first:    newPositionSet(maxPos),
			last:     newPositionSet(maxPos),
		}
	}

	p := n.literalPosIDs[0]
	fs, ls := newPositionSet(maxPos), newPositionSet(maxPos)
	fs.add(p)
	ls.add(p)
	return glushkovInfo{nullable: false, first: fs, last: ls}
}

func computeClassInfo[TObs any](n *RegulaAST[TObs], maxPos positionID) glushkovInfo {
	p := n.classPosID
	fs, ls := newPositionSet(maxPos), newPositionSet(maxPos)
	fs.add(p)
	ls.add(p)
	return glushkovInfo{nullable: false, first: fs, last: ls}
}

func computeUnionInfo[TObs any](n *RegulaAST[TObs], follow []positionSet, maxPos positionID) glushkovInfo {
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
}

func computeRepeatInfo[TObs any](n *RegulaAST[TObs], follow []positionSet, maxPos positionID) glushkovInfo {
	sub := computeGlushkovInfo(n.sub, follow, maxPos)

	// Traditional Glushkov follow-set update: all last positions of the sub-expression
	// can be followed by any first position of the sub-expression.
	sub.last.iter(func(p positionID) {
		follow[p].union(sub.first)
	})

	return glushkovInfo{
		nullable: (n.min == 0) || sub.nullable,
		first:    sub.first,
		last:     sub.last,
	}
}

func emptyGlushkovInfo(maxPos positionID) glushkovInfo {
	return glushkovInfo{
		nullable: false,
		first:    newPositionSet(maxPos),
		last:     newPositionSet(maxPos),
	}
}

func computeConcatInfo[TObs any](n *RegulaAST[TObs], follow []positionSet, maxPos positionID) glushkovInfo {
	l := computeGlushkovInfo(n.left, follow, maxPos)
	r := computeGlushkovInfo(n.right, follow, maxPos)

	l.last.iter(func(p positionID) { follow[p].union(r.first) })

	res := glushkovInfo{
		nullable: l.nullable && r.nullable,
		first:    newPositionSet(maxPos),
		last:     newPositionSet(maxPos),
	}
	res.first.union(l.first)
	if l.nullable {
		res.first.union(r.first)
	}
	res.last.union(r.last)
	if r.nullable {
		res.last.union(l.last)
	}
	return res
}

func buildGlushkovNFA[TObs any, TOutcome comparable](
	alloc memarch.AllocationFn,
	ctx *SharedCompilationContext[TObs, RegulaAST[TObs]],
	pm posMetadataMap,
	follow []positionSet,
	info glushkovInfo,
	baseOutcome TOutcome,
	nonTerminalOutcome TOutcome,
) *autarch.NFA[TObs, AnnotatedOutcome[TOutcome]] {
	localPositions := buildLocalPositions(pm)
	numLocalPositions := len(localPositions)
	numStates := uint64(numLocalPositions) + 1
	globalToLocal := make(map[positionID]uint64, numLocalPositions)
	for i, p := range localPositions {
		globalToLocal[p] = uint64(i) + 1
	}

	transitions := make([]autarch.Transition[TObs], 0)
	expansionCache := make([][]physicalID, len(ctx.alphabet)+1)
	getExpansion := func(lid logicalID) []physicalID {
		if expansionCache[lid] == nil {
			expansionCache[lid] = ctx.expander.expand(lid)
		}
		return expansionCache[lid]
	}

	emit := func(fromLocal uint64, toPos positionID) {
		toLocal, ok := globalToLocal[toPos]
		if !ok {
			return
		}
		phys := getExpansion(pm.symByPos[toPos])
		for _, pid := range phys {
			transitions = append(transitions, autarch.Transition[TObs]{
				CurrentState: fromLocal,
				NextState:    toLocal,
				Symbol:       autarch.SymbolCreate[TObs]("", uint64(pid)),
			})
		}
	}

	// 1. Initial Transitions: Start State (0) -> First Positions (only positions in this pattern)
	info.first.iter(func(p positionID) {
		if _, ok := globalToLocal[p]; ok {
			emit(0, p)
		}
	})

	// 2. Follow Transitions: only (p, q) where both are in this pattern
	for _, p := range localPositions {
		follow[p].iter(func(q positionID) {
			if _, ok := globalToLocal[q]; ok {
				emit(globalToLocal[p], q)
			}
		})
	}

	// 3. State Metadata: all states nonTerminalOutcome; terminal states get baseOutcome
	outcomes := make([]AnnotatedOutcome[TOutcome], numStates)
	for i := uint64(0); i < numStates; i++ {
		outcomes[i] = AnnotatedOutcome[TOutcome]{Value: nonTerminalOutcome}
	}
	for _, p := range localPositions {
		local := globalToLocal[p]
		var annPtr *AnnotationID
		if a, ok := pm.annByPos[p]; ok {
			val := a
			annPtr = &val
		}
		outcomes[local] = AnnotatedOutcome[TOutcome]{Value: nonTerminalOutcome, Annotation: annPtr}
	}
	if info.nullable {
		outcomes[0] = AnnotatedOutcome[TOutcome]{Value: baseOutcome}
	}
	info.last.iter(func(p positionID) {
		if local, ok := globalToLocal[p]; ok {
			var annPtr *AnnotationID
			if a, ok := pm.annByPos[p]; ok {
				val := a
				annPtr = &val
			}
			outcomes[local] = AnnotatedOutcome[TOutcome]{Value: baseOutcome, Annotation: annPtr}
		}
	})

	return autarch.NFACreate(
		alloc,
		ctx.alphabet,
		transitions,
		nil,
		[]uint64{0},
		outcomes,
		ctx.nondeterministicResolve,
	)
}

// buildLocalPositions returns a sorted slice of position IDs that belong to this pattern
// (keys of pm.symByPos). Used so NFA state count depends only on local position count.
func buildLocalPositions(pm posMetadataMap) []positionID {
	if len(pm.symByPos) == 0 {
		return nil
	}
	out := make([]positionID, 0, len(pm.symByPos))
	for p := range pm.symByPos {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
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

	populateMetadata(&pm, n, nil)
	return pm
}

func populateMetadata[TObs any](pm *posMetadataMap, n *RegulaAST[TObs], currentAnn *AnnotationID) {
	if n == nil {
		return
	}

	// Update the inherited annotation if the current node provides a new one
	if n.annotationID != nil {
		currentAnn = n.annotationID
	}

	switch n.kind {
	case EXPRESSION_LITERAL:
		registerLiteralPositions(pm, n, currentAnn)
	case EXPRESSION_CLASS:
		registerClassPosition(pm, n, currentAnn)
	case EXPRESSION_CONCAT, EXPRESSION_UNION:
		populateMetadata(pm, n.left, currentAnn)
		populateMetadata(pm, n.right, currentAnn)
	case EXPRESSION_REPEAT:
		populateMetadata(pm, n.sub, currentAnn)
	}
}

func registerLiteralPositions[TObs any](pm *posMetadataMap, n *RegulaAST[TObs], ann *AnnotationID) {
	for i, p := range n.literalPosIDs {
		pm.symByPos[p] = n.literalSymIDs[i]
		if ann != nil {
			pm.annByPos[p] = *ann
		}
		if p > pm.maxPos {
			pm.maxPos = p
		}
	}
}

func registerClassPosition[TObs any](pm *posMetadataMap, n *RegulaAST[TObs], ann *AnnotationID) {
	p := n.classPosID
	pm.symByPos[p] = n.classSymID
	if ann != nil {
		pm.annByPos[p] = *ann
	}
	if p > pm.maxPos {
		pm.maxPos = p
	}
}

func normalizeForGlushkov[TObs any](n *RegulaAST[TObs]) (*RegulaAST[TObs], bool) {
	if n == nil {
		return nil, false
	}

	switch n.kind {
	case EXPRESSION_LITERAL:
		return normalizeLiteral(n)
	case EXPRESSION_CONCAT:
		return normalizeBinaryOp(n)
	case EXPRESSION_UNION:
		return normalizeBinaryOp(n)
	case EXPRESSION_REPEAT:
		return normalizeRepeat(n)
	case EXPRESSION_CLASS:
		return n, false
	default:
		return n, false
	}
}

func normalizeLiteral[TObs any](n *RegulaAST[TObs]) (*RegulaAST[TObs], bool) {
	if len(n.literals) <= 1 {
		return n, false
	}

	ann := n.annotationID

	cur := &RegulaAST[TObs]{
		kind:         EXPRESSION_LITERAL,
		literals:     []TObs{n.literals[0]},
		annotationID: ann,
	}

	for i := 1; i < len(n.literals); i++ {
		leaf := &RegulaAST[TObs]{
			kind:         EXPRESSION_LITERAL,
			literals:     []TObs{n.literals[i]},
			annotationID: ann,
		}

		cur = &RegulaAST[TObs]{
			kind:         EXPRESSION_CONCAT,
			left:         cur,
			right:        leaf,
			annotationID: ann,
		}
	}

	return cur, true
}

func normalizeRepeat[TObs any](n *RegulaAST[TObs]) (*RegulaAST[TObs], bool) {
	subNormalized, _ := normalizeForGlushkov(n.sub)
	ann := n.annotationID

	if n.max == -1 {
		return handleInfiniteRepeat(subNormalized, n.min, ann), true
	}

	return unrollBoundedRepeat(subNormalized, n.min, n.max, ann), true
}

func handleInfiniteRepeat[TObs any](sub *RegulaAST[TObs], min int, ann *AnnotationID) *RegulaAST[TObs] {
	// Base cases: * (0 to inf) or + (1 to inf) natively supported by Glushkov algorithm
	if min == 0 || min == 1 {
		return &RegulaAST[TObs]{
			kind:         EXPRESSION_REPEAT,
			sub:          sub,
			min:          min,
			max:          -1,
			annotationID: ann,
		}
	}

	// For min > 1 (e.g., a{3,}), we unroll the prefix and attach a native + to the end
	return unrollInfinitePrefix(sub, min, ann)
}

func unrollInfinitePrefix[TObs any](sub *RegulaAST[TObs], min int, ann *AnnotationID) *RegulaAST[TObs] {
	plusNode := &RegulaAST[TObs]{
		kind:         EXPRESSION_REPEAT,
		sub:          cloneAST(sub), // Critical: clone the isolated sub-tree
		min:          1,
		max:          -1,
		annotationID: ann,
	}

	// Chain the strict prefix (min - 1 times) terminating into the plusNode
	return concatChain(sub, min-1, plusNode, ann)
}

func normalizeBinaryOp[TObs any](n *RegulaAST[TObs]) (*RegulaAST[TObs], bool) {
	l2, _ := normalizeForGlushkov(n.left)
	r2, _ := normalizeForGlushkov(n.right)

	res := &RegulaAST[TObs]{
		kind:         n.kind,
		left:         l2,
		right:        r2,
		annotationID: n.annotationID,
	}
	return res, true
}

func concatChain[TObs any](sub *RegulaAST[TObs], count int, tail *RegulaAST[TObs], ann *AnnotationID) *RegulaAST[TObs] {
	if count <= 0 {
		return assignAnnotation(tail, ann)
	}

	res := tail
	if res == nil {
		res = cloneAST(sub)
		if count == 1 {
			res.annotationID = ann
		}
		count--
	}

	for i := 0; i < count; i++ {
		res = &RegulaAST[TObs]{
			kind:  EXPRESSION_CONCAT,
			left:  cloneAST(sub), // Strictly unique node
			right: res,
		}
	}
	return res
}

func assignAnnotation[TObs any](n *RegulaAST[TObs], ann *AnnotationID) *RegulaAST[TObs] {
	if n != nil && ann != nil {
		n.annotationID = ann
	}
	return n
}

func unrollBoundedRepeat[TObs any](sub *RegulaAST[TObs], min, max int, ann *AnnotationID) *RegulaAST[TObs] {
	if max == 0 {
		return createEpsilon[TObs](ann)
	}

	if min == 0 {
		return buildOptionalChain(sub, max, ann)
	}

	// Exact repeat (min==max): no optional part; return only the fixed chain so Glushkov
	// last-set and follow sets are correct (concat with epsilon can affect accepting positions).
	if min == max {
		return concatChain(sub, min, nil, ann)
	}

	fixedPart := concatChain(sub, min, nil, ann)
	optionalPart := unrollBoundedRepeat(sub, 0, max-min, ann)

	res := &RegulaAST[TObs]{
		kind:  EXPRESSION_CONCAT,
		left:  fixedPart,
		right: optionalPart,
	}
	res.annotationID = ann
	return res
}

func buildOptionalChain[TObs any](sub *RegulaAST[TObs], max int, ann *AnnotationID) *RegulaAST[TObs] {
	cur := createEpsilon[TObs](ann)
	for i := 0; i < max; i++ {
		target := cloneAST(sub)
		if i == 0 {
			cur = makeOptionalNode(target, ann)
		} else {
			concat := &RegulaAST[TObs]{kind: EXPRESSION_CONCAT, left: target, right: cur}
			cur = makeOptionalNode(concat, ann)
		}
	}
	return cur
}

func makeOptionalNode[TObs any](node *RegulaAST[TObs], ann *AnnotationID) *RegulaAST[TObs] {
	optEpsilon := createEpsilon[TObs](ann)
	node.annotationID = ann
	return &RegulaAST[TObs]{
		kind:         EXPRESSION_UNION,
		left:         node,
		right:        optEpsilon,
		annotationID: ann,
	}
}

func createEpsilon[TObs any](ann *AnnotationID) *RegulaAST[TObs] {
	return &RegulaAST[TObs]{
		kind:         EXPRESSION_LITERAL,
		literals:     nil,
		annotationID: ann,
	}
}

func cloneAST[TObs any](n *RegulaAST[TObs]) *RegulaAST[TObs] {
	if n == nil {
		return nil
	}

	clone := &RegulaAST[TObs]{
		kind:         n.kind,
		min:          n.min,
		max:          n.max,
		classPosID:   n.classPosID,
		classSymID:   n.classSymID,
		annotationID: n.annotationID,
	}

	if n.literals != nil {
		clone.literals = make([]TObs, len(n.literals))
		copy(clone.literals, n.literals)
	}

	if len(n.class.ranges) > 0 {
		clone.class.ranges = make([]CharRange[TObs], len(n.class.ranges))
		copy(clone.class.ranges, n.class.ranges)
	}
	if len(n.class.negatedFrom) > 0 {
		clone.class.negatedFrom = make([]CharRange[TObs], len(n.class.negatedFrom))
		copy(clone.class.negatedFrom, n.class.negatedFrom)
	}

	clone.left = cloneAST(n.left)
	clone.right = cloneAST(n.right)
	clone.sub = cloneAST(n.sub)

	return clone
}
