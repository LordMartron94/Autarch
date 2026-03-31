package autarch

import (
	"fmt"
	"foundation/formatting"
	"memarch"
	"memcore"
	"memforge"
	"memstruct"
	"unsafe"
)

// ---------------------------------------------------------------------
// SUBSET REGISTRY
// ---------------------------------------------------------------------

type subsetRegistry[TSymbol, TStateOutcome any] struct {
	// Key: raw bytes of subset bitset words.
	// Value: DFA state id.
	mapping map[string]uint64

	subsets       []dfaStateSubset
	outcomes      []TStateOutcome
	nfaStateCount uint64
}

func newSubsetRegistry[TS, TO any](nfaCount uint64) *subsetRegistry[TS, TO] {
	return &subsetRegistry[TS, TO]{
		mapping:       make(map[string]uint64),
		subsets:       make([]dfaStateSubset, 0),
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

	out := resolveDFAOutcome(sub, nfa, resFn)
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

func NFAToDFA[TSymbol, TStateOutcome any](
	nfa *NFA[TSymbol, TStateOutcome],
	minMem, maxMem memcore.MemoryUnitBytes,
	dfaAlloc memarch.AllocationFn,
	deterministicResolver DeterministicSymbolResolver[TSymbol],
	resFn OutcomeResolutionFn[TStateOutcome],
) *DFA[TSymbol, TStateOutcome] {
	if resFn == nil {
		resFn = OutcomeResolutionFirst[TStateOutcome]
	}
	if deterministicResolver == nil {
		panic("NFAToDFA: deterministic resolver is required")
	}

	allocHandle := createTempAllocator(minMem, maxMem)
	defer memforge.DynamicLinearAllocatorDestroy(allocHandle)

	nfaStateCount := NFANumStates(nfa)
	alphabet := NFAAlphabetGet(nfa)

	registry := newSubsetRegistry[TSymbol, TStateOutcome](nfaStateCount)
	moveCtx := newMoveContext(nfaStateCount)

	// Old behavior sizing hint: n^2 (not tight, but consistent with prior).
	maxDFAStates := nfaStateCount * nfaStateCount

	worklistBytes := NFAToDFAWorklistBytesRequired(nfaStateCount)
	if worklistBytes > uint64(maxMem) {
		panic(fmt.Errorf(
			"NFA->DFA worklist would exceed temp limit: nfa_states=%d worklist_capacity=%d estimated_bytes=%s max_temp=%s",
			nfaStateCount,
			maxDFAStates,
			formatting.FormatMemoryBytes(worklistBytes),
			formatting.FormatMemoryBytes(uint64(maxMem)),
		))
	}

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
				// No move targets => encode dead transition directly.
				nextID = DeadState
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

	for _, symDef := range alphabet {
		if symDef.Observation == nil {
			continue
		}
		symbolID, ok := deterministicResolver(*symDef.Observation)
		if !ok || symbolID >= uint64(len(alphabet)) {
			panic(fmt.Errorf("NFAToDFA: deterministic resolver produced invalid symbol for ID=%d name=%q", symDef.ID, symDef.Name))
		}
	}

	return DFACreate(
		dfaAlloc,
		alphabet,
		transitions,
		registry.outcomes,
		deterministicResolver,
	)
}

// NFAToDFAWorklistBytesRequired returns the number of bytes required for the
// subset-construction worklist queue given an NFA state count (capacity n²).
// Use this to check against maxMem before calling NFAToDFA or to report diagnostics.
func NFAToDFAWorklistBytesRequired(nfaStateCount uint64) uint64 {
	maxDFAStates := nfaStateCount * nfaStateCount
	return memstruct.QueueRequiredBytesGet[dfaStateSubset](maxDFAStates)
}

func computeMove[TS, TO any](
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

func resolveDFAOutcome[TS, TO any](
	sub dfaStateSubset,
	nfa *NFA[TS, TO],
	resFn OutcomeResolutionFn[TO],
) TO {
	outArray := NFAOutcomesGet(nfa)

	var allStates []uint64
	var allOutcomes []TO

	for _, s := range sub.States() {
		allStates = append(allStates, s)
		allOutcomes = append(allOutcomes, memstruct.ArrayItemGetAtUnsafe[TO](outArray, s))
	}

	if len(allStates) == 0 {
		var zero TO
		return zero
	}

	outcome, ok := resFn(allStates, allOutcomes)
	if !ok {
		var zero TO
		return zero
	}

	return outcome
}

func createTempAllocator(minMem, maxMem memcore.MemoryUnitBytes) memcore.MarkRaw {
	return memforge.DynamicLinearAllocatorCreateFunction(
		uint64(minMem),
		func(curr, need uint64) uint64 {
			nextSize := max(curr*2, need)
			if nextSize > uint64(maxMem) {
				panic(fmt.Errorf("NFA->DFA allocation overflow: %s > %s", formatting.FormatMemoryBytes(nextSize), formatting.FormatMemoryBytes(uint64(maxMem))))
			}
			return nextSize
		},
	)
}
