package autarch

import (
	"fmt"
	"foundation"
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
type DFA[TObservation, TStateOutcome any] struct {
	outcomes    memcore.MarkRaw // Array[TStateOutcome]
	transitions memcore.MarkRaw // Array[uint64]

	outcomesCursor    memstruct.ArrayCursor[TStateOutcome]
	transitionsCursor memstruct.ArrayCursor[uint64]

	deterministicResolver DeterministicSymbolResolver[TObservation]

	alphabet     []SymbolDefinition[TObservation]
	numStates    uint64
	alphabetSize uint64

	deadStates []bool
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
func DFAOutcomesGet[TObservation, TStateOutcome any](dfa *DFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return dfa.outcomes
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
func DFAAlphabetGet[TObservation, TStateOutcome any](dfa *DFA[TObservation, TStateOutcome]) []SymbolDefinition[TObservation] {
	return dfa.alphabet
}

/*
DFACreate constructs a Deterministic Finite Automaton from an explicit
and fully-specified transition set.

This constructor is intentionally *semantic-neutral*:

  - It does NOT invent dead states
  - It does NOT fill missing transitions
  - It does NOT repair incomplete automata

Instead, it enforces the formal DFA invariant:

	δ : Q × Σ → Q  is a total function

Meaning:

	Every (state, symbol) pair must appear exactly once in the transition list.

All DFA semantics — including explicit dead/sink states — must already be
present in the provided transition set (typically produced by NFAToDFA or
post-processing passes).

───────────────────────────────────────────────────────────────
Formal invariants enforced:

	✔ exactly one transition per (state, symbol)
	✔ no missing transitions
	✔ no duplicate transitions
	✔ all state indices valid
	✔ all symbol IDs valid
	✔ alphabet consistency

───────────────────────────────────────────────────────────────
Use cases:
  - Constructing DFAs from subset construction
  - Creating minimized DFAs
  - Programmatic DFA generation
  - Verified automata for lexers/parsers

Time complexity:  O(s * a)
Space complexity: O(s * a)

Where:

	s = number of states
	a = alphabet size

───────────────────────────────────────────────────────────────
Panics on:

  - missing transitions
  - duplicate transitions
  - out-of-range states
  - out-of-range symbols
  - inconsistent alphabet definitions

───────────────────────────────────────────────────────────────
Design principle:

	Construction builds memory layout.
	Validation enforces automaton correctness.

Semantic mutation belongs in automaton-building passes — not here.
*/
func DFACreate[TObservation, TStateOutcome any](
	allocFn memarch.AllocationFn,
	alphabet []SymbolDefinition[TObservation],
	transitions []Transition[TObservation],
	outcomes []TStateOutcome,
	resolver DeterministicSymbolResolver[TObservation],
) *DFA[TObservation, TStateOutcome] {
	validateAlphabet(alphabet, "dfa")

	alphabetSize := uint64(len(alphabet))
	numStates := uint64(len(outcomes))

	if numStates == 0 {
		panic("DFACreate: DFA must contain at least one state")
	}

	expectedTransitions := numStates * alphabetSize

	if uint64(len(transitions)) != expectedTransitions {
		panic(fmt.Errorf(
			"DFACreate: DFA must define exactly %d transitions (got %d)",
			expectedTransitions,
			len(transitions),
		))
	}

	// ───────────────────────────────────────────────────────────────
	// Allocate state metadata (outcomes only; acceptance is client-defined via outcome)
	// ───────────────────────────────────────────────────────────────
	outcomeTable, _ := memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)

	for i := uint64(0); i < numStates; i++ {
		memstruct.ArraySetAtUnsafe(outcomeTable, i, outcomes[i])
	}

	// ───────────────────────────────────────────────────────────────
	// Allocate transition table
	// Layout: [state * alphabetSize + symbolID] → nextState
	// ───────────────────────────────────────────────────────────────
	transitionArray, _ := memarch.MemArchArrayCreate[uint64](
		allocFn,
		numStates*alphabetSize,
	)

	seen := make([]bool, numStates*alphabetSize)

	// ───────────────────────────────────────────────────────────────
	// Populate transitions with strict validation
	// ───────────────────────────────────────────────────────────────
	for _, t := range transitions {
		if t.Symbol.SymbolID >= alphabetSize {
			panic(fmt.Errorf(
				"DFACreate: symbol ID out of range: %d (max %d)",
				t.Symbol.SymbolID,
				alphabetSize-1,
			))
		}

		if t.CurrentState >= numStates || t.NextState >= numStates {
			panic(fmt.Errorf(
				"DFACreate: transition refers to invalid state: %d → %d (max %d)",
				t.CurrentState,
				t.NextState,
				numStates-1,
			))
		}

		idx := getTransitionIDX(alphabetSize, t.CurrentState, t.Symbol.SymbolID)

		if seen[idx] {
			panic(fmt.Errorf(
				"DFACreate: duplicate transition for state %d on symbol %d",
				t.CurrentState,
				t.Symbol.SymbolID,
			))
		}

		seen[idx] = true
		memstruct.ArraySetAtUnsafe(transitionArray, idx, t.NextState)
	}

	// ───────────────────────────────────────────────────────────────
	// Enforce total DFA invariant (no missing transitions)
	// ───────────────────────────────────────────────────────────────
	for i, ok := range seen {
		if !ok {
			state := uint64(i) / alphabetSize
			symbol := uint64(i) % alphabetSize
			panic(fmt.Errorf(
				"DFACreate: missing transition for state %d on symbol %d",
				state,
				symbol,
			))
		}
	}

	deadStates := make([]bool, numStates)

	transCur := memstruct.ArrayCursorCreate[uint64](transitionArray)

	for s := uint64(0); s < numStates; s++ {
		row := s * alphabetSize
		isDead := true

		for a := uint64(0); a < alphabetSize; a++ {
			if *transCur.PtrAt(row + a) != s {
				isDead = false
				break
			}
		}
		deadStates[s] = isDead
	}

	// ───────────────────────────────────────────────────────────────
	// Construct DFA object
	// ───────────────────────────────────────────────────────────────
	return &DFA[TObservation, TStateOutcome]{
		outcomes:              outcomeTable,
		transitions:           transitionArray,
		outcomesCursor:        memstruct.ArrayCursorCreate[TStateOutcome](outcomeTable),
		transitionsCursor:     memstruct.ArrayCursorCreate[uint64](transitionArray),
		deterministicResolver: resolver,
		numStates:             numStates,
		alphabetSize:          alphabetSize,
		alphabet:              alphabet,
		deadStates:            deadStates,
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
- Returns zero value if invalid symbol encountered
- Empty input returns outcome from state 0
*/
func DFARun[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	input []TObservation,
) (outcome TStateOutcome, err error) {
	state := StartStateID

	for _, observation := range input {
		symbolID, ok := dfa.deterministicResolver(observation)
		if !ok {
			var zero TStateOutcome
			return zero, &AutomatonError{
				Kind:      AutomatonErrorInvalidSymbolFinite,
				Automaton: "DFA",
				Message:   fmt.Sprintf("invalid symbol: %v", observation),
			}
		}
		transitionIDX := getTransitionIDX(dfa.alphabetSize, state, symbolID)
		newState := dfa.transitionsCursor.PtrAt(transitionIDX)
		state = *newState
	}

	outcome = *dfa.outcomesCursor.PtrAt(state)
	return outcome, nil
}

/*
DFAAvailableSymbols returns all observation symbols that can legally transition
from the given DFA state.

This exposes the DFA frontier at a specific state — i.e. the set of input symbols
for which a transition exists (non-dead transition). It is primarily used for
diagnostics, error reporting, recovery, and interactive tooling.

The function scans the transition row for the given state and collects all symbols
whose transition target is non-zero.

Use cases:
- Producing precise lexer error messages ("expected one of ...")
- Implementing error recovery strategies
- Interactive parsing and tooling (IDEs, debuggers)
- Visualizing DFA frontiers

Time complexity: O(a) where a is alphabet size
Space complexity: O(k) where k is number of valid outgoing transitions (k ≤ a)

Prerequisites:
- dfa must be a valid DFA instance
- state must be a valid state index

Edge cases:
- Returns empty slice if state is a dead-end (no valid transitions)
- Includes all symbols that lead to any state
- Does not allocate excessively (capacity bounded by alphabet size)

Design notes:
- Uses direct transition table scanning for maximal performance
- Matches DFA formal definition: outgoing labeled edges from a state
*/
func DFAAvailableSymbols[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	state uint64,
) []SymbolDefinition[TObservation] {
	if state >= dfa.numStates {
		panic(fmt.Errorf(
			"invalid DFA state: %d (max=%d)",
			state,
			dfa.numStates,
		))
	}

	rowStart := state * dfa.alphabetSize
	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)

	// Upper bound = alphabet size (never reallocs beyond that)
	out := make([]SymbolDefinition[TObservation], 0, dfa.alphabetSize)

	for symbolID := uint64(0); symbolID < dfa.alphabetSize; symbolID++ {
		target := *transCur.PtrAt(rowStart + symbolID)

		if DFAIsDeadState(dfa, target) {
			continue
		}

		sym := dfa.alphabet[symbolID]
		out = append(out, sym)
	}

	return out
}

/*
DFATransitionsFrom returns all outgoing DFA transitions from the given state.

Each transition is represented as a (symbol, targetState) pair corresponding to:

	δ(state, symbol) → targetState

This exposes the full outgoing edge set of the DFA graph at a specific state.

Use cases:
- DFA visualization
- Minimization algorithms
- Graph traversal and reachability analysis
- Debugging automata structure
- Building higher-level transition views

Time complexity: O(a) where a is alphabet size
Space complexity: O(a)

Prerequisites:
- dfa must be a valid DFA instance
- state must be a valid state index

Edge cases:
- Includes transitions to dead states (true DFA semantics)
- Always returns exactly alphabetSize transitions (DFA is total)
- Order matches alphabet symbol IDs

Design notes:
- Directly scans transition table row (no maps, no allocations beyond slice)
- Preserves DFA formal invariant: one transition per symbol
*/
func DFATransitionsFrom[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	state uint64,
) []struct {
	Symbol SymbolDefinition[TObservation]
	Target uint64
} {

	if state >= dfa.numStates {
		panic(fmt.Errorf(
			"DFATransitionsFrom: invalid state %d (max=%d)",
			state,
			dfa.numStates-1,
		))
	}

	rowStart := state * dfa.alphabetSize
	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)

	out := make([]struct {
		Symbol SymbolDefinition[TObservation]
		Target uint64
	}, 0, dfa.alphabetSize)

	for symbolID := uint64(0); symbolID < dfa.alphabetSize; symbolID++ {
		target := *transCur.PtrAt(rowStart + symbolID)

		out = append(out, struct {
			Symbol SymbolDefinition[TObservation]
			Target uint64
		}{
			Symbol: dfa.alphabet[symbolID],
			Target: target,
		})
	}

	return out
}

/* DFAIsDeadState checks whether the state is dead. */
func DFAIsDeadState[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	state uint64,
) bool {
	return dfa.deadStates[state]
}

/*
DFADebugFormatter provides optional formatting functions to make DFA debug output
more readable by converting numeric values to human-readable representations.

Each formatting function is optional (nil means use default formatting). This allows
partial formatting (e.g., only format symbol names, not IDs).

FormatStateIndicator supplies the single character shown between brackets for each
state (e.g. [A] for accepting, [D] for dead). If nil, default is " " for normal
states and "D" for dead states. Return a single character (e.g. "A") so clients can
mark accepting or other state kinds; only the first rune is used for alignment.

Use cases:
- Converting numeric symbol names to character representations (e.g., "105" → "'i'")
- Formatting state outcomes for better readability
- Customizing debug output for specific observation types
- Marking accepting states with "A" or other single-character indicators
- Improving debugging experience for complex automata

Time complexity: O(1) per formatting call
Space complexity: O(1) per formatting call

Prerequisites:
- All formatting functions should be fast (no allocations if possible)
- Formatting functions should handle edge cases gracefully
- FormatStateIndicator should return a single character (first rune is used)

Edge cases:
- Nil formatter functions fall back to default formatting
- Formatting functions may return strings of varying lengths
*/
type DFADebugFormatter[TObservation, TStateOutcome any] struct {
	FormatSymbolName   func(id uint64, def SymbolDefinition[TObservation]) string
	FormatStateOutcome func(outcome TStateOutcome) string
	FormatSymbolID     func(symbolID uint64) string
	FormatStateID      func(stateID uint64) string

	// FormatStateIndicator returns the single-character indicator shown in [ ] for this state.
	// E.g. "A" for accepting, " " for normal, "D" for dead. If nil, " " is used except "D" when isDead.
	// Only the first rune of the return value is used for alignment.
	FormatStateIndicator func(state uint64, outcome TStateOutcome, isDead bool) string
}

func dfaDebugStateIndicatorRune[TObservation, TStateOutcome any](
	formatter *DFADebugFormatter[TObservation, TStateOutcome],
	state uint64,
	outcome TStateOutcome,
	isDead bool,
) string {
	if formatter != nil && formatter.FormatStateIndicator != nil {
		s := formatter.FormatStateIndicator(state, outcome, isDead)
		for _, r := range s {
			return string(r)
		}
		return " "
	}
	if isDead {
		return "D"
	}
	return " "
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
func DFADebugPrint[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	formatter *DFADebugFormatter[TObservation, TStateOutcome],
) string {
	var sb strings.Builder

	// ============================================================
	// 1. Pre-calculation for Layout
	// ============================================================
	// Calculate max widths for columns to ensure the table is readable
	maxStateWidth := 5 // Minimum "State" header
	stateStrings := make([]string, dfa.numStates)
	for s := uint64(0); s < dfa.numStates; s++ {
		if formatter != nil && formatter.FormatStateID != nil {
			stateStrings[s] = formatter.FormatStateID(s)
		} else {
			stateStrings[s] = fmt.Sprintf("%d", s)
		}
		if len(stateStrings[s]) > maxStateWidth {
			maxStateWidth = len(stateStrings[s])
		}
	}

	symbolStrings := make([]string, dfa.alphabetSize)
	maxSymbolWidth := 3
	for id := uint64(0); id < dfa.alphabetSize; id++ {
		if formatter != nil && formatter.FormatSymbolID != nil {
			symbolStrings[id] = formatter.FormatSymbolID(id)
		} else {
			symbolStrings[id] = fmt.Sprintf("%d", id)
		}
		if len(symbolStrings[id]) > maxSymbolWidth {
			maxSymbolWidth = len(symbolStrings[id])
		}
	}

	// ============================================================
	// 2. Header & Alphabet
	// ============================================================
	sb.WriteString("DFA Debug Print\n")
	sb.WriteString("=============================\n\n")

	sb.WriteString("Alphabet:\n")
	for id, symDef := range dfa.alphabet {
		var symbolName string
		if formatter != nil && formatter.FormatSymbolName != nil {
			symbolName = formatter.FormatSymbolName(uint64(id), symDef)
		} else {
			symbolName = symDef.Name
		}
		sb.WriteString(fmt.Sprintf("  %s → %s\n", symbolStrings[id], symbolName))
	}
	sb.WriteString("\n")

	// ============================================================
	// 3. States (Outcome)
	// ============================================================
	sb.WriteString("States:\n")
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](dfa.outcomes)

	for s := uint64(0); s < dfa.numStates; s++ {
		isDead := dfa.deadStates[s]
		outcome := *outCur.PtrAt(s)

		var outcomeStr string
		if formatter != nil && formatter.FormatStateOutcome != nil {
			outcomeStr = formatter.FormatStateOutcome(outcome)
		} else {
			outcomeStr = fmt.Sprintf("%v", outcome)
		}

		status := dfaDebugStateIndicatorRune(formatter, s, outcome, isDead)

		sb.WriteString(fmt.Sprintf("  [%s] state %*s → %s\n", status, maxStateWidth, stateStrings[s], outcomeStr))
	}
	sb.WriteString("\n")

	// ============================================================
	// 4. Transition Table
	// ============================================================
	sb.WriteString("Transitions (D=Dead):\n")

	// Header Row
	sb.WriteString(strings.Repeat(" ", maxStateWidth+4) + "|")
	for id := uint64(0); id < dfa.alphabetSize; id++ {
		sb.WriteString(fmt.Sprintf(" %*s ", maxSymbolWidth, symbolStrings[id]))
	}
	sb.WriteString("\n")

	// Separator
	sb.WriteString(strings.Repeat("-", maxStateWidth+4) + "+")
	for id := uint64(0); id < dfa.alphabetSize; id++ {
		sb.WriteString(strings.Repeat("-", maxSymbolWidth+2))
	}
	sb.WriteString("\n")

	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)
	for s := uint64(0); s < dfa.numStates; s++ {
		isDead := dfa.deadStates[s]
		outcome := *outCur.PtrAt(s)
		status := dfaDebugStateIndicatorRune(formatter, s, outcome, isDead)

		sb.WriteString(fmt.Sprintf("  %s %*s |", status, maxStateWidth, stateStrings[s]))

		rowStart := s * dfa.alphabetSize
		for symID := uint64(0); symID < dfa.alphabetSize; symID++ {
			target := *transCur.PtrAt(rowStart + symID)
			cell := stateStrings[target]
			if dfa.deadStates[target] {
				cell = "—"
			}
			sb.WriteString(fmt.Sprintf(" %*s ", maxSymbolWidth, cell))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

/*
DFACursorGet returns the cached transition-table cursor for efficient access.

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
func DFACursorGet[TObservation, TStateOutcome any](dfa *DFA[TObservation, TStateOutcome]) memstruct.ArrayCursor[uint64] {
	return dfa.transitionsCursor
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
func DFAPredecessorSets[TObservation, TStateOutcome any](
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
*/
func DFAStep[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	currentState uint64,
	observation TObservation,
	arrayCursor memstruct.ArrayCursor[uint64],
) (uint64, error) {
	symbolID, ok := dfa.deterministicResolver(observation)
	if !ok {
		var zero uint64
		return zero, &AutomatonError{
			Kind:      AutomatonErrorInvalidSymbolFinite,
			Automaton: "DFA",
			Message:   fmt.Sprintf("invalid symbol: %v", observation),
		}
	}
	transitionIDX := getTransitionIDX(dfa.alphabetSize, currentState, symbolID)
	newState := arrayCursor.PtrAt(transitionIDX)
	return *newState, nil
}

/*
DFAStateOutcome retrieves the semantic outcome for a DFA state.

Every state has an outcome assigned at construction. The client defines
acceptance by interpreting the outcome (e.g. comparing to a sentinel).

Use cases:
- Retrieving token types in lexers
- Obtaining match results from automata
- Client-defined acceptance (e.g. outcome != rejectSentinel)

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- dfa must be a valid DFA instance
- state must be a valid state index

Edge cases:
- Panics if state is out of range
*/
func DFAStateOutcome[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	state uint64,
) (outcome TStateOutcome, ok bool) {
	if state >= dfa.numStates {
		panic(fmt.Errorf(
			"DFAStateOutcome: invalid state %d (max=%d)",
			state,
			dfa.numStates-1,
		))
	}
	outcome = *dfa.outcomesCursor.PtrAt(state)
	return outcome, true
}

/*
DFAStates returns all DFA state IDs as a contiguous slice.

The states are always numbered densely from 0 to numStates-1.

Use cases:
- Iterating over all states (minimization, analysis, visualization)
- Building partitions or worklists
- Debugging and inspection

Time complexity: O(s)
Space complexity: O(s)

Where:

	s = number of states in the DFA
*/
func DFAStates[TObservation, TStateOutcome any, TAs foundation.Integer](
	dfa *DFA[TObservation, TStateOutcome],
) []TAs {
	states := make([]TAs, dfa.numStates)

	for i := uint64(0); i < dfa.numStates; i++ {
		states[i] = TAs(i)
	}

	return states
}
