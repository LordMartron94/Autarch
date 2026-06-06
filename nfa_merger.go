package autarch

import (
	"memarch"
	"memstruct"
)

/*
NFAMergeOr combines two NFAs into a single NFA recognizing the union of their languages.

The function merges the alphabets of both NFAs, deduplicating symbols by name.
Symbols with the same name from both NFAs are treated as the same symbol and
only appear once in the merged alphabet. Symbol IDs are reassigned to match
their array indices, maintaining the invariant that SymbolDefinition.ID == index.

Transitions from both NFAs are remapped to use the deduplicated symbol IDs,
ensuring correct behavior when the merged NFA is converted to a DFA.

Use cases:
- Combining lexer rules that share the same alphabet (e.g., from a shared compilation context)
- Merging multiple pattern NFAs into a single recognizer
- Building union automata for alternation patterns

Prerequisites:
- Both NFAs must use compatible observation types
- State outcomes must be comparable types
- allocFn must be a valid memory allocation function

Edge cases:
- Empty alphabets are handled gracefully
- Duplicate symbol names are automatically deduplicated
- Symbol IDs are guaranteed to match array indices after merging
*/
func NFAMergeOr[TObservation any, TStateOutcome comparable](
	nfaA *NFA[TObservation, TStateOutcome],
	nfaB *NFA[TObservation, TStateOutcome],
	allocFn memarch.AllocationFn,
) *NFA[TObservation, TStateOutcome] {

	merge := MergeAlphabets(nfaA.alphabet, nfaB.alphabet)
	newAlphabet := merge.Alphabet
	remapA := merge.Remaps[0]
	remapB := merge.Remaps[1]

	mergedResolver := MergeTwoAlphabetsNondeterministicResolver(
		newAlphabet, remapA, remapB,
		nfaA.nondeterministicResolver, nfaB.nondeterministicResolver,
	)

	// ------------------------------------------------------------
	// 2. Build merged state space
	// ------------------------------------------------------------

	offsetA := uint64(1)
	offsetB := offsetA + nfaA.numStates

	newNumStates := nfaA.numStates + nfaB.numStates + 1

	newOutcomes := make([]TStateOutcome, newNumStates)

	outA := nfaA.outcomes
	for i := uint64(0); i < nfaA.numStates; i++ {
		newOutcomes[i+offsetA] = memstruct.ArrayItemGetAtUnsafe[TStateOutcome](outA, i)
	}

	outB := nfaB.outcomes
	for i := uint64(0); i < nfaB.numStates; i++ {
		newOutcomes[i+offsetB] = memstruct.ArrayItemGetAtUnsafe[TStateOutcome](outB, i)
	}

	// state 0 is the new start; its outcome remains the zero value (caller may override if needed)

	// ------------------------------------------------------------
	// 3. Merge symbol transitions
	// ------------------------------------------------------------

	newTransitions := make([]Transition[TObservation], 0)

	for key, nexts := range nfaA.transitions {
		from, sym := key[0], key[1]
		global := remapA[sym]

		for _, to := range nexts {
			newTransitions = append(newTransitions, Transition[TObservation]{
				CurrentState: from + offsetA,
				NextState:    to + offsetA,
				Symbol:       SymbolCreate[TObservation](nfaA.alphabet[sym].Name, global),
			})
		}
	}

	for key, nexts := range nfaB.transitions {
		from, sym := key[0], key[1]
		global := remapB[sym]

		for _, to := range nexts {
			newTransitions = append(newTransitions, Transition[TObservation]{
				CurrentState: from + offsetB,
				NextState:    to + offsetB,
				Symbol:       SymbolCreate[TObservation](nfaB.alphabet[sym].Name, global),
			})
		}
	}

	// ------------------------------------------------------------
	// 4. Merge epsilon edges
	// ------------------------------------------------------------

	newEpsilonEdges := make(map[uint64][]uint64)

	for from, nexts := range nfaA.epsilonEdges {
		nf := from + offsetA
		for _, ns := range nexts {
			newEpsilonEdges[nf] = append(newEpsilonEdges[nf], ns+offsetA)
		}
	}

	for from, nexts := range nfaB.epsilonEdges {
		nf := from + offsetB
		for _, ns := range nexts {
			newEpsilonEdges[nf] = append(newEpsilonEdges[nf], ns+offsetB)
		}
	}

	for _, s := range nfaA.startingStates {
		newEpsilonEdges[0] = append(newEpsilonEdges[0], s+offsetA)
	}
	for _, s := range nfaB.startingStates {
		newEpsilonEdges[0] = append(newEpsilonEdges[0], s+offsetB)
	}

	// ------------------------------------------------------------
	// 5. Create merged NFA
	// ------------------------------------------------------------

	return NFACreate(
		allocFn,
		newAlphabet,
		newTransitions,
		newEpsilonEdges,
		[]uint64{0},
		newOutcomes,
		mergedResolver,
	)
}
