package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memforge"
	"memstruct"
)

/*
DFAMinimize reduces a DFA to its minimal equivalent form using Hopcroft's algorithm.

The algorithm partitions states into equivalence classes based on their
behavior (transitions and acceptance/outcome), then merges equivalent states to create
the smallest DFA recognizing the same language.

Use cases:
- Reducing DFA size for memory efficiency
- Optimizing automata after NFA-to-DFA conversion
- Creating canonical representations of regular languages

Time complexity: O(n log n) where n is number of states
Space complexity: O(n * a) where n is states, a is alphabet size

Prerequisites:
- dfa must be a valid DFA instance
- minTempAllocatorMemory and maxTempAllocatorMemory must be sufficient
- dfaAllocationFn must provide memory for the minimized DFA

Edge cases:
- Panics if temporary allocator exceeds maxTempAllocatorMemory
- Already-minimal DFAs return equivalent but new instances
- Dead states are preserved if reachable
- Start state (0) is preserved in minimized DFA

Outcome semantics:
- Acceptance is tracked explicitly via a boolean accepting set
- Outcomes are only meaningful for accepting states
- Lexer error handling / fallback behavior is delegated to higher-level execution layers
*/
func DFAMinimize[TObservation any, TStateOutcome comparable](
	dfa *DFA[TObservation, TStateOutcome],
	dfaAllocationFn memarch.AllocationFn,
	minTempAllocatorMemory, maxTempAllocatorMemory memcore.MemoryUnitBytes,
) *DFA[TObservation, TStateOutcome] {

	// ───────────────────────────────────────────────────────────────
	// Temporary allocator
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
	accArray := DFAAcceptingGet(dfa) // Array[bool]
	outArray := DFAOutcomesGet(dfa)  // Array[TStateOutcome]
	numStates := memstruct.ArrayCapacityGet[bool](accArray)
	if numStates == 0 {
		panic("DFAMinimize: DFA has no states")
	}

	alphabet := DFAAlphabetGet(dfa)
	alphabetSize := len(alphabet)
	indexer := DFAIndexerGet(dfa)

	// Predecessor sets
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
	// Initial partition P:
	// - all non-accepting states in one block
	// - accepting states grouped by outcome value
	// ───────────────────────────────────────────────────────────────
	var nonAccepting dfaStateSubset

	acceptGroups := make(map[TStateOutcome]*dfaStateSubset)

	accCur := memstruct.ArrayCursorCreate[bool](accArray)
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](outArray)

	for s := uint64(0); s < numStates; s++ {
		if !*accCur.PtrAt(s) {
			nonAccepting.Add(s)
			continue
		}

		out := *outCur.PtrAt(s)
		g, ok := acceptGroups[out]
		if !ok {
			tmp := &dfaStateSubset{}
			acceptGroups[out] = tmp
			g = tmp
		}
		g.Add(s)
	}

	P := make([]dfaStateSubset, 0, 1+len(acceptGroups))
	if nonAccepting.Count() > 0 {
		P = append(P, nonAccepting)
	}
	for _, bs := range acceptGroups {
		P = append(P, *bs)
	}

	// ───────────────────────────────────────────────────────────────
	// Worklist
	// ───────────────────────────────────────────────────────────────
	worklist, _ := memarch.MemArchQueueCreate[uint64](
		func(sizeBytes, align uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, align)
		},
		numStates,
	)

	inW := make(map[uint64]bool)

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

				if Yint.Count() == 0 || Ydiff.Count() == 0 {
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

	// Acceptance/outcome per block:
	// Because we started by grouping accepting states by outcome, each final block is homogeneous.
	minAccepting := make([]bool, blockCount)
	minOutcomes := make([]TStateOutcome, blockCount)

	for bid := range P {
		sts := P[bid].States()
		if len(sts) == 0 {
			panic(fmt.Errorf("DFAMinimize: empty block at %d", bid))
		}

		rep := sts[0]
		if *accCur.PtrAt(rep) {
			minAccepting[bid] = true
			minOutcomes[bid] = *outCur.PtrAt(rep)
		} else {
			minAccepting[bid] = false
			var zero TStateOutcome
			minOutcomes[bid] = zero
		}
	}

	// Build transitions
	minTransitions := make([]Transition[TObservation], 0, blockCount*uint64(alphabetSize))

	for bid, B := range P {
		sts := B.States()
		if len(sts) == 0 {
			panic(fmt.Errorf("DFAMinimize: empty block at %v", bid))
		}

		representative := sts[0]

		for symbolID := uint64(0); symbolID < uint64(alphabetSize); symbolID++ {
			symDef := alphabet[symbolID]
			symbol := SymbolCreate[TObservation](symDef.Name, symbolID)

			transitionIDX := getTransitionIDX(dfa.alphabetSize, representative, symbolID)
			nextState := memstruct.ArrayItemGetAtUnsafe[uint64](dfa.transitions, transitionIDX)

			minTransitions = append(minTransitions, Transition[TObservation]{
				CurrentState: uint64(bid),
				Symbol:       symbol,
				NextState:    stateToBlock[nextState],
			})
		}
	}

	return DFACreate(
		dfaAllocationFn,
		alphabet,
		minTransitions,
		minAccepting,
		minOutcomes,
		indexer,
	)
}
