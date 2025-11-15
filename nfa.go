package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memstruct"
	"sort"
)

// NFA is a non-deterministic finite automaton.
type NFA[TObservation any, TStateOutcome comparable] struct {
	states      memcore.MarkRaw        // Array[TStateOutcome]
	transitions map[[2]uint64][]uint64 // TODO - maybe replace this with a manually allocated data structure? This would require dynamically sized structures, which I do not want to implement yet.

	alphabet       []TObservation
	startingStates []uint64

	indexer   SymbolIndexer[TObservation]
	numStates uint64
}

func NFACreate[TObservation any, TStateOutcome comparable](
	allocFn memarch.AllocationFn,
	alphabet []TObservation,
	transitions []Transition[TObservation],
	startingStates []uint64,
	states []TStateOutcome,
	indexer SymbolIndexer[TObservation],
) *NFA[TObservation, TStateOutcome] {
	alphabetSize := uint64(len(alphabet))
	numStates := uint64(len(states))

	// 1. State Outcomes
	stateTable, _ := memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)
	for stateNum, stateOutcome := range states {
		memstruct.ArraySetAtUnsafe(stateTable, uint64(stateNum), stateOutcome)
	}

	// 2. Transitions
	transitionTable := make(map[[2]uint64][]uint64)
	for _, transition := range transitions {
		if transition.Symbol.SymbolID >= alphabetSize && transition.Symbol.SymbolID != AutarchEpsilonID {
			panic(fmt.Errorf("symbol ID must be less than the alphabetSize, got=%d,max=%d", transition.Symbol.SymbolID, alphabetSize))
		}

		key := [2]uint64{transition.CurrentState, transition.Symbol.SymbolID}
		if _, exist := transitionTable[key]; !exist {
			transitionTable[key] = []uint64{transition.NextState}
		} else {
			transitionTable[key] = append(transitionTable[key], transition.NextState)
		}
	}

	return &NFA[TObservation, TStateOutcome]{
		states:         stateTable,
		transitions:    transitionTable,
		startingStates: startingStates,
		indexer:        indexer,
		numStates:      numStates,
		alphabet:       alphabet,
	}
}

func NFADebugPrint[TObservation any, TStateOutcome comparable](
	nfa *NFA[TObservation, TStateOutcome],
) {

	fmt.Println("===== NFA DEBUG PRINT =====")

	// -------------------------------------------------------
	// Alphabet section
	// -------------------------------------------------------
	fmt.Println("Alphabet:")
	for i, sym := range nfa.alphabet {
		idx, ok := nfa.indexer(sym)
		if !ok {
			fmt.Printf("  [%d] <invalid index>\n", i)
		} else {
			fmt.Printf("  [%d] symbolID=%d  (%v)\n", i, idx.SymbolID, sym)
		}
	}
	fmt.Println()

	// -------------------------------------------------------
	// States section
	// -------------------------------------------------------
	fmt.Println("States (index → outcome):")
	for s := uint64(0); s < nfa.numStates; s++ {
		out := memstruct.ArrayItemGetAtUnsafe[TStateOutcome](nfa.states, s)
		fmt.Printf("  State %d → %v\n", s, out)
	}
	fmt.Println()

	// -------------------------------------------------------
	// Starting states
	// -------------------------------------------------------
	fmt.Println("Starting States:")

	startCopy := make([]uint64, len(nfa.startingStates))
	copy(startCopy, nfa.startingStates)
	sort.Slice(startCopy, func(i, j int) bool { return startCopy[i] < startCopy[j] })

	for _, st := range startCopy {
		fmt.Printf("  %d\n", st)
	}
	fmt.Println()

	// -------------------------------------------------------
	// Transitions
	// -------------------------------------------------------
	fmt.Println("Transitions:")

	keys := make([][2]uint64, 0, len(nfa.transitions))
	for k := range nfa.transitions {
		keys = append(keys, k)
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] == keys[j][0] {
			return keys[i][1] < keys[j][1]
		}
		return keys[i][0] < keys[j][0]
	})

	for _, k := range keys {
		from := k[0]
		symbolID := k[1]

		var symbolDesc string
		if symbolID == AutarchEpsilonID {
			symbolDesc = "ε"
		} else if symbolID < uint64(len(nfa.alphabet)) {
			sym := nfa.alphabet[symbolID]
			symbolDesc = fmt.Sprintf("%v", sym)
		} else {
			symbolDesc = fmt.Sprintf("<invalid symbol id=%d>", symbolID)
		}

		nexts := nfa.transitions[k]

		nsCopy := make([]uint64, len(nexts))
		copy(nsCopy, nexts)
		sort.Slice(nsCopy, func(i, j int) bool { return nsCopy[i] < nsCopy[j] })

		fmt.Printf("  (%d) --[%s]--> %v\n", from, symbolDesc, nsCopy)
	}

	fmt.Println("===== END NFA DEBUG PRINT =====")
}

