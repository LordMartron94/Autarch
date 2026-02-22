package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memforge"
	"memstruct"
	"unsafe"
)

// ---------------------------------------------------------------------
// SUBSET REGISTRY
// ---------------------------------------------------------------------

type subsetRegistry[TSymbol any, TStateOutcome comparable] struct {
	// Key: raw bytes of subset bitset words.
	// Value: DFA state id.
	mapping map[string]uint64

	subsets       []dfaStateSubset
	accepting     []bool
	outcomes      []TStateOutcome
	nfaStateCount uint64
}

func newSubsetRegistry[TS any, TO comparable](nfaCount uint64) *subsetRegistry[TS, TO] {
	return &subsetRegistry[TS, TO]{
		mapping:       make(map[string]uint64),
		subsets:       make([]dfaStateSubset, 0),
		accepting:     make([]bool, 0),
		outcomes:      make([]TO, 0),
		nfaStateCount: nfaCount,
	}
}

// subsetKey returns a stable string key representing the subset's bitset.
// It is safe for len(words)==0 (degenerate NFA with 0 states).
//
// IMPORTANT: The returned string aliases the underlying bytes.
// In this codebase, subsets are immutable after creation, so this is safe.
func subsetKey(sub *dfaStateSubset) string {
	if len(sub.words) == 0 {
		return ""
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&sub.words[0])), len(sub.words)*8)
	return string(b)
}

func (r *subsetRegistry[TS, TO]) getID(sub *dfaStateSubset) (uint64, bool) {
	id, exists := r.mapping[subsetKey(sub)]
	return id, exists
}

func (r *subsetRegistry[TS, TO]) register(
	sub dfaStateSubset,
	nfa *NFA[TS, TO],
	resFn OutcomeResolutionFn[TO],
) uint64 {
	id := uint64(len(r.subsets))

	r.mapping[subsetKey(&sub)] = id
	r.subsets = append(r.subsets, sub)

	acc, out := resolveDFAOutcome(sub, nfa, resFn)
	r.accepting = append(r.accepting, acc)
	r.outcomes = append(r.outcomes, out)

	return id
}

// ---------------------------------------------------------------------
// OPTIMIZATION CONTEXT
// ---------------------------------------------------------------------

// moveContext carries pre-allocated buffers to avoid heap thrashing.
type moveContext struct {
	nfaStateCount uint64

	// seen dedups move targets without allocating a map.
	seen *dfaStateSubset

	// scratch stores unique move targets for one (subset, symbol) computation.
	scratch []uint64
}

func newMoveContext(nfaStateCount uint64) *moveContext {
	return &moveContext{
		nfaStateCount: nfaStateCount,
		seen:          dfaStateSubsetCreate(nfaStateCount, nil),
		scratch:       make([]uint64, 0, nfaStateCount),
	}
}

func (c *moveContext) reset() {
	c.seen.Clear()
	c.scratch = c.scratch[:0]
}

// ---------------------------------------------------------------------
// CORE CONVERTER
// ---------------------------------------------------------------------

