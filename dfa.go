package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memstruct"
	"strings"
)

/*
DFA represents a Deterministic Finite Automaton.

A DFA has exactly one transition per state-symbol pair, making execution
deterministic and efficient. DFAs are typically created from NFAs via subset
construction and then minimized to reduce state count.

Use cases:
- Efficient pattern matching (O(n) input processing)
- Lexical analysis in compilers
- String matching and validation

Time complexity:
- State transitions: O(1) via array lookup
- Input processing: O(n) where n is input length

Space complexity: O(s * a) where s is states, a is alphabet size

Prerequisites:
- Must have exactly one transition per state-symbol pair
- Starting state is always state 0
- All transitions must use valid symbol IDs

Edge cases:
- Dead states (no valid transitions) halt processing
- Invalid symbols return errors
- Empty input returns outcome from state 0
*/
type DFA[TObservation any, TStateOutcome comparable] struct {
	states      memcore.MarkRaw // Array[TStateOutcome]
	transitions memcore.MarkRaw // Array[uint64] -- stateAmount * alphabetSize

	indexer SymbolIndexer[TObservation]

	alphabet     []TObservation
	numStates    uint64
	alphabetSize uint64
}

/*
DFAStatesGet retrieves the raw memory array containing state outcomes.

The returned array is indexed by state ID and contains the outcome value
for each state (e.g., token type, accept/reject flag).

Use cases:
- Inspecting state outcomes directly
- Building compatible automata
- Debugging state assignments

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance

Edge cases:
- Returns raw memory handle - use memstruct.ArrayItemGetAtUnsafe to access
*/
func DFAStatesGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return dfa.states
}

/*
DFAIndexerGet retrieves the symbol indexer function for the DFA.

The indexer maps observations to symbols, enabling the DFA to process input
and determine valid transitions.

Use cases:
- Accessing the indexer for custom processing
- Building compatible automata with the same alphabet
- Debugging symbol mapping issues

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance
*/
func DFAIndexerGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) SymbolIndexer[TObservation] {
	return dfa.indexer
}

/*
DFAAlphabetGet retrieves the alphabet (set of valid input symbols) for the DFA.

The alphabet defines all possible observations that can be processed by the
automaton. Symbol IDs correspond to indices in this slice.

Use cases:
- Building compatible automata with matching alphabets
- Understanding valid input symbols
- Debugging symbol mapping

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance

Edge cases:
- Empty alphabet means no valid input symbols
- Alphabet order determines symbol ID assignment
*/
func DFAAlphabetGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) []TObservation {
	return dfa.alphabet
}

/*
DFACreate constructs a new Deterministic Finite Automaton.

The function allocates memory for state outcomes and builds a transition table
as a flat array indexed by (state * alphabetSize + symbolID). Each state-symbol
pair must have exactly one transition.

Use cases:
- Creating DFAs from NFAs via subset construction
- Building minimized DFAs after minimization
- Constructing DFAs programmatically

Time complexity: O(t) where t is the number of transitions
Space complexity: O(s * a) where s is states, a is alphabet size

Prerequisites:
- alphabet must contain all symbols used in transitions
- transitions must have exactly one transition per state-symbol pair
- transitions must use valid symbol IDs (0 <= SymbolID < len(alphabet))
- indexer must correctly map observations to symbols in the alphabet

Edge cases:
- Panics if multiple transitions exist for same state-symbol pair
- Panics if symbol ID exceeds alphabet size
- Dead states (no transitions) are represented as transitions to state 0
*/
func DFACreate[TObservation any, TStateOutcome comparable](
	allocFn memarch.AllocationFn,
	alphabet []TObservation,
	transitions []Transition[TObservation],
	states []TStateOutcome,
	indexer SymbolIndexer[TObservation],
) *DFA[TObservation, TStateOutcome] {

	alphabetSize := uint64(len(alphabet))
	numStates := uint64(len(states))
	rowStride := alphabetSize

	// 1. State Outcomes
	stateTable, _ := memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)
	for stateNum, stateOutcome := range states {
		memstruct.ArraySetAtUnsafe(stateTable, uint64(stateNum), stateOutcome)
	}

	// 2. Transitions
	transitionArray, _ := memarch.MemArchArrayCreate[uint64](allocFn, numStates*alphabetSize)

	for _, transition := range transitions {
		if transition.Symbol.SymbolID >= alphabetSize && transition.Symbol.SymbolID != AutarchEpsilonID {
			panic(fmt.Errorf("symbol ID must be less than the alphabetSize, got=%d,max=%d", transition.Symbol.SymbolID, alphabetSize))
		}

		idx := getTransitionIDX(rowStride, transition.CurrentState, transition.Symbol.SymbolID)

		if memstruct.ArrayItemGetAtUnsafe[uint64](transitionArray, idx) != 0 {
			panic(fmt.Errorf("cannot have multiple transitions for input symbol %v and state %d", transition.Symbol.SymbolDescription, transition.CurrentState))
		}

		memstruct.ArraySetAtUnsafe(transitionArray, idx, transition.NextState)
	}

	return &DFA[TObservation, TStateOutcome]{
		states:       stateTable,
		transitions:  transitionArray,
		indexer:      indexer,
		numStates:    numStates,
		alphabetSize: alphabetSize,
		alphabet:     alphabet,
	}
}

