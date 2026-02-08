package autarch

import (
	"math/bits"
	"memcore"
	"memstruct"
)

/*
SymbolKind represents the type of a symbol definition.

SymbolKind categorizes symbols into different types based on their matching behavior.
This enables optimized processing and symbol identity comparison.

Use cases:
- Categorizing symbols for optimized matching
- Symbol identity and deduplication
- Debugging and symbol analysis

Time complexity: O(1) for all operations
Space complexity: O(1)

Edge cases:
- SymbolKindLiteral represents exact value matches
- SymbolKindRange represents range-based matches (e.g., 'a'-'z')
- SymbolKindClass represents character class matches (e.g., digits, letters)
- SymbolKindWildcard represents wildcard matches (any character)
*/
type SymbolKind uint8

const (
	SymbolKindLiteral  SymbolKind = 0
	SymbolKindRange    SymbolKind = 1
	SymbolKindClass    SymbolKind = 2
	SymbolKindWildcard SymbolKind = 3
)

/*
SymbolKey represents a unique identifier for a symbol definition.

SymbolKey combines a SymbolKind with a hash value to create a unique identity
for symbol definitions. This enables efficient symbol comparison and deduplication
during alphabet merging operations.

Use cases:
- Symbol identity comparison
- Alphabet merging and deduplication
- Symbol lookup and caching

Time complexity: O(1) for all operations
Space complexity: O(1)

Prerequisites:
- Hash should be computed deterministically from symbol definition properties
- Same symbol definition should always produce the same SymbolKey

Edge cases:
- Different symbol definitions may have the same hash (hash collision)
- Kind and Hash together provide stronger uniqueness guarantee
*/
type SymbolKey struct {
	Kind SymbolKind
	Hash uint64
}

/*
SymbolIndexer is a function type that maps observations to symbols in an automaton's alphabet.

The indexer is responsible for converting input observations (e.g., runes, bytes) into
symbol identifiers that the automaton can process. It returns an array of symbols, where
an empty array indicates the observation is not part of the automaton's alphabet, and
a non-empty array contains all valid symbols for that observation.

Use cases:
- Mapping input characters to symbol IDs
- Validating input against alphabet
- Supporting custom symbol types
- Supporting multiple symbols per observation (e.g., case-insensitive matching)

Time complexity: Implementation-dependent (typically O(1) with hash map)
Space complexity: O(k) where k is the number of symbols returned

Prerequisites:
- Must handle all observations that may be processed
- Must return consistent symbols for same observation
- Must return empty array for invalid observations

Edge cases:
- Must be deterministic (same observation → same symbols)
- Empty array indicates invalid observation
- Multiple symbols enable non-deterministic behavior at the observation level
*/
type SymbolIndexer[TObservation any] func(observation TObservation) []Symbol[TObservation]

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
- SymbolID must be less than alphabet size
- SymbolDescription should be meaningful for debugging

Edge cases:
- SymbolID 0 is valid and represents the first alphabet symbol
*/
type Symbol[TObservation any] struct {
	SymbolDescription string
	SymbolID          uint64
}

