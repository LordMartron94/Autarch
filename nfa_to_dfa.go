package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memforge"
	"memstruct"
)

// NFAToDFA transforms an NFA into an equivalent DFA using subset construction.
// It does NOT use memstruct.HashMap for subset → stateID mapping, to avoid the
// segfault you are observing in HashMapItemAdd. Instead it uses a simple
// slice of subsets and linear search. DFA state ID = index in that slice.
func NFAToDFA[TSymbol any, TStateOutcome comparable](
	nfa *NFA[TSymbol, TStateOutcome],
	minTempAllocatorMemory, maxTempAllocatorMemory memcore.MemoryUnitBytes,
	dfaAllocationFn memarch.AllocationFn,
) *DFA[TSymbol, TStateOutcome] {

	// ───────────────────────────────────────────────────────────────
	//  Temporary region allocator
	// ───────────────────────────────────────────────────────────────
	allocator := memforge.DynamicLinearAllocatorCreateFunction(
		uint64(minTempAllocatorMemory),
		func(currentCap, neededCap uint64) uint64 {
			newSize := currentCap * 2
			if newSize < neededCap {
				newSize = neededCap
			}
			if newSize > uint64(maxTempAllocatorMemory) {
				panic(fmt.Errorf("subset construction needs too much memory: need=%v max=%v", newSize, maxTempAllocatorMemory))
			}
			return newSize
		},
	)
	defer memforge.DynamicLinearAllocatorDestroy(allocator)

	// ───────────────────────────────────────────────────────────────
	//  NFA static information
	// ───────────────────────────────────────────────────────────────
	stateArray := NFAStatesGet(nfa)
	nfaStateCount := memstruct.ArrayCapacityGet[TStateOutcome](stateArray)
	alphabet := NFAAlphabetGet(nfa)
	alphabetSize := len(alphabet)

	// This is just a sizing hint for slices, not a hard bound.
	maxDFAStates := nfaStateCount * nfaStateCount

	// ───────────────────────────────────────────────────────────────
	//  Worklist queue (stores DFA subsets) – lives in manual memory
	// ───────────────────────────────────────────────────────────────
	worklist, _ := memarch.MemArchQueueCreate[dfaStateSubset](
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		maxDFAStates,
	)

	// ───────────────────────────────────────────────────────────────
	//  Subset → ID mapping: maintained as a Go slice.
	//  ID of a subset is simply its index in this slice.
	//  No pointers into manual memory are stored.
	// ───────────────────────────────────────────────────────────────
	subsets := make([]dfaStateSubset, 0, maxDFAStates)
	dfaOutcomes := make([]TStateOutcome, 0, maxDFAStates)

	// helper: find existing subset ID
	findSubsetID := func(target *dfaStateSubset) (uint64, bool) {
		for i := range subsets {
			if subsets[i].Equals(target) {
				return uint64(i), true
			}
		}
		return 0, false
	}

	// helper: add new subset and return its ID
	addSubset := func(sub dfaStateSubset) uint64 {
		id := uint64(len(subsets))
		subsets = append(subsets, sub)
		outcome, _ := determineOutcome[TStateOutcome](&sub, stateArray)
		dfaOutcomes = append(dfaOutcomes, outcome)
		memstruct.QueuePushUnsafe(worklist, sub)
		return id
	}

	indexer := NFAIndexerGet(nfa)

	// ───────────────────────────────────────────────────────────────
	//  Initialize with ε-closure(start) as DFA state 0
	// ───────────────────────────────────────────────────────────────
	startStates := NFAStartingStatesGet(nfa)
	startClosure := NFAEpsilonClosureCompute(nfa, startStates)
	startSubset := *dfaStateSubsetCreate(startClosure)

	_ = addSubset(startSubset)

	// ───────────────────────────────────────────────────────────────
	//  Subset Construction Loop
	// ───────────────────────────────────────────────────────────────
	dfaTransitions := make([]Transition[TSymbol], 0, maxDFAStates*uint64(alphabetSize))

	for !memstruct.QueueIsEmpty[dfaStateSubset](worklist) {

		// get next subset to expand
		subset := memstruct.QueuePopUnsafe[dfaStateSubset](worklist)

		currentID, found := findSubsetID(&subset)
		if !found {
			// This should not happen: every subset in the queue is created via addSubset.
			// If it does, it's a logic error, panic for now.
			panic("NFAToDFA: subset popped from queue but not present in subset list")
		}

		// For each observation, compute the target subset
		for _, observation := range alphabet {

			nfaMoves, _ := NFATransitionsForStates(nfa, subset.States(), observation)
			nfaClosure := NFAEpsilonClosureCompute(nfa, nfaMoves)

			newSubset := *dfaStateSubsetCreate(nfaClosure)

			// Check if we've seen this subset before
			nextID, exists := findSubsetID(&newSubset)
			if !exists {
				nextID = addSubset(newSubset)
			}

			symbol, _ := indexer(observation)

			dfaTransitions = append(dfaTransitions, Transition[TSymbol]{
				currentState: currentID,
				symbol:       symbol,
				nextState:    nextID,
			})
		}
	}

	// ───────────────────────────────────────────────────────────────
	//  Build DFA object in manual memory
	// ───────────────────────────────────────────────────────────────
	return DFACreate(
		dfaAllocationFn,
		alphabet,
		dfaTransitions,
		dfaOutcomes,
		indexer,
	)
}
