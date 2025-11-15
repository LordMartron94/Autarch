package regex

import (
	"autarch"
	"fmt"
	"memarch"
	"sort"
)

func RegexToNFA[TStateOutcome comparable](
	allocFn memarch.AllocationFn,
	regex string,
	acceptToken TStateOutcome,
	invalidState TStateOutcome,
) (*autarch.NFA[rune, TStateOutcome], error) {

	// 1. Tokenize
	tokens, err := tokenizeRegex(regex)
	if err != nil {
		return nil, fmt.Errorf("regex tokenize error: %w", err)
	}

	// 2. Parse to AST
	ast, err := parseRegex(tokens)
	if err != nil {
		return nil, fmt.Errorf("regex parse error: %w", err)
	}

	// 3. Thompson construction
	var counter uint64 = 0
	fragment := thompsonBuild(ast, &counter)

	// Number of states
	numStates := counter

	// 4. Collect alphabet (unique runes in literal transitions)
	alphabet := collectAlphabet(fragment.transitions)

	// 5. Build indexer
	indexer := buildIndexer(alphabet)

	// 6. Build state outcomes (all false except accept)
	states := make([]TStateOutcome, numStates)
	for i := uint64(0); i < numStates; i++ {
		states[i] = invalidState
	}
	states[fragment.accept] = acceptToken

	// 7. Normalize transitions: assign symbolID from indexer
	nfaTransitions, err := normalizeTransitions(fragment.transitions, indexer)
	if err != nil {
		return nil, err
	}

	// 8. Build the final NFA
	nfa := autarch.NFACreate(
		allocFn,
		alphabet,
		nfaTransitions,
		[]uint64{fragment.start},
		states,
		indexer,
	)

	return nfa, nil
}

// ------------------------------------------------------------
// ALPHABET COLLECTION
// ------------------------------------------------------------
//
// Collect all runes used in literal transitions.
// Epsilon transitions are skipped.
// Alphabet is sorted for deterministic ordering.
// ------------------------------------------------------------

func collectAlphabet(transitions []autarch.Transition[rune]) []rune {
	set := make(map[rune]struct{})

	for _, tr := range transitions {
		if tr.Symbol.SymbolID == autarch.AutarchEpsilonID {
			continue
		}
		r := []rune(tr.Symbol.SymbolDescription)
		if len(r) == 1 {
			set[r[0]] = struct{}{}
		}
	}

	out := make([]rune, 0, len(set))
	for r := range set {
		out = append(out, r)
	}

	// deterministic ordering
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})

	return out
}

// ------------------------------------------------------------
// INDEXER CREATION
// ------------------------------------------------------------
//
// indexer maps a rune → Symbol[rune].
// symbolID is its index in the alphabet slice.
//
// Epsilon is handled separately.
//
// ------------------------------------------------------------

func buildIndexer(alphabet []rune) autarch.SymbolIndexer[rune] {
	m := make(map[rune]uint64, len(alphabet))
	for i, r := range alphabet {
		m[r] = uint64(i)
	}

	return func(observation rune) (autarch.Symbol[rune], bool) {
		id, ok := m[observation]
		if !ok {
			return autarch.Symbol[rune]{}, false
		}
		return autarch.SymbolCreate[rune](string(observation), id), true
	}
}

// ------------------------------------------------------------
// NORMALIZE TRANSITIONS
// ------------------------------------------------------------
//
// Convert the Thompson transitions (with temporary symbolID=0) into
// proper transitions with correct symbolIDs assigned from indexer.
//
// ------------------------------------------------------------

func normalizeTransitions(
	transitions []autarch.Transition[rune],
	indexer autarch.SymbolIndexer[rune],
) ([]autarch.Transition[rune], error) {

	out := make([]autarch.Transition[rune], 0, len(transitions))

	for _, tr := range transitions {
		// Epsilon
		if tr.Symbol.SymbolID == autarch.AutarchEpsilonID {
			out = append(out, tr)
			continue
		}

		// Literal transition
		runes := []rune(tr.Symbol.SymbolDescription)
		if len(runes) != 1 {
			return nil, fmt.Errorf("invalid literal symbol description: %s", tr.Symbol.SymbolDescription)
		}
		r := runes[0]

		sym, ok := indexer(r)
		if !ok {
			return nil, fmt.Errorf("literal %q not in alphabet", r)
		}

		out = append(out, autarch.Transition[rune]{
			Symbol:       sym,
			CurrentState: tr.CurrentState,
			NextState:    tr.NextState,
		})
	}

	return out, nil
}
