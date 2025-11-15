package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memforge"
	"memstruct"
)

// DFAMinimize applies Hopcroft's DFA minimization using bitsets.
// It operates in O(n log n) time complexity.
func DFAMinimize[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
	dfaAllocationFn memarch.AllocationFn,
	minTempAllocatorMemory, maxTempAllocatorMemory memcore.MemoryUnitBytes,
	invalidOutcome TStateOutcome,
) *DFA[TObservation, TStateOutcome] {

	// ───────────────────────────────────────────────────────────────
	// Temporary allocator (exactly as in NFAToDFA)
	// ───────────────────────────────────────────────────────────────
	allocator := memforge.DynamicLinearAllocatorCreateFunction(
		uint64(minTempAllocatorMemory),
		func(currentCap, neededCap uint64) uint64 {
			newSize := currentCap * 2
			if newSize < neededCap {
				newSize = neededCap
			}
			if newSize > uint64(maxTempAllocatorMemory) {
				panic(fmt.Errorf("DFAMinimize: temp allocator exceeded: need=%v max=%v", newSize, maxTempAllocatorMemory))
			}
			return newSize
		},
	)
	defer memforge.DynamicLinearAllocatorDestroy(allocator)

	// ───────────────────────────────────────────────────────────────
	// Load DFA data
	// ───────────────────────────────────────────────────────────────
	stateArray := DFAStatesGet(dfa)
	numStates := memstruct.ArrayCapacityGet[TStateOutcome](stateArray)
	if numStates == 0 {
		panic("DFAMinimize: DFA has no states")
	}

	alphabet := DFAAlphabetGet(dfa)
	alphabetSize := len(alphabet)
	indexer := DFAIndexerGet(dfa)

	// Predecessor sets (slice-of-slices-of-slices)
	predecessorSets := DFAPredecessorSets(dfa)

	// ───────────────────────────────────────────────────────────────
	// Build predecessor bitsets: predBit[a][q] = bitset of all p with δ(p,a)=q
	// ───────────────────────────────────────────────────────────────
	predBit := make([][]dfaStateSubset, alphabetSize)
	for a := 0; a < alphabetSize; a++ {
		predBit[a] = make([]dfaStateSubset, numStates)
		for q := uint64(0); q < numStates; q++ {
			var bs dfaStateSubset
			for _, p := range predecessorSets[a][q] {
				bs.Add(p)
			}
			predBit[a][q] = bs
		}
	}

	// ───────────────────────────────────────────────────────────────
	// Initial partition P: group states by outcome
	// ───────────────────────────────────────────────────────────────
	groups := make(map[TStateOutcome]*dfaStateSubset)

	for s := uint64(0); s < numStates; s++ {
		out := memstruct.ArrayItemGetAtUnsafe[TStateOutcome](stateArray, s)
		g, ok := groups[out]
		if !ok {
			tmp := &dfaStateSubset{}
			groups[out] = tmp
			g = tmp
		}
		g.Add(s)
	}

	// Convert groups into slice of blocks P
	P := make([]dfaStateSubset, 0, len(groups))
	for _, bs := range groups {
		P = append(P, *bs)
	}

	// ───────────────────────────────────────────────────────────────
	// Worklist: Queue[uint64] of block IDs
	// Membership: map[uint64]bool
	// ───────────────────────────────────────────────────────────────
	// Max blocks ≤ numStates initially
	worklist, _ := memarch.MemArchQueueCreate[uint64](
		func(sizeBytes, align uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, align)
		},
		numStates,
	)

	inW := make(map[uint64]bool)

	// Enqueue all initial blocks
	for bid := uint64(0); bid < uint64(len(P)); bid++ {
		memstruct.QueuePushUnsafe(worklist, bid)
		inW[bid] = true
	}

	// ───────────────────────────────────────────────────────────────
	// Hopcroft refinement loop
	// ───────────────────────────────────────────────────────────────
	for !memstruct.QueueIsEmpty[uint64](worklist) {

		Aid := memstruct.QueuePopUnsafe[uint64](worklist)
		delete(inW, Aid)

		A := &P[Aid]

		for a := 0; a < alphabetSize; a++ {
			var X dfaStateSubset

			for _, target := range A.States() {
				for i := 0; i < int(wordsPerSubset); i++ {
					X.words[i] |= predBit[a][target].words[i]
				}
			}

			if X.Count() == 0 {
				continue
			}

			originalPCount := len(P)

			for Yid := 0; Yid < originalPCount; Yid++ {

				Y := &P[Yid]

				var Yint, Ydiff dfaStateSubset
				Yint.Intersect(Y, &X)
				Ydiff.Difference(Y, &X)

				intEmpty := (Yint.Count() == 0)
				diffEmpty := (Ydiff.Count() == 0)

				if intEmpty || diffEmpty {
					continue
				}

				P[Yid] = Yint
				newYid := len(P)
				P = append(P, Ydiff)

				if inW[uint64(Yid)] {
					delete(inW, uint64(Yid))

					memstruct.QueuePushUnsafe(worklist, uint64(Yid))
					memstruct.QueuePushUnsafe(worklist, uint64(newYid))
					inW[uint64(Yid)] = true
					inW[uint64(newYid)] = true

				} else {
					if Yint.Count() <= Ydiff.Count() {
						memstruct.QueuePushUnsafe(worklist, uint64(Yid))
						inW[uint64(Yid)] = true
					} else {
						memstruct.QueuePushUnsafe(worklist, uint64(newYid))
						inW[uint64(newYid)] = true
					}
				}
			}
		}
	}

	// ───────────────────────────────────────────────────────────────
	// Build minimized DFA
	// ───────────────────────────────────────────────────────────────
	startBlockIdx := -1
	for i := range P {
		if P[i].Has(0) {
			startBlockIdx = i
			break
		}
	}
	if startBlockIdx == -1 {
		panic("DFAMinimize: could not find original start state 0 in any block")
	}

	// Swap the start block to be P[0]
	if startBlockIdx != 0 {
		P[0], P[startBlockIdx] = P[startBlockIdx], P[0]
	}

	blockCount := uint64(len(P))
	stateToBlock := make([]uint64, numStates)

	for bid, B := range P {
		for _, s := range B.States() {
			stateToBlock[s] = uint64(bid)
		}
	}

	// Outcomes per block
	minOut := make([]TStateOutcome, blockCount)
	for bid := range P {
		out := determineOutcome[TStateOutcome](&P[bid], stateArray, invalidOutcome)
		minOut[bid] = out
	}

	// Build transitions
	minTransitions := make([]Transition[TObservation], 0, blockCount*uint64(alphabetSize))

	cursor := DFACursorGet(dfa)
	for bid, B := range P {
		sts := B.States()
		if len(sts) == 0 {
			panic(fmt.Errorf("DFAMinimize: empty block at %v", bid))
		}

		representative := sts[0]

		for _, obs := range alphabet {
			sym, ok := indexer(obs)
			if !ok {
				panic("DFAMinimize: symbol outside DFA alphabet")
			}

			next := DFATransition(dfa, obs, representative, cursor)
			minTransitions = append(minTransitions, Transition[TObservation]{
				CurrentState: uint64(bid),
				Symbol:       sym,
				NextState:    stateToBlock[next],
			})
		}
	}

	// Construct minimal DFA
	return DFACreate(
		dfaAllocationFn,
		alphabet,
		minTransitions,
		minOut,
		indexer,
	)
}
