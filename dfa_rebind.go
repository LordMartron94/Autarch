package autarch

import (
	"memarch"
	"memstruct"
)

/*
DFARebindAlphabet rebuilds dfa on mergedAlphabet using remap[oldSymbolID]=newSymbolID.
Missing symbols in the merged alphabet become DeadState transitions. The DFA must be complete.
*/
func DFARebindAlphabet[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	mergedAlphabet []SymbolDefinition[TObservation],
	remap map[uint64]uint64,
	resolver DeterministicSymbolResolver[TObservation],
	allocFn memarch.AllocationFn,
) *DFA[TObservation, TStateOutcome] {
	if dfa == nil {
		panic("DFARebindAlphabet: nil dfa")
	}

	newSize := uint64(len(mergedAlphabet))
	outcomes := make([]TStateOutcome, dfa.numStates)
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](dfa.outcomes)
	for s := uint64(0); s < dfa.numStates; s++ {
		outcomes[s] = *outCur.PtrAt(s)
	}

	transitions := make([]Transition[TObservation], 0, dfa.numStates*newSize)
	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)

	for state := uint64(0); state < dfa.numStates; state++ {
		rowStart := state * dfa.alphabetSize
		for symID := uint64(0); symID < newSize; symID++ {
			target := DeadState
			for oldID, newID := range remap {
				if newID != symID || oldID >= dfa.alphabetSize {
					continue
				}
				oldTarget := *transCur.PtrAt(rowStart + oldID)
				if oldTarget != DeadState {
					target = oldTarget
				}
				break
			}
			transitions = append(transitions, Transition[TObservation]{
				CurrentState: state,
				NextState:    target,
				Symbol:       SymbolCreate[TObservation](mergedAlphabet[symID].Name, symID),
			})
		}
	}

	return DFACreate(allocFn, mergedAlphabet, transitions, outcomes, resolver)
}