func NFAToDFA[TSymbol any, TStateOutcome comparable](
	nfa *NFA[TSymbol, TStateOutcome],
	minMem, maxMem memcore.MemoryUnitBytes,
	dfaAlloc memarch.AllocationFn,
	resFn OutcomeResolutionFn[TStateOutcome],
) *DFA[TSymbol, TStateOutcome] {
	if resFn == nil {
		resFn = OutcomeResolutionFirst[TStateOutcome]
	}

	allocHandle := createTempAllocator(minMem, maxMem)
	defer memforge.DynamicLinearAllocatorDestroy(allocHandle)

	nfaStateCount := memstruct.ArrayCapacityGet[bool](NFAAcceptingGet(nfa))
	alphabet := NFAAlphabetGet(nfa)

	registry := newSubsetRegistry[TSymbol, TStateOutcome](nfaStateCount)
	moveCtx := newMoveContext(nfaStateCount)

	// Old behavior sizing hint: n^2 (not tight, but consistent with prior).
	maxDFAStates := nfaStateCount * nfaStateCount

	worklist, _ := memarch.MemArchQueueCreate[dfaStateSubset](
		func(sz, al uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocHandle, sz, al)
		},
		maxDFAStates,
	)

	// -----------------------------------------------------------------
	// 1) Entry point MUST be DFA state 0 (invariant)
	// -----------------------------------------------------------------
	startStates := NFAStartingStatesGet(nfa)
	entryClosure := NFAEpsilonClosureCompute(nfa, startStates)
	entrySub := *dfaStateSubsetCreate(nfaStateCount, entryClosure)

	_ = registry.register(entrySub, nfa, resFn)   // ID 0
	memstruct.QueuePushUnsafe(worklist, entrySub) // first processed subset

	// Sink (empty subset) is created lazily, exactly when first needed,
	// matching the old discovery order.
	var sinkInit bool
	var sinkID uint64
	var sinkSub dfaStateSubset

	// -----------------------------------------------------------------
	// 2) Subset construction loop (ordering matches OLD)
	//    - BFS by queue order starting from entry
	//    - symbols in increasing order
	//    - transitions appended in that same order
	// -----------------------------------------------------------------
	var transitions []Transition[TSymbol]
	for !memstruct.QueueIsEmpty[dfaStateSubset](worklist) {
		currentSub := memstruct.QueuePopUnsafe[dfaStateSubset](worklist)
		currentID, _ := registry.getID(&currentSub)

		for symID := uint64(0); symID < uint64(len(alphabet)); symID++ {
			moveCtx.reset()
			computeMove(nfa, &currentSub, symID, moveCtx)

			var nextID uint64
			if len(moveCtx.scratch) == 0 {
				// No move targets => empty subset transition.
				// Create & enqueue sink the first time it is encountered
				// (same as OLD, which discovers it on demand).
				if !sinkInit {
					sinkSub = *dfaStateSubsetCreate(nfaStateCount, nil) // all-zero bitset
					sinkID = registry.register(sinkSub, nfa, resFn)
					memstruct.QueuePushUnsafe(worklist, sinkSub)
					sinkInit = true
				}
				nextID = sinkID
			} else {
				closure := NFAEpsilonClosureCompute(nfa, moveCtx.scratch)
				nextSub := *dfaStateSubsetCreate(nfaStateCount, closure)

				var found bool
				nextID, found = registry.getID(&nextSub)
				if !found {
					nextID = registry.register(nextSub, nfa, resFn)
					memstruct.QueuePushUnsafe(worklist, nextSub)
				}
			}

			transitions = append(transitions, Transition[TSymbol]{
				CurrentState: currentID,
				Symbol:       SymbolCreate[TSymbol](alphabet[symID].Name, symID),
				NextState:    nextID,
			})
		}
	}

	return DFACreate(
		dfaAlloc,
		alphabet,
		transitions,
		registry.accepting,
		registry.outcomes,
		NFAIndexerGet(nfa),
	)
}

// ---------------------------------------------------------------------
// HELPERS
// ---------------------------------------------------------------------

func computeMove[TS any, TO comparable](
	nfa *NFA[TS, TO],
	sub *dfaStateSubset,
	symID uint64,
	ctx *moveContext,
) {
	// Deterministic traversal:
	// - subset.States() ordering
	// - targets slice ordering per transition
	// This eliminates the old map-iteration nondeterminism.
	for _, s := range sub.States() {
		key := [2]uint64{s, symID}
		if targets, ok := nfa.transitions[key]; ok {
			for _, t := range targets {
				if !ctx.seen.Has(t) {
					ctx.seen.Add(t)
					ctx.scratch = append(ctx.scratch, t)
				}
			}
		}
	}
}

func resolveDFAOutcome[TS any, TO comparable](
	sub dfaStateSubset,
	nfa *NFA[TS, TO],
	resFn OutcomeResolutionFn[TO],
) (bool, TO) {
	accArray := NFAAcceptingGet(nfa)
	outArray := NFAOutcomesGet(nfa)

	var aStates []uint64
	var aOutcomes []TO

	for _, s := range sub.States() {
		if memstruct.ArrayItemGetAtUnsafe[bool](accArray, s) {
			aStates = append(aStates, s)
			aOutcomes = append(aOutcomes, memstruct.ArrayItemGetAtUnsafe[TO](outArray, s))
		}
	}

	if len(aStates) == 0 {
		var zero TO
		return false, zero
	}

	outcome, ok := resFn(aStates, aOutcomes)
	return ok, outcome
}

func createTempAllocator(minMem, maxMem memcore.MemoryUnitBytes) memcore.MarkRaw {
	return memforge.DynamicLinearAllocatorCreateFunction(
		uint64(minMem),
		func(curr, need uint64) uint64 {
			nextSize := max(curr*2, need)
			if nextSize > uint64(maxMem) {
				panic(fmt.Errorf("NFA->DFA allocation overflow: %d > %d", nextSize, maxMem))
			}
			return nextSize
		},
	)
}
