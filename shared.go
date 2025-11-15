package autarch

import (
	"fmt"
	"math/bits"
	"memcore"
	"memstruct"
)

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
type Transition[TObservation any] struct {
	symbol       Symbol[TObservation]
	currentState uint64
	nextState    uint64
}

//go:inline
func getTransitionIDX(alphabetSize, currentState, symbolID uint64) uint64 {
	return currentState*alphabetSize + symbolID
}

const maxNFAStates uint64 = 256
const wordsPerSubset uint64 = maxNFAStates / 64

type dfaStateSubset struct {
	words [wordsPerSubset]uint64
}

func dfaStateSubsetCreate(states []uint64) *dfaStateSubset {
	subset := &dfaStateSubset{}
	for _, state := range states {
		subset.Add(state)
	}
	return subset
}

//go:inline
func (s *dfaStateSubset) Add(state uint64) {
	w := state >> 6
	b := state & 63
	s.words[w] |= 1 << b
}

//go:inline
func (s *dfaStateSubset) Remove(state uint64) {
	w := state >> 6
	b := state & 63
	s.words[w] &^= 1 << b
}

//go:inline
func (s *dfaStateSubset) Has(state uint64) bool {
	w := state >> 6
	b := state & 63
	return (s.words[w] & (1 << b)) != 0
}

//go:inline
func (s *dfaStateSubset) Clear() {
	for i := range s.words {
		s.words[i] = 0
	}
}

//go:inline
func (s *dfaStateSubset) Union(a, b *dfaStateSubset) {
	for i := range s.words {
		s.words[i] = a.words[i] | b.words[i]
	}
}

//go:inline
func (s *dfaStateSubset) Equals(o *dfaStateSubset) bool {
	for i := range s.words {
		if s.words[i] != o.words[i] {
			return false
		}
	}
	return true
}

// CopyFrom copies an entire subset.
func (dst *dfaStateSubset) CopyFrom(src *dfaStateSubset) {
	for i := range dst.words {
		dst.words[i] = src.words[i]
	}
}

// Intersect computes a ∩ b into dst.
func (dst *dfaStateSubset) Intersect(a, b *dfaStateSubset) {
	for i := range dst.words {
		dst.words[i] = a.words[i] & b.words[i]
	}
}

// Difference computes a \ b into dst.
func (dst *dfaStateSubset) Difference(a, b *dfaStateSubset) {
	for i := range dst.words {
		dst.words[i] = a.words[i] &^ b.words[i]
	}
}

// Count returns number of states present in the subset.
//
//go:inline
func (s *dfaStateSubset) Count() uint64 {
	var c uint64
	for _, w := range s.words {
		c += uint64(bits.OnesCount64(w))
	}
	return c
}

//go:inline
func (s *dfaStateSubset) States() []uint64 {
	states := make([]uint64, 0)

	for wi, w := range s.words {
		for w != 0 {
			bit := bits.TrailingZeros64(w)
			state := uint64(wi)*64 + uint64(bit)

			states = append(states, state)

			w &= w - 1
		}
	}

	return states
}

// determineOutcome picks the "best" outcome for a subset of NFA states.
// We use the lowest-indexed NFA state with a non-invalid outcome.
//
//go:inline
func determineOutcome[TStateOutcome comparable](
	subset *dfaStateSubset,
	stateArray memcore.MarkRaw,
) (TStateOutcome, error) {
	var zero TStateOutcome

	states := subset.States()
	if len(states) == 0 {
		return zero, fmt.Errorf("no states in subset")
	}

	best := ^uint64(0)
	var bestOutcome TStateOutcome

	for _, s := range states {
		outcome := memstruct.ArrayItemGetAtUnsafe[TStateOutcome](stateArray, s)
		if s < best {
			best = s
			bestOutcome = outcome
		}
	}

	return bestOutcome, nil
}
