package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memstruct"
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
		if transition.symbol.symbolID >= alphabetSize && transition.symbol.symbolID != epsilonID {
			panic(fmt.Errorf("symbol ID must be less than the alphabetSize, got=%d,max=%d", transition.symbol.symbolID, alphabetSize))
		}

		idx := getTransitionIDX(rowStride, transition.currentState, transition.symbol.symbolID)

		if memstruct.ArrayItemGetAtUnsafe[uint64](transitionArray, idx) != 0 {
			panic(fmt.Errorf("cannot have multiple transitions for input symbol %v and state %d", transition.symbol.symbolDescription, transition.currentState))
		}

		memstruct.ArraySetAtUnsafe(transitionArray, idx, transition.nextState)
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
			return zero, fmt.Errorf("invalid symbol encountered: %s", symbol.symbolDescription)
		}

		transitionIDX := getTransitionIDX(dfa.alphabetSize, state, symbol.symbolID)
		newState := arrayCursor.PtrAt(transitionIDX)
		state = *newState
	}

	return memstruct.ArrayItemGetAtUnsafe[TStateOutcome](dfa.states, state), nil
}
