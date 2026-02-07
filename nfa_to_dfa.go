package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memforge"
	"memstruct"
)

/*
NFAToDFA converts a Non-Deterministic Finite Automaton to an equivalent
Deterministic Finite Automaton using subset construction.

The algorithm builds DFA states as sets of NFA states, starting with the
epsilon closure of the NFA's starting states. Each DFA state represents
all possible NFA states that could be active after processing a prefix.

Use cases:
- Converting NFAs to executable DFAs
- Optimizing automata for efficient execution
- Preparing automata for minimization

Time complexity: O(2^n * a) worst case where n is NFA states, a is alphabet size
Space complexity: O(2^n * a) for DFA states and transitions

Prerequisites:
- nfa must be a valid NFA instance
- minTempAllocatorMemory and maxTempAllocatorMemory must be sufficient
- dfaAllocationFn must provide memory for the resulting DFA
- invalidOutcome must be distinct from valid outcomes

Edge cases:
- Panics if temporary allocator exceeds maxTempAllocatorMemory
- Empty NFA results in single-state DFA
- Worst-case exponential blowup possible (rare in practice)
- Uses linear search for subset matching (intentional design choice)
*/
func NFAToDFA[TSymbol any, TStateOutcome comparable](
	nfa *NFA[TSymbol, TStateOutcome],
	minTempAllocatorMemory, maxTempAllocatorMemory memcore.MemoryUnitBytes,
	dfaAllocationFn memarch.AllocationFn,
	invalidOutcome TStateOutcome,
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
		outcome := determineOutcome[TStateOutcome](&sub, stateArray, invalidOutcome)
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

		// For each symbol in the alphabet, compute the target subset
		for symbolID := uint64(0); symbolID < uint64(len(alphabet)); symbolID++ {
			// Directly look up transitions by symbol ID (no need for observations)
			nfaMoves := make(map[uint64]struct{})
			for _, state := range subset.States() {
				key := [2]uint64{state, symbolID}
				nextStates, exists := nfa.transitions[key]
				if exists {
					for _, ns := range nextStates {
						nfaMoves[ns] = struct{}{}
					}
				}
			}

			// Convert to slice for epsilon closure
			moveSlice := make([]uint64, 0, len(nfaMoves))
			for ns := range nfaMoves {
				moveSlice = append(moveSlice, ns)
			}

			nfaClosure := NFAEpsilonClosureCompute(nfa, moveSlice)
			newSubset := *dfaStateSubsetCreate(nfaClosure)

			// Check if we've seen this subset before
			nextID, exists := findSubsetID(&newSubset)
			if !exists {
				nextID = addSubset(newSubset)
			}

			// Create symbol from the symbol definition
			symDef := alphabet[symbolID]
			symbol := SymbolCreate[TSymbol](symDef.Name, symbolID)

			dfaTransitions = append(dfaTransitions, Transition[TSymbol]{
				CurrentState: currentID,
				Symbol:       symbol,
				NextState:    nextID,
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
