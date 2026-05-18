package autarch

import (
	"memarch"
	"memstruct"
)

type dfaProductKey struct {
	a, b uint64
}

/*
DFAIntersect builds a product DFA recognizing L(a) ∩ L(b).

Both inputs must be complete DFAs sharing observation type. Alphabets are merged by symbol Name.
Product state accepts when isAccept holds for both component outcomes.
*/
func DFAIntersect[TObservation, TStateOutcome comparable](
	a, b *DFA[TObservation, TStateOutcome],
	isAccept func(TStateOutcome) bool,
	acceptOut, rejectOut TStateOutcome,
	resolver DeterministicSymbolResolver[TObservation],
	allocFn memarch.AllocationFn,
) *DFA[TObservation, TStateOutcome] {
	if a == nil || b == nil {
		panic("DFAIntersect: nil operand")
	}

	merge := MergeAlphabets(a.alphabet, b.alphabet)
	aRe := DFARebindAlphabet(a, merge.Alphabet, merge.Remaps[0], resolver, allocFn)
	bRe := DFARebindAlphabet(b, merge.Alphabet, merge.Remaps[1], resolver, allocFn)

	alphabet := merge.Alphabet
	alphabetSize := uint64(len(alphabet))

	registry := make(map[dfaProductKey]uint64)
	var outcomes []TStateOutcome
	transitions := make([]Transition[TObservation], 0)

	outCurA := memstruct.ArrayCursorCreate[TStateOutcome](aRe.outcomes)
	outCurB := memstruct.ArrayCursorCreate[TStateOutcome](bRe.outcomes)

	getID := func(pa, pb uint64) uint64 {
		key := dfaProductKey{a: pa, b: pb}
		if id, ok := registry[key]; ok {
			return id
		}
		id := uint64(len(registry))
		registry[key] = id

		outA := *outCurA.PtrAt(pa)
		outB := *outCurB.PtrAt(pb)
		if isAccept(outA) && isAccept(outB) {
			outcomes = append(outcomes, acceptOut)
		} else {
			outcomes = append(outcomes, rejectOut)
		}
		return id
	}

	work := []dfaProductKey{{0, 0}}
	seen := map[dfaProductKey]bool{{0, 0}: true}
	transA := memstruct.ArrayCursorCreate[uint64](aRe.transitions)
	transB := memstruct.ArrayCursorCreate[uint64](bRe.transitions)

	for len(work) > 0 {
		cur := work[0]
		work = work[1:]
		curID := getID(cur.a, cur.b)

		for symID := uint64(0); symID < alphabetSize; symID++ {
			idxA := getTransitionIDX(alphabetSize, cur.a, symID)
			idxB := getTransitionIDX(alphabetSize, cur.b, symID)
			na := *transA.PtrAt(idxA)
			nb := *transB.PtrAt(idxB)

			if na == DeadState || nb == DeadState {
				transitions = append(transitions, Transition[TObservation]{
					CurrentState: curID,
					NextState:    DeadState,
					Symbol:       SymbolCreate[TObservation](alphabet[symID].Name, symID),
				})
				continue
			}

			nextKey := dfaProductKey{a: na, b: nb}
			nextID := getID(na, nb)
			if !seen[nextKey] {
				seen[nextKey] = true
				work = append(work, nextKey)
			}

			transitions = append(transitions, Transition[TObservation]{
				CurrentState: curID,
				NextState:    nextID,
				Symbol:       SymbolCreate[TObservation](alphabet[symID].Name, symID),
			})
		}
	}

	if len(outcomes) == 0 {
		outcomes = append(outcomes, rejectOut)
	}

	return DFACreate(allocFn, alphabet, transitions, outcomes, resolver)
}
