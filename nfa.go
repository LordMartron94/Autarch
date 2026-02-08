package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memstruct"
	"sort"
)

/*
NFA represents a Non-Deterministic Finite Automaton.

An NFA allows multiple transitions from a single state for the same input symbol,
and supports epsilon (ε) transitions that consume no input. This flexibility
makes NFAs easier to construct but less efficient to execute than DFAs.

Use cases:
- Building automata from regular expressions or patterns
- Combining multiple automata via union operations
- Converting to DFA for efficient execution

Time complexity:
- State transitions: O(1) per transition lookup
- Epsilon closure: O(n) where n is the number of states
- Input processing: O(n * m) where n is input length, m is states per step

Space complexity: O(s * a) where s is number of states, a is alphabet size

Prerequisites:
- Alphabet must be provided with corresponding indexer function
- States array must be pre-allocated with outcomes
- Transitions must use valid symbol IDs from the alphabet

Edge cases:
- Empty input returns outcomes from starting states' epsilon closure
- Invalid symbols return errors during execution
- Dead states (no transitions) terminate processing early
*/
type NFA[TObservation any, TStateOutcome comparable] struct {
	accepting memcore.MarkRaw
	outcomes  memcore.MarkRaw

	transitions  map[[2]uint64][]uint64
	epsilonEdges map[uint64][]uint64

	alphabet       []SymbolDefinition[TObservation]
	startingStates []uint64

	indexer   SymbolIndexer[TObservation]
	numStates uint64
}

/*
NFACreate constructs a new Non-Deterministic Finite Automaton.

The function allocates memory for state outcomes and builds the transition table
from the provided transitions. Multiple transitions from the same state with the
same symbol are allowed, enabling non-deterministic behavior. Epsilon transitions
are provided separately and stored in the epsilonEdges map.

Use cases:
- Creating NFAs from regular expression patterns
- Building automata programmatically
- Constructing NFAs for later conversion to DFA

Time complexity: O(t) where t is the number of transitions
Space complexity: O(s + t) where s is number of states, t is transitions

Prerequisites:
- alphabet must contain all symbols used in transitions
- startingStates must contain valid state indices (0 <= state < len(states))
- transitions must use valid symbol IDs (0 <= SymbolID < len(alphabet))
- epsilonEdges can be nil (will be initialized as empty map)
- indexer must correctly map observations to symbols in the alphabet

Edge cases:
- Panics if symbol ID exceeds alphabet size
- Empty startingStates creates an automaton that accepts nothing
- Duplicate transitions are preserved (non-deterministic behavior)
*/
func NFACreate[TObservation any, TStateOutcome comparable](
	allocFn memarch.AllocationFn,
	alphabet []SymbolDefinition[TObservation],
	transitions []Transition[TObservation],
	epsilonEdges map[uint64][]uint64,
	startingStates []uint64,
	accepting []bool,
	outcomes []TStateOutcome,
	indexer SymbolIndexer[TObservation],
) *NFA[TObservation, TStateOutcome] {
	if len(accepting) != len(outcomes) {
		panic("accepting and outcomes must be same length")
	}

	alphabetSize := uint64(len(alphabet))
	numStates := uint64(len(outcomes))

	validateAlphabet(alphabet, "nfa")

	acceptTable, _ := memarch.MemArchArrayCreate[bool](allocFn, numStates)
	outcomeTable, _ := memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)

	for i := uint64(0); i < numStates; i++ {
		memstruct.ArraySetAtUnsafe(acceptTable, i, accepting[i])
		if accepting[i] {
			memstruct.ArraySetAtUnsafe(outcomeTable, i, outcomes[i])
		}
	}

	transitionTable := make(map[[2]uint64][]uint64)
	for _, transition := range transitions {
		if transition.Symbol.SymbolID >= alphabetSize {
			panic(fmt.Errorf("symbol ID must be less than the alphabetSize, got=%d,max=%d", transition.Symbol.SymbolID, alphabetSize))
		}

		key := [2]uint64{transition.CurrentState, transition.Symbol.SymbolID}
		if _, exist := transitionTable[key]; !exist {
			transitionTable[key] = []uint64{transition.NextState}
		} else {
			transitionTable[key] = append(transitionTable[key], transition.NextState)
		}
	}

	epsilonMap := make(map[uint64][]uint64)
	for state, nextStates := range epsilonEdges {
		epsilonMap[state] = append([]uint64{}, nextStates...)
	}

	return &NFA[TObservation, TStateOutcome]{
		outcomes:       outcomeTable,
		accepting:      acceptTable,
		transitions:    transitionTable,
		epsilonEdges:   epsilonMap,
		startingStates: startingStates,
		indexer:        indexer,
		numStates:      numStates,
		alphabet:       alphabet,
	}
}

