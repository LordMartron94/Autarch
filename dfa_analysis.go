package autarch

import (
	"cmp"
	"memstruct"
	"slices"
)

/*
ObservationRange is an inclusive character interval used by DFA analysis helpers.
*/
type ObservationRange[TObservation any] struct {
	Lo TObservation
	Hi TObservation
}

/*
DFACoaccessibleFromAccept returns a bitset of states that can reach an accept state (inclusive).
*/
func DFACoaccessibleFromAccept[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	isAccept func(TStateOutcome) bool,
) []bool {
	n := dfa.numStates
	coaccess := make([]bool, n)
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](dfa.outcomes)
	preds := DFAPredecessorSets(dfa)
	stack := make([]uint64, 0, n)

	for s := uint64(0); s < n; s++ {
		if isAccept(*outCur.PtrAt(s)) {
			coaccess[s] = true
			stack = append(stack, s)
		}
	}

	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for symID := uint64(0); symID < dfa.alphabetSize; symID++ {
			for _, pred := range preds[symID][s] {
				if !coaccess[pred] {
					coaccess[pred] = true
					stack = append(stack, pred)
				}
			}
		}
	}

	return coaccess
}

/*
DFAContinuationAfterPrefix returns character ranges for observations that may appear immediately
after prefix while still allowing a match strictly longer than prefix in L(dfa).
*/
func DFAContinuationAfterPrefix[TObservation cmp.Ordered, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	prefix []TObservation,
	isAccept func(TStateOutcome) bool,
) []ObservationRange[TObservation] {
	state, ok := dfaWalkPrefix(dfa, prefix)
	if !ok {
		return nil
	}

	coaccess := DFACoaccessibleFromAccept(dfa, isAccept)
	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)
	rowStart := state * dfa.alphabetSize

	var ranges []ObservationRange[TObservation]
	for symID := uint64(0); symID < dfa.alphabetSize; symID++ {
		target := *transCur.PtrAt(rowStart + symID)
		if target == DeadState || !coaccess[target] {
			continue
		}
		ranges = append(ranges, symbolDefinitionRanges(dfa.alphabet[symID])...)
	}

	return normalizeContinuationRanges(ranges)
}

func dfaWalkPrefix[TObservation, TStateOutcome any](
	dfa *DFA[TObservation, TStateOutcome],
	prefix []TObservation,
) (uint64, bool) {
	state := StartStateID
	transCur := memstruct.ArrayCursorCreate[uint64](dfa.transitions)

	for _, obs := range prefix {
		symbolID, ok := dfa.deterministicResolver(obs)
		if !ok {
			return 0, false
		}
		idx := getTransitionIDX(dfa.alphabetSize, state, symbolID)
		state = *transCur.PtrAt(idx)
		if state == DeadState {
			return 0, false
		}
	}
	return state, true
}

func symbolDefinitionRanges[TObservation any](sym SymbolDefinition[TObservation]) []ObservationRange[TObservation] {
	if sym.Observation != nil {
		v := *sym.Observation
		return []ObservationRange[TObservation]{{Lo: v, Hi: v}}
	}
	if sym.GapLo != nil && sym.GapHi != nil {
		return []ObservationRange[TObservation]{{Lo: *sym.GapLo, Hi: *sym.GapHi}}
	}
	return nil
}

func normalizeContinuationRanges[TObservation cmp.Ordered](ranges []ObservationRange[TObservation]) []ObservationRange[TObservation] {
	if len(ranges) == 0 {
		return ranges
	}
	order := continuationOrdering[TObservation]()
	slices.SortFunc(ranges, func(a, b ObservationRange[TObservation]) int {
		if c := order(a.Lo, b.Lo); c != 0 {
			return c
		}
		return order(a.Hi, b.Hi)
	})
	out := make([]ObservationRange[TObservation], 0, len(ranges))
	cur := ranges[0]
	for i := 1; i < len(ranges); i++ {
		r := ranges[i]
		if order(r.Lo, cur.Hi) <= 0 {
			if order(r.Hi, cur.Hi) > 0 {
				cur.Hi = r.Hi
			}
			continue
		}
		out = append(out, cur)
		cur = r
	}
	out = append(out, cur)
	return out
}

func continuationOrdering[TObservation cmp.Ordered]() func(a, b TObservation) int {
	var zero TObservation
	switch any(zero).(type) {
	case rune:
		return func(a, b TObservation) int {
			return cmp.Compare(any(a).(rune), any(b).(rune))
		}
	case byte:
		return func(a, b TObservation) int {
			return cmp.Compare(any(a).(byte), any(b).(byte))
		}
	default:
		return cmp.Compare[TObservation]
	}
}
