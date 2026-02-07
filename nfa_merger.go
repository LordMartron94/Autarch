package autarch

import (
	"memarch"
	"memstruct"
)

/*
NFAMergeOr combines two NFAs into a single NFA recognizing the union of their languages.

Symbol definitions are not deduplicated. Each NFA’s alphabet is preserved and
assigned new global symbol IDs.

Semantic symbol reuse must be handled at the Regula/compiler layer.
*/
func NFAMergeOr[TObservation any, TStateOutcome comparable](
	nfaA *NFA[TObservation, TStateOutcome],
	nfaB *NFA[TObservation, TStateOutcome],
	allocFn memarch.AllocationFn,
	invalidState TStateOutcome,
) *NFA[TObservation, TStateOutcome] {

	// ------------------------------------------------------------
	// 1. Merge alphabet (no semantic deduplication)
	// ------------------------------------------------------------

	newAlphabet := make(
		[]SymbolDefinition[TObservation],
		0,
		len(nfaA.alphabet)+len(nfaB.alphabet),
	)

	newAlphabet = append(newAlphabet, nfaA.alphabet...)
	newAlphabet = append(newAlphabet, nfaB.alphabet...)

	newIndexer := SymbolIndexerBuild(newAlphabet)

	// ------------------------------------------------------------
	// 2. Build symbol ID remaps
	// ------------------------------------------------------------

	remapA := make(map[uint64]uint64, len(nfaA.alphabet)+1)
	remapB := make(map[uint64]uint64, len(nfaB.alphabet)+1)

	for i := range nfaA.alphabet {
		remapA[uint64(i)] = uint64(i)
	}
	for i := range nfaB.alphabet {
		remapB[uint64(i)] = uint64(len(nfaA.alphabet) + i)
	}

	remapA[AutarchEpsilonID] = AutarchEpsilonID
	remapB[AutarchEpsilonID] = AutarchEpsilonID

	// ------------------------------------------------------------
	// 3. Build new state list
	// ------------------------------------------------------------

	offsetA := uint64(1)
	offsetB := offsetA + nfaA.numStates

	newNumStates := nfaA.numStates + nfaB.numStates + 1
	newStates := make([]TStateOutcome, newNumStates)

	newStates[0] = invalidState

	statesA := NFAStatesGet(nfaA)
	for i := uint64(0); i < nfaA.numStates; i++ {
		newStates[i+offsetA] = memstruct.ArrayItemGetAtUnsafe[TStateOutcome](statesA, i)
	}

	statesB := NFAStatesGet(nfaB)
	for i := uint64(0); i < nfaB.numStates; i++ {
		newStates[i+offsetB] = memstruct.ArrayItemGetAtUnsafe[TStateOutcome](statesB, i)
	}

	// ------------------------------------------------------------
	// 4. Merge transitions
	// ------------------------------------------------------------

	newTransitions := make([]Transition[TObservation], 0)
	epsilon := EpsilonSymbolCreate[TObservation]()

	for key, nextStates := range nfaA.transitions {
		from, sym := key[0], key[1]
		globalSym := remapA[sym]

		for _, to := range nextStates {
			newTransitions = append(newTransitions, Transition[TObservation]{
				CurrentState: from + offsetA,
				NextState:    to + offsetA,
				Symbol: SymbolCreate[TObservation](
					nfaA.alphabet[sym].Name,
					globalSym,
				),
			})
		}
	}

	for key, nextStates := range nfaB.transitions {
		from, sym := key[0], key[1]
		globalSym := remapB[sym]

		for _, to := range nextStates {
			newTransitions = append(newTransitions, Transition[TObservation]{
				CurrentState: from + offsetB,
				NextState:    to + offsetB,
				Symbol: SymbolCreate[TObservation](
					nfaB.alphabet[sym].Name,
					globalSym,
				),
			})
		}
	}

	for _, s := range nfaA.startingStates {
		newTransitions = append(newTransitions, Transition[TObservation]{
			CurrentState: 0,
			NextState:    s + offsetA,
			Symbol:       epsilon,
		})
	}

	for _, s := range nfaB.startingStates {
		newTransitions = append(newTransitions, Transition[TObservation]{
			CurrentState: 0,
			NextState:    s + offsetB,
			Symbol:       epsilon,
		})
	}

	// ------------------------------------------------------------
	// 5. Create merged NFA
	// ------------------------------------------------------------

	return NFACreate(
		allocFn,
		newAlphabet,
		newTransitions,
		[]uint64{0},
		newStates,
		newIndexer,
	)
}
