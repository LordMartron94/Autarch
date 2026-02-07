package autarch

import (
	"math/bits"
	"memcore"
	"memstruct"
)

const AutarchEpsilonID = ^uint64(0)
const AutarchWildcardID = ^uint64(1)

/*
SymbolIndexer is a function type that maps observations to symbols in an automaton's alphabet.

The indexer is responsible for converting input observations (e.g., runes, bytes) into
symbol identifiers that the automaton can process. It returns false when an observation
is not part of the automaton's alphabet.

Use cases:
- Mapping input characters to symbol IDs
- Validating input against alphabet
- Supporting custom symbol types

Time complexity: Implementation-dependent (typically O(1) with hash map)
Space complexity: O(1)

Prerequisites:
- Must handle all observations that may be processed
- Must return consistent SymbolID for same observation
- Must return false for invalid observations

Edge cases:
- Should handle epsilon and wildcard symbols if needed
- Must be deterministic (same observation → same symbol)
*/
type SymbolIndexer[TObservation any] func(observation TObservation) (Symbol[TObservation], bool)

/*
Symbol represents a symbol in an automaton's alphabet with its identifier and description.

The SymbolID is used for efficient transition table indexing, while SymbolDescription
provides human-readable information for debugging and error messages.

Use cases:
- Representing alphabet symbols in transitions
- Debugging automaton structure
- Error reporting with symbol descriptions

Time complexity: O(1) for all operations
Space complexity: O(1) per symbol

Prerequisites:
- SymbolID must be unique within an automaton's alphabet
- SymbolID must be less than alphabet size (or special IDs like epsilon)
- SymbolDescription should be meaningful for debugging

Edge cases:
- Special IDs: AutarchEpsilonID for epsilon, AutarchWildcardID for wildcard
- SymbolID 0 is valid and represents the first alphabet symbol
*/
type Symbol[TObservation any] struct {
	SymbolDescription string
	SymbolID          uint64
}

/*
SymbolCreate constructs a new Symbol with the given description and ID.

Use cases:
- Creating symbols for transition definitions
- Building alphabet representations
- Constructing symbols programmatically

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- id must be a valid symbol ID for the target automaton
- description should be meaningful for debugging

Edge cases:
- Special IDs (epsilon, wildcard) can use any description
- ID validation is responsibility of automaton construction
*/
func SymbolCreate[TObservation any](description string, id uint64) Symbol[TObservation] {
	return Symbol[TObservation]{
		SymbolDescription: description,
		SymbolID:          id,
	}
}

/*
EpsilonSymbolCreate creates a symbol representing an epsilon (ε) transition.

Epsilon transitions consume no input and allow the automaton to move between
states without processing an observation. They are used in NFAs for pattern
construction and are eliminated during NFA-to-DFA conversion.

Use cases:
- Creating epsilon transitions in NFAs
- Building complex automata patterns
- Representing optional or concatenated patterns

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- None

Edge cases:
- Epsilon symbol ID is AutarchEpsilonID (max uint64)
- Epsilon transitions are only valid in NFAs, not DFAs
*/
func EpsilonSymbolCreate[TObservation any]() Symbol[TObservation] {
	return Symbol[TObservation]{
		SymbolDescription: "epsilon",
		SymbolID:          AutarchEpsilonID,
	}
}

/*
Transition represents a single state transition in an automaton.

A transition specifies moving from a current state to a next state when
processing a specific symbol. In NFAs, multiple transitions from the same
state with the same symbol are allowed; in DFAs, exactly one is required.

Use cases:
- Defining automaton structure
- Building transition tables
- Representing state machine edges

Time complexity: O(1) for all operations
Space complexity: O(1) per transition

Prerequisites:
- CurrentState and NextState must be valid state indices
- Symbol must have valid SymbolID for the automaton
- For DFAs, each (state, symbol) pair must appear exactly once

Edge cases:
- Epsilon transitions use AutarchEpsilonID
- Self-transitions (CurrentState == NextState) are valid
- Dead states have no outgoing transitions
*/
type Transition[TObservation any] struct {
	Symbol       Symbol[TObservation]
	CurrentState uint64
	NextState    uint64
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
//
// We implement the priority rule:
// Use the outcome of the lowest-indexed NFA state that has a
// "non-invalid" (i.e., non-zero) outcome.
//
// If all states in the subset have a "zero" outcome, the DFA state
// is non-accepting (returns the zero outcome).
//
//go:inline
func determineOutcome[TStateOutcome comparable](
	subset *dfaStateSubset,
	stateArray memcore.MarkRaw,
	invalidOutcome TStateOutcome,
) TStateOutcome {
	states := subset.States()
	if len(states) == 0 {
		return invalidOutcome // An empty (dead) state is non-accepting
	}

	bestStateID := ^uint64(0)     // Start at max uint64
	bestOutcome := invalidOutcome // Default to the non-accepting outcome

	for _, s := range states {
		outcome := memstruct.ArrayItemGetAtUnsafe[TStateOutcome](stateArray, s)

		if outcome != invalidOutcome {
			if s < bestStateID {
				bestStateID = s
				bestOutcome = outcome
			}
		}
	}

	return bestOutcome
}
