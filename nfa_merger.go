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

	// ------------------------------------------------------------
	// 1. Merge alphabet
	// ------------------------------------------------------------

	nameToDef := make(map[string]SymbolDefinition[TObservation])
	nameToNewID := make(map[string]uint64)

	for _, def := range nfaA.alphabet {
		if _, ok := nameToDef[def.Name]; !ok {
			id := uint64(len(nameToDef))
			nameToDef[def.Name] = def
			nameToNewID[def.Name] = id
		}
	}
	for _, def := range nfaB.alphabet {
		if _, ok := nameToDef[def.Name]; !ok {
			id := uint64(len(nameToDef))
			nameToDef[def.Name] = def
			nameToNewID[def.Name] = id
		}
	}

	added := make(map[string]bool)
	newAlphabet := make([]SymbolDefinition[TObservation], 0, len(nameToDef))

	for _, def := range nfaA.alphabet {
		if !added[def.Name] {
			def.ID = nameToNewID[def.Name]
			newAlphabet = append(newAlphabet, def)
			added[def.Name] = true
		}
	}
	for _, def := range nfaB.alphabet {
		if !added[def.Name] {
			def.ID = nameToNewID[def.Name]
			newAlphabet = append(newAlphabet, def)
			added[def.Name] = true
		}
	}

	for i := range newAlphabet {
		newAlphabet[i].ID = uint64(i)
	}

	nameToFinalID := make(map[string]uint64)
	for i, def := range newAlphabet {
		nameToFinalID[def.Name] = uint64(i)
	}

	remapA := make(map[uint64]uint64)
	remapB := make(map[uint64]uint64)

	for i, def := range nfaA.alphabet {
		remapA[uint64(i)] = nameToFinalID[def.Name]
	}
	for i, def := range nfaB.alphabet {
		remapB[uint64(i)] = nameToFinalID[def.Name]
	}

	seenEpoch := make([]uint32, len(newAlphabet))
	currentEpoch := uint32(1)
	outScratch := make([]uint64, 0, len(newAlphabet))
	mergedResolver := func(observation TObservation) []uint64 {
		idsA := nfaA.nondeterministicResolver(observation)
		idsB := nfaB.nondeterministicResolver(observation)
		if len(idsA) == 0 && len(idsB) == 0 {
			return nil
		}
		currentEpoch++
		if currentEpoch == 0 {
			for i := range seenEpoch {
				seenEpoch[i] = 0
			}
			currentEpoch = 1
		}
		outScratch = outScratch[:0]

		for _, id := range idsA {
			merged := remapA[id]
			if seenEpoch[merged] == currentEpoch {
				continue
			}
			seenEpoch[merged] = currentEpoch
			outScratch = append(outScratch, merged)
		}

		for _, id := range idsB {
			merged := remapB[id]
			if seenEpoch[merged] == currentEpoch {
				continue
			}
			seenEpoch[merged] = currentEpoch
			outScratch = append(outScratch, merged)
		}

		return outScratch
	}

	// ------------------------------------------------------------
	// 2. Build merged state space
	// ------------------------------------------------------------

	offsetA := uint64(1)
	offsetB := offsetA + nfaA.numStates

	newNumStates := nfaA.numStates + nfaB.numStates + 1

	newOutcomes := make([]TStateOutcome, newNumStates)

	outA := memstruct.ArrayCursorCreate[TStateOutcome](nfaA.outcomes)
	for i := uint64(0); i < nfaA.numStates; i++ {
		newOutcomes[i+offsetA] = *outA.PtrAt(i)
	}

	outB := memstruct.ArrayCursorCreate[TStateOutcome](nfaB.outcomes)
	for i := uint64(0); i < nfaB.numStates; i++ {
		newOutcomes[i+offsetB] = *outB.PtrAt(i)
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
