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

The resolution function determines which outcome to assign when multiple
accepting NFA states are merged into a single DFA state. Non-accepting DFA
states carry no semantic outcome (tracked via an explicit accepting set).

Use cases:
- Converting NFAs to executable DFAs
- Optimizing automata for efficient execution
- Preparing automata for minimization
- Custom outcome resolution during conversion

Time complexity: O(2^n * a) worst case where n is NFA states, a is alphabet size
Space complexity: O(2^n * a) for DFA states and transitions

Prerequisites:
- nfa must be a valid NFA instance
- minTempAllocatorMemory and maxTempAllocatorMemory must be sufficient
- dfaAllocationFn must provide memory for the resulting DFA
- resolutionFn must be a valid OutcomeResolutionFn (nil uses default: OutcomeResolutionFirst)

Edge cases:
- Panics if temporary allocator exceeds maxTempAllocatorMemory
- Empty NFA results in single-state DFA
- Worst-case exponential blowup possible (rare in practice)
- Uses linear search for subset matching (intentional design choice)
- If resolutionFn is nil, uses OutcomeResolutionFirst as default

Outcome semantics:
- Accepting DFA states are tracked via a boolean accepting set
- Outcomes are only meaningful for accepting states
- Lexer error handling / fallback behavior is delegated to higher-level execution layers
*/
func NFAToDFA[TSymbol any, TStateOutcome comparable](
	nfa *NFA[TSymbol, TStateOutcome],
	minTempAllocatorMemory, maxTempAllocatorMemory memcore.MemoryUnitBytes,
	dfaAllocationFn memarch.AllocationFn,
	resolutionFn OutcomeResolutionFn[TStateOutcome],
) *DFA[TSymbol, TStateOutcome] {
	if resolutionFn == nil {
		resolutionFn = OutcomeResolutionFirst[TStateOutcome]
	}

	// ───────────────────────────────────────────────────────────────
	//  Temporary region allocator
	// ───────────────────────────────────────────────────────────────
	allocator := memforge.DynamicLinearAllocatorCreateFunction(
		uint64(minTempAllocatorMemory),
		func(currentCap, neededCap uint64) uint64 {
			newSize := max(currentCap*2, neededCap)
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
	accArray := NFAAcceptingGet(nfa) // Array[bool]
	outArray := NFAOutcomesGet(nfa)  // Array[TStateOutcome]

	nfaStateCount := memstruct.ArrayCapacityGet[bool](accArray)
	alphabet := NFAAlphabetGet(nfa)
	alphabetSize := len(alphabet)

	// Sizing hint only
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
	//  Subset → ID mapping (Go slice)
	// ───────────────────────────────────────────────────────────────
	subsets := make([]dfaStateSubset, 0, maxDFAStates)

	dfaAccepting := make([]bool, 0, maxDFAStates)
	dfaOutcomes := make([]TStateOutcome, 0, maxDFAStates)

	findSubsetID := func(target *dfaStateSubset) (uint64, bool) {
		for i := range subsets {
			if subsets[i].Equals(target) {
				return uint64(i), true
			}
		}
		return 0, false
	}

	addSubset := func(sub dfaStateSubset) uint64 {
		id := uint64(len(subsets))
		subsets = append(subsets, sub)

		allStates := sub.States()

		acceptStates := make([]uint64, 0, len(allStates))
		acceptOutcomes := make([]TStateOutcome, 0, len(allStates))

		for _, s := range allStates {
			if memstruct.ArrayItemGetAtUnsafe[bool](accArray, s) {
				acceptStates = append(acceptStates, s)
				acceptOutcomes = append(acceptOutcomes, memstruct.ArrayItemGetAtUnsafe[TStateOutcome](outArray, s))
			}
		}

		if len(acceptStates) == 0 {
			dfaAccepting = append(dfaAccepting, false)
			var zero TStateOutcome
			dfaOutcomes = append(dfaOutcomes, zero) // ignored when non-accepting
		} else {
			outcome, ok := resolutionFn(acceptStates, acceptOutcomes)
			if ok {
				dfaAccepting = append(dfaAccepting, true)
				dfaOutcomes = append(dfaOutcomes, outcome)
			} else {
				// Resolution chose no outcome → non-accepting DFA state
				dfaAccepting = append(dfaAccepting, false)

				var zero TStateOutcome
				dfaOutcomes = append(dfaOutcomes, zero) // ignored when non-accepting
			}
		}

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
	//  Subset construction loop
	// ───────────────────────────────────────────────────────────────
	dfaTransitions := make([]Transition[TSymbol], 0, maxDFAStates*uint64(alphabetSize))

	for !memstruct.QueueIsEmpty[dfaStateSubset](worklist) {
		subset := memstruct.QueuePopUnsafe[dfaStateSubset](worklist)

		currentID, found := findSubsetID(&subset)
		if !found {
			panic("NFAToDFA: subset popped from queue but not present in subset list")
		}

		for symbolID := uint64(0); symbolID < uint64(alphabetSize); symbolID++ {

			nfaMoves := make(map[uint64]struct{})

			for _, state := range subset.States() {
				key := [2]uint64{state, symbolID}
				nextStates, exists := nfa.transitions[key]
				if !exists {
					continue
				}
				for _, ns := range nextStates {
					nfaMoves[ns] = struct{}{}
				}
			}

			moveSlice := make([]uint64, 0, len(nfaMoves))
			for ns := range nfaMoves {
				moveSlice = append(moveSlice, ns)
			}

			nfaClosure := NFAEpsilonClosureCompute(nfa, moveSlice)
			newSubset := *dfaStateSubsetCreate(nfaClosure)

			nextID, exists := findSubsetID(&newSubset)
			if !exists {
				nextID = addSubset(newSubset)
			}

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
	//  Build DFA object
	// ───────────────────────────────────────────────────────────────
	return DFACreate(
		dfaAllocationFn,
		alphabet,
		dfaTransitions,
		dfaAccepting,
		dfaOutcomes,
		indexer,
	)
}
