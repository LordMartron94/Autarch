package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memstruct"
	"strings"
)

// DFA is a deterministic finite automaton.
type DFA[TObservation any, TStateOutcome comparable] struct {
	states      memcore.MarkRaw // Array[TStateOutcome]
	transitions memcore.MarkRaw // Array[uint64] -- stateAmount * alphabetSize

	indexer SymbolIndexer[TObservation]

	alphabet     []TObservation
	numStates    uint64
	alphabetSize uint64
}

func DFAStatesGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return dfa.states
}

func DFAIndexerGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) SymbolIndexer[TObservation] {
	return dfa.indexer
}

func DFAAlphabetGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) []TObservation {
	return dfa.alphabet
}

// DFACreate creates a new DFA, creating its data structures using the provided allocation function.
// For the state slice, each state that is accepting must return true.
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

// DFARun consumes the input and returns the state outcome defined by the language.
// It returns an error if something went wrong while processing.
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

// DFADebugPrint prints a deterministic, readable description of the DFA.
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

func DFACursorGet[TObservation any, TStateOutcome comparable](dfa *DFA[TObservation, TStateOutcome]) memstruct.ArrayCursor[uint64] {
	return memstruct.ArrayCursorCreate[uint64](dfa.transitions)
}

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

// DFAPredecessorSets computes all predecessor sets (incoming transitions) for a given state.
// Index by: symbolID,state
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

// DFAStep advances the DFA by one observation.
// It returns the next state.
// It returns an error if the observation is not in the alphabet.
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

// DFAStateOutcome returns the outcome (e.g., token type) for a given state.
func DFAStateOutcome[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
	state uint64,
) TStateOutcome {
	return memstruct.ArrayItemGetAtUnsafe[TStateOutcome](dfa.states, state)
}
