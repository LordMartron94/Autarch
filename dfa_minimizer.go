package autarch

import (
	"memarch"
	"memcore"
	"memforge"
	"memstruct"
)

func DFAMinimize[TObservation, TStateOutcome any, TKey comparable](
	dfa *DFA[TObservation, TStateOutcome],
	dfaAllocationFn memarch.AllocationFn,
	minTemp, maxTemp memcore.MemoryUnitBytes,
	outcomeKeyFn func(out TStateOutcome) TKey,
) *DFA[TObservation, TStateOutcome] {

	allocator := createTempAllocator(minTemp, maxTemp)
	defer memforge.DynamicLinearAllocatorDestroy(allocator)

	outArray := DFAOutcomesGet(dfa)
	numStates := memstruct.ArrayCapacityGet[TStateOutcome](outArray)
	if numStates == 0 {
		return dfa // Nothing to minimize
	}

	alphabet := DFAAlphabetGet(dfa)
	alphabetSize := len(alphabet)
	predecessorSets := DFAPredecessorSets(dfa)

	// 1. Build predecessor bitsets (Dynamically sized)
	predBit := make([][]dfaStateSubset, alphabetSize)
	for a := 0; a < alphabetSize; a++ {
		predBit[a] = make([]dfaStateSubset, numStates)
		for q := uint64(0); q < numStates; q++ {
			// Initialize with the correct state count to avoid out-of-bounds
			bs := *dfaStateSubsetCreate(numStates, predecessorSets[a][q])
			predBit[a][q] = bs
		}
	}

	// 2. Initial partition P: by outcome key (every state has an outcome)
	outcomeGroups := make(map[TKey]*dfaStateSubset)
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](outArray)

	for s := uint64(0); s < numStates; s++ {
		key := outcomeKeyFn(*outCur.PtrAt(s))

		bs, ok := outcomeGroups[key]
		if !ok {
			bs = dfaStateSubsetCreate(numStates, nil)
			outcomeGroups[key] = bs
		}
		bs.Add(s)
	}

	P := make([]dfaStateSubset, 0, len(outcomeGroups))
	for _, bs := range outcomeGroups {
		P = append(P, *bs)
	}

	// 3. Hopcroft worklist (Queue of Block IDs)
	worklist, _ := memarch.MemArchQueueCreate[uint64](
		func(sz, al uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sz, al)
		},
		numStates+uint64(len(P)),
	)

	inW := make(map[uint64]bool)
	for i := range P {
		memstruct.QueuePushUnsafe(worklist, uint64(i))
		inW[uint64(i)] = true
	}

	// 4. Refinement Loop
	for !memstruct.QueueIsEmpty[uint64](worklist) {
		Aid := memstruct.QueuePopUnsafe[uint64](worklist)
		delete(inW, Aid)
		A := P[Aid]

		for a := 0; a < alphabetSize; a++ {
			// X = {p | δ(p,a) ∈ A}
			X := *dfaStateSubsetCreate(numStates, nil)
			for _, target := range A.States() {
				X.Union(&X, &predBit[a][target])
			}

			if X.Count() == 0 {
				continue
			}

			// Refine existing partitions
			for i := 0; i < len(P); i++ {
				Y := &P[i]
				var Yint, Ydiff dfaStateSubset
				Yint = *dfaStateSubsetCreate(numStates, nil)
				Ydiff = *dfaStateSubsetCreate(numStates, nil)

				Yint.Intersect(Y, &X)
				if Yint.Count() == 0 || Yint.Count() == Y.Count() {
					continue // No split possible
				}

				Ydiff.Difference(Y, &X)

				// Y is split into Yint and Ydiff
				*Y = Yint
				newIdx := uint64(len(P))
				P = append(P, Ydiff)

				if inW[uint64(i)] {
					memstruct.QueuePushUnsafe(worklist, newIdx)
					inW[newIdx] = true
				} else {
					if Yint.Count() <= Ydiff.Count() {
						memstruct.QueuePushUnsafe(worklist, uint64(i))
						inW[uint64(i)] = true
					} else {
						memstruct.QueuePushUnsafe(worklist, newIdx)
						inW[newIdx] = true
					}
				}
			}
		}
	}

	return buildMinimizedDFA[TObservation, TStateOutcome](
		dfa,
		dfaAllocationFn,
		P,
		alphabet,
		dfa.deterministicResolver,
		outCur,
	)
}

func buildMinimizedDFA[TO any, TR any](
	oldDFA *DFA[TO, TR],
	alloc memarch.AllocationFn,
	P []dfaStateSubset,
	alphabet []SymbolDefinition[TO],
	resolver DeterministicSymbolResolver[TO],
	outCur memstruct.ArrayCursor[TR],
) *DFA[TO, TR] {
	// Canonicalize: Start state (0) must be Block 0
	for i := range P {
		if P[i].Has(0) {
			P[0], P[i] = P[i], P[0]
			break
		}
	}

	numStates := memstruct.ArrayCapacityGet[TR](oldDFA.outcomes)
	stateToBlock := make([]uint64, numStates)
	for bid, B := range P {
		for _, s := range B.States() {
			stateToBlock[s] = uint64(bid)
		}
	}

	blockCount := uint64(len(P))
	minOutcomes := make([]TR, blockCount)
	minTransitions := make([]Transition[TO], 0, blockCount*uint64(len(alphabet)))

	for bid, B := range P {
		rep := B.States()[0]
		minOutcomes[bid] = *outCur.PtrAt(rep)

		for symID := uint64(0); symID < uint64(len(alphabet)); symID++ {
			oldIdx := getTransitionIDX(uint64(len(alphabet)), rep, symID)
			oldTarget := memstruct.ArrayItemGetAtUnsafe[uint64](oldDFA.transitions, oldIdx)
			newTarget := DeadState
			if oldTarget != DeadState {
				newTarget = stateToBlock[oldTarget]
			}

			minTransitions = append(minTransitions, Transition[TO]{
				CurrentState: uint64(bid),
				Symbol:       SymbolCreate[TO](alphabet[symID].Name, symID),
				NextState:    newTarget,
			})
		}
	}

	return DFACreate(alloc, alphabet, minTransitions, minOutcomes, resolver)
}
