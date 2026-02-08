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
	accepting   memcore.MarkRaw // Array[bool]
	outcomes    memcore.MarkRaw // Array[TStateOutcome]
	transitions memcore.MarkRaw // Array[uint64]

	indexer SymbolIndexer[TObservation]

	alphabet     []SymbolDefinition[TObservation]
	numStates    uint64
	alphabetSize uint64
}

/*
DFAOutcomesGet retrieves the raw memory array containing state outcomes.

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
func DFAOutcomesGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return dfa.outcomes
}

func DFAAcceptingGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return dfa.accepting
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
func DFAAlphabetGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) []SymbolDefinition[TObservation] {
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
- alphabet must not contain duplicate symbol names with different IDs

Edge cases:
- Panics if multiple transitions exist for same state-symbol pair
- Panics if symbol ID exceeds alphabet size
- Panics if duplicate alphabet entries with same name but different IDs are found
- Dead states (no transitions) are represented as transitions to state 0
*/
func DFACreate[TObservation any, TStateOutcome comparable](
	allocFn memarch.AllocationFn,
	alphabet []SymbolDefinition[TObservation],
	transitions []Transition[TObservation],
	accepting []bool,
	outcomes []TStateOutcome,
	indexer SymbolIndexer[TObservation],
) *DFA[TObservation, TStateOutcome] {
	if len(accepting) != len(outcomes) {
		panic("accepting and outcomes must be same length")
	}

	alphabetSize := uint64(len(alphabet))
	numStates := uint64(len(outcomes))
	rowStride := alphabetSize

	validateAlphabet(alphabet, "dfa")

	acceptTable, _ := memarch.MemArchArrayCreate[bool](allocFn, numStates)
	outcomeTable, _ := memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)

	for i := range accepting {
		memstruct.ArraySetAtUnsafe(acceptTable, uint64(i), accepting[i])
		if accepting[i] {
			memstruct.ArraySetAtUnsafe(outcomeTable, uint64(i), outcomes[i])
		}
	}

	transitionArray, _ := memarch.MemArchArrayCreate[uint64](allocFn, numStates*alphabetSize)

	for _, transition := range transitions {
		if transition.Symbol.SymbolID >= alphabetSize {
			panic(fmt.Errorf("symbol ID must be less than the alphabetSize, got=%d,max=%d", transition.Symbol.SymbolID, alphabetSize))
		}

		idx := getTransitionIDX(rowStride, transition.CurrentState, transition.Symbol.SymbolID)

		if memstruct.ArrayItemGetAtUnsafe[uint64](transitionArray, idx) != 0 {
			panic(fmt.Errorf("cannot have multiple transitions for input symbol %v and state %d", transition.Symbol.SymbolDescription, transition.CurrentState))
		}

		memstruct.ArraySetAtUnsafe(transitionArray, idx, transition.NextState)
	}

	return &DFA[TObservation, TStateOutcome]{
		outcomes:     outcomeTable,
		accepting:    acceptTable,
		transitions:  transitionArray,
		indexer:      indexer,
		numStates:    numStates,
		alphabetSize: alphabetSize,
		alphabet:     alphabet,
	}
}

func validateAlphabet[TObservation any](alphabet []SymbolDefinition[TObservation], component string) {
	nameToID := make(map[string]uint64)
	for i, symDef := range alphabet {
		symID := uint64(i)

		// Validate that SymbolDefinition.ID matches its index (if ID is set)
		if symDef.ID != symID {
			panic(fmt.Errorf(
				"%s: alphabet entry at index %d has mismatched ID: SymbolDefinition.ID=%d does not match index %d",
				component, i, symDef.ID, symID,
			))
		}

		// Check for duplicate names with different IDs
		if existingID, exists := nameToID[symDef.Name]; exists {
			if existingID != symID {
				panic(fmt.Errorf(
					"%s: duplicate alphabet entry: symbol name %q appears with different IDs: ID %d and ID %d",
					component, symDef.Name, existingID, symID,
				))
			}
		} else {
			nameToID[symDef.Name] = symID
		}
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
		symbols := dfa.indexer(observation)
		if len(symbols) == 0 {
			var zero TStateOutcome
			return zero, fmt.Errorf("invalid symbol encountered: %v", observation)
		}

		// For DFA, use the first symbol (deterministic)
		symbol := symbols[0]
		transitionIDX := getTransitionIDX(dfa.alphabetSize, state, symbol.SymbolID)
		newState := arrayCursor.PtrAt(transitionIDX)
		state = *newState
	}

	out, ok := DFAStateOutcome(dfa, state)
	if !ok {
		var zero TStateOutcome
		return zero, nil
	}
	return out, nil

}