/*
NFADebugPrint outputs a human-readable representation of the NFA to stdout.

The output includes the alphabet, state outcomes, starting states, and all
transitions in a sorted, deterministic format for debugging purposes.

Use cases:
- Debugging automaton construction
- Verifying transition correctness
- Understanding automaton structure

Time complexity: O(s + t) where s is states, t is transitions
Space complexity: O(1) - only temporary sorting buffers

Prerequisites:
- nfa must be a valid NFA instance

Edge cases:
- Handles empty alphabets and state sets gracefully
- Invalid symbol IDs are marked in output
*/
func NFADebugPrint[TObservation any, TStateOutcome comparable](
	nfa *NFA[TObservation, TStateOutcome],
) {

	fmt.Println("===== NFA DEBUG PRINT =====")

	// -------------------------------------------------------
	// Alphabet
	// -------------------------------------------------------
	fmt.Println("Alphabet:")
	for i, symDef := range nfa.alphabet {
		fmt.Printf("  [%d] symbolID=%d  name=%s\n", i, symDef.ID, symDef.Name)
	}
	fmt.Println()

	// -------------------------------------------------------
	// States (accepting + outcomes)
	// -------------------------------------------------------
	fmt.Println("States:")

	accCur := memstruct.ArrayCursorCreate[bool](nfa.accepting)
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](nfa.outcomes)

	for s := uint64(0); s < nfa.numStates; s++ {

		isAccepting := *accCur.PtrAt(s)

		if isAccepting {
			out := *outCur.PtrAt(s)
			fmt.Printf("  State %d → %v\n", s, out)
		} else {
			fmt.Printf("  State %d → —\n", s)
		}
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
	// Symbol Transitions
	// -------------------------------------------------------
	fmt.Println("Symbol Transitions:")

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
		if symbolID < uint64(len(nfa.alphabet)) {
			symDef := nfa.alphabet[symbolID]
			symbolDesc = symDef.Name
		} else {
			symbolDesc = fmt.Sprintf("<invalid symbol id=%d>", symbolID)
		}

		nexts := nfa.transitions[k]

		nsCopy := make([]uint64, len(nexts))
		copy(nsCopy, nexts)
		sort.Slice(nsCopy, func(i, j int) bool { return nsCopy[i] < nsCopy[j] })

		fmt.Printf("  (%d) --[%s]--> %v\n", from, symbolDesc, nsCopy)
	}
	fmt.Println()

	// -------------------------------------------------------
	// Epsilon edges
	// -------------------------------------------------------
	fmt.Println("Epsilon Edges:")

	epsilonKeys := make([]uint64, 0, len(nfa.epsilonEdges))
	for k := range nfa.epsilonEdges {
		epsilonKeys = append(epsilonKeys, k)
	}
	sort.Slice(epsilonKeys, func(i, j int) bool { return epsilonKeys[i] < epsilonKeys[j] })

	for _, from := range epsilonKeys {
		nexts := nfa.epsilonEdges[from]

		nsCopy := make([]uint64, len(nexts))
		copy(nsCopy, nexts)
		sort.Slice(nsCopy, func(i, j int) bool { return nsCopy[i] < nsCopy[j] })

		fmt.Printf("  (%d) --[ε]--> %v\n", from, nsCopy)
	}

	fmt.Println("===== END NFA DEBUG PRINT =====")
}

/*
NFAIndexerGet retrieves the symbol indexer function for the NFA.

The indexer maps observations to symbols, enabling the NFA to process input
and determine valid transitions.

Use cases:
- Accessing the indexer for custom processing
- Building compatible automata with the same alphabet
- Debugging symbol mapping issues

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- nfa must be a valid NFA instance
*/
func NFAIndexerGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) SymbolIndexer[TObservation] {
	return nfa.indexer
}

/*
NFAOutcomesGet retrieves the raw memory array containing state outcomes.

The returned array is indexed by state ID and contains the outcome value
for each state (e.g., token type, accept/reject flag).

Use cases:
- Inspecting state outcomes directly
- Building compatible automata
- Debugging state assignments

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- nfa must be a valid NFA instance

Edge cases:
- Returns raw memory handle - use memstruct.ArrayItemGetAtUnsafe to access
*/
func NFAOutcomesGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return nfa.outcomes
}

func NFAAcceptingGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return nfa.accepting
}

/*
NFAStartingStatesGet retrieves the list of starting state IDs.

NFAs can have multiple starting states, unlike DFAs which have a single start.
All starting states are included in the epsilon closure at the beginning of
input processing.

Use cases:
- Understanding automaton initialization
- Building compatible automata
- Debugging start state configuration

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- nfa must be a valid NFA instance

Edge cases:
- Empty slice indicates no valid starting states
- Multiple starting states enable parallel exploration
*/
func NFAStartingStatesGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) []uint64 {
	return nfa.startingStates
}