// NFAIndexerGet returns the symbol indexer used by this NFA.
func NFAIndexerGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) SymbolIndexer[TObservation] {
	return nfa.indexer
}

// NFAStatesGet returns Array[TStateOutcome] of states.
func NFAStatesGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return nfa.states
}

// NFAStartingStatesGet returns the array of starting states.
func NFAStartingStatesGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) []uint64 {
	return nfa.startingStates
}

// NFAAlphabetGet returns the NFA's alphabet.
func NFAAlphabetGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) []TObservation {
	return nfa.alphabet
}

func NFARun[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome], input []TObservation) ([]TStateOutcome, error) {
	current := epsilonClosure(nfa, nfa.startingStates)

	// assert.go:10: Assertion failure: incorrect outcome, expected=false, got=true, input=b

	for _, observation := range input {
		symbol, ok := nfa.indexer(observation)
		if !ok {
			return nil, fmt.Errorf("invalid symbol: %s", symbol.SymbolDescription)
		}

		nextSet := make(map[uint64]struct{})

		for _, state := range current {
			key := [2]uint64{state, symbol.SymbolID}
			for _, ns := range nfa.transitions[key] {
				nextSet[ns] = struct{}{}
			}
		}

		if len(nextSet) == 0 {
			return nil, nil
		}

		current = epsilonClosure(nfa, mapKeys(nextSet))
	}

	results := make([]TStateOutcome, len(current))
	for i, s := range current {
		results[i] = memstruct.ArrayItemGetAtUnsafe[TStateOutcome](nfa.states, s)
	}

	return results, nil
}

// NFAEpsilonClosureCompute computes the epsilon closure for this nfa for a set of states.
func NFAEpsilonClosureCompute[TSymbol any, TStateOutcome comparable](
	nfa *NFA[TSymbol, TStateOutcome],
	states []uint64,
) []uint64 {
	return epsilonClosure(nfa, states)
}

// NFATransitionsForStates takes a list of states and a symbol,
// and returns a slice of slices, where each inner slice contains
// the next states reachable from that specific input state.
func NFATransitionsForStates[TObservation any, TStateOutcome comparable](
	nfa *NFA[TObservation, TStateOutcome],
	states []uint64,
	observation TObservation,
) ([]uint64, error) {
	symbol, ok := nfa.indexer(observation)
	if !ok {
		return nil, fmt.Errorf("invalid symbol: %v", observation)
	}

	out := make(map[uint64]struct{})

	for _, s := range states {
		key := [2]uint64{s, symbol.SymbolID}
		next, exists := nfa.transitions[key]
		if !exists {
			continue
		}

		for _, ns := range next {
			out[ns] = struct{}{}
		}
	}

	// Convert to slice
	result := make([]uint64, 0, len(out))
	for ns := range out {
		result = append(result, ns)
	}

	return result, nil
}

// ------------------------------------------------------------ PRIVATE HELPERS

func mapKeys(m map[uint64]struct{}) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func epsilonClosure[TObservation any, TStateOutcome comparable](
	nfa *NFA[TObservation, TStateOutcome],
	states []uint64,
) []uint64 {

	// Result set (deduplicated)
	closure := make(map[uint64]struct{}, len(states))

	// Stack for DFS/graph walk
	stack := make([]uint64, 0, len(states))

	// Initialize closure and stack with starting states
	for _, s := range states {
		if _, exists := closure[s]; !exists {
			closure[s] = struct{}{}
			stack = append(stack, s)
		}
	}

	// Explore all reachable epsilon transitions
	for len(stack) > 0 {
		// Pop last element
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		key := [2]uint64{s, AutarchEpsilonID}
		nextStates, exists := nfa.transitions[key]
		if !exists {
			continue
		}

		// Visit all epsilon-neighbors
		for _, ns := range nextStates {
			if _, seen := closure[ns]; !seen {
				closure[ns] = struct{}{}
				stack = append(stack, ns)
			}
		}
	}

	// Convert closure map to slice
	return mapKeys(closure)
}