/*
DFADebugFormatter provides optional formatting functions to make DFA debug output
more readable by converting numeric values to human-readable representations.

Each formatting function is optional (nil means use default formatting). This allows
partial formatting (e.g., only format symbol names, not IDs).

Use cases:
- Converting numeric symbol names to character representations (e.g., "105" → "'i'")
- Formatting state outcomes for better readability
- Customizing debug output for specific observation types
- Improving debugging experience for complex automata

Time complexity: O(1) per formatting call
Space complexity: O(1) per formatting call

Prerequisites:
- All formatting functions should be fast (no allocations if possible)
- Formatting functions should handle edge cases gracefully

Edge cases:
- Nil formatter functions fall back to default formatting
- Formatting functions may return strings of varying lengths
*/
type DFADebugFormatter[TObservation any, TStateOutcome comparable] struct {
	FormatSymbolName   func(symbolID uint64, name string, observation *TObservation) string
	FormatStateOutcome func(outcome TStateOutcome) string
	FormatSymbolID     func(symbolID uint64) string
	FormatStateID      func(stateID uint64) string
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
- If formatter is nil, uses default formatting (backward compatible)
*/
func DFADebugPrint[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
	formatter *DFADebugFormatter[TObservation, TStateOutcome],
) string {

	var sb strings.Builder

	// ============================================================
	// 1. Alphabet
	// ============================================================
	sb.WriteString("DFA Debug Print\n")
	sb.WriteString("=============================\n\n")

	sb.WriteString("Alphabet:\n")
	for id, symDef := range dfa.alphabet {
		var symbolName string
		if formatter != nil && formatter.FormatSymbolName != nil && symDef.Observation != nil {
			symbolName = formatter.FormatSymbolName(uint64(id), symDef.Name, symDef.Observation)
		} else {
			symbolName = symDef.Name
		}
		sb.WriteString(fmt.Sprintf("  %2d → %s\n", id, symbolName))
	}
	sb.WriteString("\n")

	// ============================================================
	// 2. States (accepting + outcomes)
	// ============================================================
	sb.WriteString("States:\n")

	acceptCur := memstruct.ArrayCursorCreate[bool](dfa.accepting)
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](dfa.outcomes)

	for s := uint64(0); s < dfa.numStates; s++ {

		isAccepting := *acceptCur.PtrAt(s)

		var stateLabel string

		if isAccepting {
			outcome := *outCur.PtrAt(s)
			if formatter != nil && formatter.FormatStateOutcome != nil {
				stateLabel = formatter.FormatStateOutcome(outcome)
			} else {
				stateLabel = fmt.Sprintf("%v", outcome)
			}
		} else {
			stateLabel = "—" // non-accepting state
		}

		var stateIDStr string
		if formatter != nil && formatter.FormatStateID != nil {
			stateIDStr = formatter.FormatStateID(s)
		} else {
			stateIDStr = fmt.Sprintf("%2d", s)
		}

		sb.WriteString(fmt.Sprintf("  state %s → %s\n", stateIDStr, stateLabel))
	}

	sb.WriteString("\n")

	// ============================================================
	// 3. Transition Table
	// ============================================================
	sb.WriteString("Transitions (rows = states, columns = symbols):\n")

	sb.WriteString("        |")
	for id := uint64(0); id < dfa.alphabetSize; id++ {
		var symbolIDStr string
		if formatter != nil && formatter.FormatSymbolID != nil {
			symbolIDStr = formatter.FormatSymbolID(id)
		} else {
			symbolIDStr = fmt.Sprintf("%3d", id)
		}
		sb.WriteString(fmt.Sprintf(" %3s ", symbolIDStr))
	}
	sb.WriteString("\n")

	sb.WriteString("--------+")
	for id := uint64(0); id < dfa.alphabetSize; id++ {
		sb.WriteString("-----")
	}
	sb.WriteString("\n")

	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)

	for s := uint64(0); s < dfa.numStates; s++ {

		var stateIDStr string
		if formatter != nil && formatter.FormatStateID != nil {
			stateIDStr = formatter.FormatStateID(s)
		} else {
			stateIDStr = fmt.Sprintf("%3d", s)
		}

		sb.WriteString(fmt.Sprintf("  %3s  |", stateIDStr))

		row := s * dfa.alphabetSize
		for symbol := uint64(0); symbol < dfa.alphabetSize; symbol++ {
			target := *transCur.PtrAt(row + symbol)

			var targetStr string
			if formatter != nil && formatter.FormatStateID != nil {
				targetStr = formatter.FormatStateID(target)
			} else {
				targetStr = fmt.Sprintf("%3d", target)
			}

			sb.WriteString(fmt.Sprintf(" %3s ", targetStr))
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
	symbols := dfa.indexer(observation)
	if len(symbols) == 0 {
		// Invalid symbol, return dead state (0)
		return 0
	}
	// For DFA, use the first symbol (deterministic)
	symbol := symbols[0]
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
	symbols := dfa.indexer(observation)
	if len(symbols) == 0 {
		var zero uint64
		return zero, fmt.Errorf("invalid symbol encountered: %v", observation)
	}

	// For DFA, use the first symbol (deterministic)
	symbol := symbols[0]
	transitionIDX := getTransitionIDX(dfa.alphabetSize, currentState, symbol.SymbolID)
	newState := arrayCursor.PtrAt(transitionIDX)
	return *newState, nil
}

/*
DFAStateOutcome retrieves the semantic outcome associated with an accepting DFA state.

Only accepting states carry outcomes. Non-accepting states have no semantic value
and will return ok=false.

The outcome typically represents a token type, rule result, or other semantic
information produced when the automaton reaches an accepting configuration.

Use cases:
- Retrieving token types in lexers
- Obtaining match results from automata
- Applying semantic actions on acceptance

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance
- state must be a valid state index

Edge cases:
- Returns ok=false for non-accepting states
- Behavior is undefined for invalid state indices
- Outcomes are assigned explicitly during DFA construction

Design notes:
- Acceptance is tracked separately from outcomes (no sentinel values)
- Zero values of TStateOutcome have no semantic meaning
- This matches formal DFA theory: acceptance is a state property, not a value hack
*/
func DFAStateOutcome[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
	state uint64,
) (outcome TStateOutcome, ok bool) {

	if !memstruct.ArrayItemGetAtUnsafe[bool](dfa.accepting, state) {
		return outcome, false
	}

	return memstruct.ArrayItemGetAtUnsafe[TStateOutcome](dfa.outcomes, state), true
}