/*
NFAAlphabetGet retrieves the alphabet (set of valid input symbols) for the NFA.

The alphabet defines all possible observations that can be processed by the
automaton. Symbol IDs correspond to indices in this slice.

Use cases:
- Building compatible automata with matching alphabets
- Understanding valid input symbols
- Debugging symbol mapping

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- nfa must be a valid NFA instance

Edge cases:
- Empty alphabet means no valid input symbols
- Alphabet order determines symbol ID assignment
*/
func NFAAlphabetGet[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome]) []SymbolDefinition[TObservation] {
	return nfa.alphabet
}

/*
NFARun processes an input sequence through the NFA and returns all possible outcomes.

The function simulates the NFA by maintaining a set of active states and computing
epsilon closures at each step. Returns all outcomes from accepting states reached
after processing the entire input.

Use cases:
- Pattern matching with regular expressions
- Lexical analysis
- Language recognition

Time complexity: O(n * s * t) where n is input length, s is states, t is transitions per state
Space complexity: O(s) for active state sets

Prerequisites:
- nfa must be a valid NFA instance
- input must contain only symbols from the alphabet

Edge cases:
- Returns empty slice if no accepting states are reached
- Returns error if invalid symbol encountered
- Empty input returns outcomes from starting states' epsilon closure
*/
func NFARun[TObservation any, TStateOutcome comparable](nfa *NFA[TObservation, TStateOutcome], input []TObservation) ([]TStateOutcome, error) {
	current := epsilonClosure(nfa, nfa.startingStates)

	for _, observation := range input {
		symbols := nfa.indexer(observation)
		if len(symbols) == 0 {
			return nil, fmt.Errorf("invalid symbol: %v", observation)
		}

		nextSet := make(map[uint64]struct{})

		for _, state := range current {
			for _, symbol := range symbols {
				key := [2]uint64{state, symbol.SymbolID}
				for _, ns := range nfa.transitions[key] {
					nextSet[ns] = struct{}{}
				}
			}
		}

		if len(nextSet) == 0 {
			return nil, nil
		}

		current = epsilonClosure(nfa, mapKeys(nextSet))
	}

	results := make([]TStateOutcome, 0)

	accCur := memstruct.ArrayCursorCreate[bool](nfa.accepting)
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](nfa.outcomes)

	for _, s := range current {
		if *accCur.PtrAt(s) {
			results = append(results, *outCur.PtrAt(s))
		}
	}

	return results, nil

}

/*
NFAEpsilonClosureCompute calculates the epsilon closure of a set of states.

The epsilon closure includes all states reachable from the input states via
zero or more epsilon transitions. This is a fundamental operation in NFA
processing and NFA-to-DFA conversion.

Use cases:
- Computing reachable states before processing input
- Subset construction for NFA-to-DFA conversion
- Analyzing state reachability

Time complexity: O(s * t) where s is states, t is epsilon transitions
Space complexity: O(s) for closure set and DFS stack

Prerequisites:
- nfa must be a valid NFA instance
- states must contain valid state indices

Edge cases:
- Empty input states returns empty closure
- States with no epsilon transitions return themselves
- Handles cycles in epsilon transitions correctly
*/
func NFAEpsilonClosureCompute[TSymbol any, TStateOutcome comparable](
	nfa *NFA[TSymbol, TStateOutcome],
	states []uint64,
) []uint64 {
	return epsilonClosure(nfa, states)
}

/*
NFATransitionsForStates computes the set of next states reachable from the given
states when processing the specified observation.

This function is used during NFA execution and NFA-to-DFA conversion to determine
which states can be reached from a set of current states with a given input symbol.

Use cases:
- NFA execution step computation
- Subset construction in NFA-to-DFA conversion
- Analyzing transition behavior

Time complexity: O(s * t) where s is input states, t is transitions per state
Space complexity: O(s) for result set

Prerequisites:
- nfa must be a valid NFA instance
- states must contain valid state indices
- observation must be in the alphabet

Edge cases:
- Returns empty slice if no transitions exist
- Returns error if observation is not in alphabet
- Deduplicates resulting states automatically
*/
func NFATransitionsForStates[TObservation any, TStateOutcome comparable](
	nfa *NFA[TObservation, TStateOutcome],
	states []uint64,
	observation TObservation,
) ([]uint64, error) {
	symbols := nfa.indexer(observation)
	if len(symbols) == 0 {
		return nil, fmt.Errorf("invalid symbol: %v", observation)
	}

	out := make(map[uint64]struct{})

	for _, s := range states {
		for _, symbol := range symbols {
			key := [2]uint64{s, symbol.SymbolID}
			next, exists := nfa.transitions[key]
			if !exists {
				continue
			}

			for _, ns := range next {
				out[ns] = struct{}{}
			}
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

		nextStates, exists := nfa.epsilonEdges[s]
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
