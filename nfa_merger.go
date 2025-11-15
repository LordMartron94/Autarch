package autarch

import (
	"fmt"
	"memarch"
	"memstruct"
)

// NFAMergeOr combines two NFAs into a single one using an OR operation (union).
//
// It requires a 'keyFn' to generate a comparable key (TKey) from a
// non-comparable TObservation, which is used to build the merged alphabet.
//
// nfaA's states are given priority (lower state IDs) over nfaB's states.
func NFAMergeOr[TObservation any, TKey comparable, TStateOutcome comparable](
	nfaA *NFA[TObservation, TStateOutcome],
	nfaB *NFA[TObservation, TStateOutcome],
	allocFn memarch.AllocationFn,
	invalidState TStateOutcome,
	keyFn func(TObservation) TKey,
) *NFA[TObservation, TStateOutcome] {

	// -----------------------------------------------------------------
	// 1. Build Merged Alphabet & New Indexer
	// -----------------------------------------------------------------

	newAlphabet := make([]TObservation, 0, len(nfaA.alphabet)+len(nfaB.alphabet))
	globalKeyMap := make(map[TKey]uint64)

	// Add A's alphabet
	for _, obs := range nfaA.alphabet {
		key := keyFn(obs)
		if _, exists := globalKeyMap[key]; !exists {
			globalKeyMap[key] = uint64(len(newAlphabet))
			newAlphabet = append(newAlphabet, obs)
		}
	}
	// Add B's alphabet
	for _, obs := range nfaB.alphabet {
		key := keyFn(obs)
		if _, exists := globalKeyMap[key]; !exists {
			globalKeyMap[key] = uint64(len(newAlphabet))
			newAlphabet = append(newAlphabet, obs)
		}
	}

	// Create new indexer based on the key map
	newIndexer := func(observation TObservation) (Symbol[TObservation], bool) {
		key := keyFn(observation)
		id, ok := globalKeyMap[key]
		if !ok {
			return Symbol[TObservation]{}, false
		}
		// The description isn't critical, but we can make one.
		return SymbolCreate[TObservation](fmt.Sprintf("%v", observation), id), true
	}

	// -----------------------------------------------------------------
	// 2. Build Local-to-Global SymbolID Remapping Tables
	// -----------------------------------------------------------------

	remapA := buildSymbolRemap(nfaA, globalKeyMap, keyFn)
	remapB := buildSymbolRemap(nfaB, globalKeyMap, keyFn)

	// -----------------------------------------------------------------
	// 3. Build New State List
	// Layout: [NewStart (ID 0), ...nfaA states..., ...nfaB states...]
	// -----------------------------------------------------------------

	offsetA := uint64(1)          // nfaA's states start at ID 1
	offsetB := nfaA.numStates + 1 // nfaB's states start after nfaA's

	newNumStates := nfaA.numStates + nfaB.numStates + 1
	newStates := make([]TStateOutcome, newNumStates)
	newStates[0] = invalidState // The new start state is not accepting

	// Copy A's states
	statesA := NFAStatesGet(nfaA)
	for i := uint64(0); i < nfaA.numStates; i++ {
		newStates[i+offsetA] = memstruct.ArrayItemGetAtUnsafe[TStateOutcome](statesA, i)
	}

	// Copy B's states
	statesB := NFAStatesGet(nfaB)
	for i := uint64(0); i < nfaB.numStates; i++ {
		newStates[i+offsetB] = memstruct.ArrayItemGetAtUnsafe[TStateOutcome](statesB, i)
	}

	// -----------------------------------------------------------------
	// 4. Build New Transition List (with remapped state & symbol IDs)
	// -----------------------------------------------------------------

	newTransitions := make([]Transition[TObservation], 0)
	epsilon := EpsilonSymbolCreate[TObservation]()

	// Add A's transitions, remapped
	for key, nextStates := range nfaA.transitions {
		localCurrent, localSymbolID := key[0], key[1]
		symbol := remapTransitionSymbol(localSymbolID, remapA, nfaA.alphabet)

		for _, localNext := range nextStates {
			newTransitions = append(newTransitions, Transition[TObservation]{
				CurrentState: localCurrent + offsetA,
				NextState:    localNext + offsetA,
				Symbol:       symbol,
			})
		}
	}

	// Add B's transitions, remapped
	for key, nextStates := range nfaB.transitions {
		localCurrent, localSymbolID := key[0], key[1]
		symbol := remapTransitionSymbol(localSymbolID, remapB, nfaB.alphabet)

		for _, localNext := range nextStates {
			newTransitions = append(newTransitions, Transition[TObservation]{
				CurrentState: localCurrent + offsetB,
				NextState:    localNext + offsetB,
				Symbol:       symbol,
			})
		}
	}

	// Add epsilon transitions from new start state (0)
	for _, startA := range nfaA.startingStates {
		newTransitions = append(newTransitions, Transition[TObservation]{
			CurrentState: 0,
			NextState:    startA + offsetA,
			Symbol:       epsilon,
		})
	}
	for _, startB := range nfaB.startingStates {
		newTransitions = append(newTransitions, Transition[TObservation]{
			CurrentState: 0,
			NextState:    startB + offsetB,
			Symbol:       epsilon,
		})
	}

	// -----------------------------------------------------------------
	// 5. Create the New NFA
	// -----------------------------------------------------------------

	return NFACreate(
		allocFn,
		newAlphabet,
		newTransitions,
		[]uint64{0},
		newStates,
		newIndexer,
	)
}

// buildSymbolRemap creates a map[local_symbol_id] -> global_symbol_id
func buildSymbolRemap[TObservation any, TKey comparable, TStateOutcome comparable](
	nfa *NFA[TObservation, TStateOutcome],
	globalKeyMap map[TKey]uint64,
	keyFn func(TObservation) TKey,
) map[uint64]uint64 {

	remap := make(map[uint64]uint64, len(nfa.alphabet)+1)
	for localID, obs := range nfa.alphabet {
		key := keyFn(obs)
		globalID, ok := globalKeyMap[key]
		if !ok {
			// This should be impossible if logic is correct
			panic(fmt.Errorf("internal merge error: symbol %v not in global map", obs))
		}
		remap[uint64(localID)] = globalID
	}
	// Epsilon maps to itself
	remap[AutarchEpsilonID] = AutarchEpsilonID
	return remap
}

// remapTransitionSymbol creates a new Symbol object for the transition list.
// The new Symbol contains the correct global SymbolID.
// This helper does not need the keyFn, as it works with IDs and indices.
func remapTransitionSymbol[TObservation any](
	localSymbolID uint64,
	remap map[uint64]uint64,
	alphabet []TObservation,
) Symbol[TObservation] {

	if localSymbolID == AutarchEpsilonID {
		return EpsilonSymbolCreate[TObservation]()
	}

	globalSymbolID := remap[localSymbolID]

	// Description is only for debugging, but we can recreate it.
	desc := fmt.Sprintf("%v", alphabet[localSymbolID])
	return SymbolCreate[TObservation](desc, globalSymbolID)
}
