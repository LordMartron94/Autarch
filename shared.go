package autarch

const epsilonID = ^uint64(0)

// SymbolIndexer must return the symbol for a given observation.
// It should return false when the observation is not included in the language's alphabet.
type SymbolIndexer[TObservation any] func(observation TObservation) (Symbol[TObservation], bool)

type Symbol[TObservation any] struct {
	symbolDescription string
	symbolID          uint64
}

func SymbolCreate[TObservation any](description string, id uint64) Symbol[TObservation] {
	return Symbol[TObservation]{
		symbolDescription: description,
		symbolID:          id,
	}
}

func EpsilonSymbolCreate[TObservation any]() Symbol[TObservation] {
	return Symbol[TObservation]{
		symbolDescription: "epsilon",
		symbolID:          epsilonID,
	}
}

// Transition defines a single transition between one state to the next
// for a given input symbol.
type Transition[TSymbol any] struct {
	symbol       Symbol[TSymbol]
	currentState uint64
	nextState    uint64
}

//go:inline
func getTransitionIDX(alphabetSize, currentState, symbolID uint64) uint64 {
	return currentState*alphabetSize + symbolID
}