/*
DFARun processes an input sequence through the DFA and returns the final state outcome.

The function processes input symbol-by-symbol, following deterministic transitions
from the starting state (0) to the final state, then returns the outcome associated
with that state.

Use cases:
- Pattern matching with regular expressions
- Lexical analysis
- Language recognition
- String validation

Time complexity: O(n) where n is input length
Space complexity: O(1) - only state tracking

Prerequisites:
- dfa must be a valid DFA instance
- input must contain only symbols from the alphabet

Edge cases:
- Returns zero value if DFA reaches dead state
- Returns error if invalid symbol encountered
- Empty input returns outcome from state 0
*/
func DFARun[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome], input []TObservation) (TStateOutcome, error) {
	state := uint64(0)
	arrayCursor := memstruct.ArrayCursorCreate[uint64](dfa.transitions) // cursor to avoid dereffing the array header each time

	for _, observation := range input {
		symbol, valid := dfa.indexer(observation)
		if !valid {
			var zero TStateOutcome
			return zero, fmt.Errorf("invalid symbol encountered: %s", symbol.SymbolDescription)
		}

		transitionIDX := getTransitionIDX(dfa.alphabetSize, state, symbol.SymbolID)
		newState := arrayCursor.PtrAt(transitionIDX)
		state = *newState
	}

	return memstruct.ArrayItemGetAtUnsafe[TStateOutcome](dfa.states, state), nil
}

/*
DFADebugPrint generates a human-readable string representation of the DFA.

The output includes the alphabet, state outcomes, and a transition table
in a formatted, deterministic layout for debugging purposes.

Use cases:
- Debugging automaton construction
- Verifying transition correctness
- Understanding automaton structure
- Visualizing state machine behavior

Time complexity: O(s * a) where s is states, a is alphabet size
Space complexity: O(s * a) for string builder

Prerequisites:
- dfa must be a valid DFA instance

Edge cases:
- Handles empty alphabets and state sets gracefully
- Transition table shows all state-symbol combinations
*/
func DFADebugPrint[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
) string {

	var sb strings.Builder

	// ============================================================
	// 1. Alphabet
	// ============================================================
	sb.WriteString("DFA Debug Print\n")
	sb.WriteString("=============================\n\n")

	sb.WriteString("Alphabet:\n")
	for id, obs := range dfa.alphabet {
		sym, _ := dfa.indexer(obs)
		// guaranteed deterministic because indexer returns symbolID + description
		sb.WriteString(fmt.Sprintf("  %2d → %s\n", id, sym.SymbolDescription))
	}
	sb.WriteString("\n")

	// ============================================================
	// 2. State Outcomes
	// ============================================================
	sb.WriteString("States:\n")

	stateCur := memstruct.ArrayCursorCreate[TStateOutcome](dfa.states)

	for s := uint64(0); s < dfa.numStates; s++ {
		outcome := *stateCur.PtrAt(s)
		sb.WriteString(fmt.Sprintf("  state %2d → %v\n", s, outcome))
	}
	sb.WriteString("\n")

	// ============================================================
	// 3. Transition Table
	// ============================================================
	sb.WriteString("Transitions (rows = states, columns = symbols):\n")

	// Header
	sb.WriteString("        |")
	for id := uint64(0); id < dfa.alphabetSize; id++ {
		sb.WriteString(fmt.Sprintf(" %3d ", id))
	}
	sb.WriteString("\n")

	// divider
	sb.WriteString("--------+")
	for id := uint64(0); id < dfa.alphabetSize; id++ {
		sb.WriteString("-----")
	}
	sb.WriteString("\n")

	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)

	// Each state = one row, deterministic iteration
	for s := uint64(0); s < dfa.numStates; s++ {
		sb.WriteString(fmt.Sprintf("  %3d  |", s))

		row := s * dfa.alphabetSize
		for symbol := uint64(0); symbol < dfa.alphabetSize; symbol++ {
			target := *transCur.PtrAt(row + symbol)
			sb.WriteString(fmt.Sprintf(" %3d ", target))
		}

		sb.WriteString("\n")
	}

	return sb.String()
}