/*
SymbolDefinition represents a symbol definition with an ID, name, and match predicate.

SymbolDefinition allows declarative definition of symbols in an alphabet, where each
symbol has a unique ID, a human-readable name for debugging, and a predicate function
that determines which observations match this symbol.

Use cases:
- Declarative alphabet definition
- Building indexers from symbol definitions
- Supporting complex matching logic (ranges, classes, etc.)
- Case-insensitive or multi-symbol matching

Time complexity: O(1) for field access, O(1) for Match predicate call
Space complexity: O(1) per definition

Prerequisites:
- ID must be unique within the alphabet
- Match function must be deterministic
- Match function should be fast (called frequently during indexing)

Edge cases:
- Multiple definitions can match the same observation (enables multi-symbol behavior)
- Match function should handle all possible observation values
- Empty match results indicate invalid observations
*/
type SymbolDefinition[TObservation any] struct {
	ID          uint64
	Name        string
	Match       func(observation TObservation) bool
	Observation *TObservation // Optional: original observation value for formatting (e.g., for literal symbols)
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
- ID validation is responsibility of automaton construction
*/
func SymbolCreate[TObservation any](description string, id uint64) Symbol[TObservation] {
	return Symbol[TObservation]{
		SymbolDescription: description,
		SymbolID:          id,
	}
}

/*
SymbolIndexerBuild constructs a SymbolIndexer from an alphabet of symbol definitions.

The function creates an indexer that tests each observation against all symbol definitions
in the alphabet, returning all matching symbols. This enables declarative alphabet definition
and supports multiple symbols per observation (e.g., case-insensitive matching, character classes).

Use cases:
- Building indexers from declarative symbol definitions
- Creating indexers with complex matching logic
- Supporting case-insensitive or multi-symbol matching
- Simplifying alphabet construction

Time complexity: O(n) where n is the number of symbol definitions in the alphabet
Space complexity: O(k) where k is the number of matching symbols returned

Prerequisites:
- alphabet must contain all symbol definitions
- Each definition's Match function must be deterministic
- Symbol IDs should be unique within the alphabet

Edge cases:
- Returns empty array if no symbols match the observation
- Multiple symbols can match the same observation
- Match functions are called in order, so order matters for performance
- Empty alphabet results in an indexer that always returns empty array
*/
func SymbolIndexerBuild[TObservation any](alphabet []SymbolDefinition[TObservation]) SymbolIndexer[TObservation] {
	return func(observation TObservation) []Symbol[TObservation] {
		var out []Symbol[TObservation]

		for _, sym := range alphabet {
			if sym.Match(observation) {
				out = append(out, SymbolCreate[TObservation](sym.Name, sym.ID))
			}
		}
		return out
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

/*
OutcomeResolutionFn resolves which outcome to assign to a DFA state when multiple NFA
accepting states are merged during NFA-to-DFA conversion.

The function receives the list of NFA state IDs in the subset and their corresponding
outcomes (only accepting states carry meaningful outcomes).

Use cases:
- Custom outcome selection logic during DFA construction
- Priority-based rule resolution
- Context-aware outcome selection

Time complexity: Should be O(n) where n is number of states
Space complexity: Should be O(1) - avoid allocations in hot path

Prerequisites:
- states and outcomes must be parallel arrays (same length)

Edge cases:
- Empty states list represents a non-accepting DFA state
- All zero-value outcomes represent no accepting state
- Function should handle any number of states efficiently

Only accepting states are expected to carry semantic outcomes. Non-accepting states
should contain the zero value of TStateOutcome.
*/
type OutcomeResolutionFn[TStateOutcome comparable] func(
	states []uint64,
	outcomes []TStateOutcome,
) (outcome TStateOutcome, ok bool)

//go:inline
func determineOutcome[TStateOutcome comparable](
	subset *dfaStateSubset,
	stateArray memcore.MarkRaw,
) (outcome TStateOutcome, ok bool) {

	states := subset.States()
	if len(states) == 0 {
		return outcome, false
	}

	bestStateID := ^uint64(0)

	for _, s := range states {
		o := memstruct.ArrayItemGetAtUnsafe[TStateOutcome](stateArray, s)

		var zero TStateOutcome
		if o != zero {
			if s < bestStateID {
				bestStateID = s
				outcome = o
				ok = true
			}
		}
	}

	return
}

/*
OutcomeResolutionFirst selects the accepting outcome with the lowest state ID.

This corresponds to rule-order priority where earlier rules win.

Use cases:
- Default outcome resolution during DFA construction
- Priority-based lexers (first rule added wins)
- Deterministic resolution

Time complexity: O(n) where n is number of states
Space complexity: O(1)

Prerequisites:
- states and outcomes must be parallel arrays

Edge cases:
- Returns ok=false if no accepting states exist
- Returns ok=false if states list is empty
*/
func OutcomeResolutionFirst[TStateOutcome comparable](
	states []uint64,
	outcomes []TStateOutcome,
) (outcome TStateOutcome, ok bool) {

	bestStateID := ^uint64(0)

	for i, s := range states {
		o := outcomes[i]

		var zero TStateOutcome
		if o != zero {
			if s < bestStateID {
				bestStateID = s
				outcome = o
				ok = true
			}
		}
	}

	return
}

/*
OutcomeResolutionLast selects the accepting outcome with the highest state ID.

This corresponds to reverse rule-order priority where later rules override earlier ones.

Use cases:
- Override-based lexers
- Alternative priority schemes

Time complexity: O(n) where n is number of states
Space complexity: O(1)

Prerequisites:
- states and outcomes must be parallel arrays

Edge cases:
- Returns ok=false if no accepting states exist
- Returns ok=false if states list is empty
*/
func OutcomeResolutionLast[TStateOutcome comparable](
	states []uint64,
	outcomes []TStateOutcome,
) (outcome TStateOutcome, ok bool) {

	var zero TStateOutcome
	bestStateID := uint64(0)

	for i, s := range states {
		o := outcomes[i]
		if o == zero {
			continue
		}
		if !ok || s > bestStateID {
			bestStateID = s
			outcome = o
			ok = true
		}
	}

	return
}