/*
DFACursorGet creates an array cursor for efficient transition table access.

The cursor provides optimized access to the transition table without repeated
array header dereferencing, improving performance in hot loops.

Use cases:
- Optimizing DFA execution in performance-critical code
- Iterating over transitions efficiently
- Reducing memory access overhead

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance

Edge cases:
- Cursor remains valid as long as DFA is not modified
- Multiple cursors can be created for parallel processing
*/
func DFACursorGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) memstruct.ArrayCursor[uint64] {
	return memstruct.ArrayCursorCreate[uint64](dfa.transitions)
}

/*
DFATransition computes the next state from a current state and observation.

This function performs a single transition step using a pre-allocated cursor
for optimal performance. It is designed for use in tight loops where multiple
transitions are processed sequentially.

Use cases:
- Manual DFA execution with cursor optimization
- Lexical analysis with maximal munch strategy
- Custom state machine processing

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance
- currentState must be a valid state index
- observation must be in the alphabet
- arrayCursor must be from DFACursorGet

Edge cases:
- Returns transition target even if it's a dead state
- Assumes valid observation (no error checking for performance)
*/
func DFATransition[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
	observation TObservation,
	currentState uint64,
	arrayCursor memstruct.ArrayCursor[uint64],
) uint64 {
	symbol, _ := dfa.indexer(observation)
	transitionIDX := getTransitionIDX(dfa.alphabetSize, currentState, symbol.SymbolID)
	newState := arrayCursor.PtrAt(transitionIDX)
	return *newState
}

/*
DFAPredecessorSets computes all predecessor states for each state-symbol combination.

The result is a three-dimensional structure: [symbolID][targetState][]predecessorStates.
This is used in DFA minimization algorithms to identify states that can be merged.

Use cases:
- DFA minimization via Hopcroft's algorithm
- Analyzing state reachability
- Finding equivalent states

Time complexity: O(s * a) where s is states, a is alphabet size
Space complexity: O(s * a) for predecessor sets

Prerequisites:
- dfa must be a valid DFA instance

Edge cases:
- Empty predecessor sets indicate unreachable states
- Multiple predecessors per state-symbol are possible
- Structure is indexed by symbol ID, then target state
*/
func DFAPredecessorSets[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
) [][][]uint64 {

	incoming := make([][][]uint64, dfa.alphabetSize)
	for c := range incoming {
		incoming[c] = make([][]uint64, dfa.numStates)
	}

	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)

	for state := uint64(0); state < dfa.numStates; state++ {
		row := state * dfa.alphabetSize

		for symbol := uint64(0); symbol < dfa.alphabetSize; symbol++ {
			target := *transCur.PtrAt(row + symbol)
			incoming[symbol][target] = append(incoming[symbol][target], state)
		}
	}

	return incoming
}

/*
DFAStep performs a single transition step in the DFA.

This function advances the automaton from the current state to the next state
based on the provided observation. It includes validation and error checking,
making it suitable for general-purpose DFA execution.

Use cases:
- Step-by-step DFA execution
- Lexical analysis with error handling
- Interactive state machine processing

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance
- currentState must be a valid state index
- observation should be in the alphabet (checked)
- arrayCursor must be from DFACursorGet

Edge cases:
- Returns error if observation is not in alphabet
- Returns next state even if it's a dead state
- Handles transitions to state 0 (dead state indicator)
*/
func DFAStep[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
	currentState uint64,
	observation TObservation,
	arrayCursor memstruct.ArrayCursor[uint64],
) (uint64, error) {
	symbol, valid := dfa.indexer(observation)
	if !valid {
		var zero uint64
		return zero, fmt.Errorf("invalid symbol encountered: %v", observation)
	}

	transitionIDX := getTransitionIDX(dfa.alphabetSize, currentState, symbol.SymbolID)
	newState := arrayCursor.PtrAt(transitionIDX)
	return *newState, nil
}

/*
DFAStateOutcome retrieves the outcome value associated with a state.

The outcome typically represents a token type, accept/reject flag, or other
semantic value associated with reaching that state.

Use cases:
- Determining if a state is accepting
- Retrieving token types in lexers
- Checking state semantics

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance
- state must be a valid state index

Edge cases:
- Returns zero value for invalid state indices (undefined behavior)
- Outcome values are set during DFA construction
*/
func DFAStateOutcome[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
	state uint64,
) TStateOutcome {
	return memstruct.ArrayItemGetAtUnsafe[TStateOutcome](dfa.states, state)
}
