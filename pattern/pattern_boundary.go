package pattern

import (
	"autarch"
	"foundation/bytes"
	"foundation/domain"
	"memarch"
	"memcore"
	"memforge"
)

func boundaryCompileAlloc() memarch.AllocationFn {
	allocator := memforge.DynamicLinearAllocatorCreateFunction(uint64(64*memcore.KiloByte), func(currentCap, neededCap uint64) uint64 {
		newSize := currentCap * 2
		if newSize < neededCap {
			newSize = neededCap
		}
		if newSize > uint64(64*memcore.MegaByte) {
			panic("pattern boundary compile: temp allocator cap exceeded")
		}
		return newSize
	})
	return func(size, align uint64) memcore.MarkRaw {
		return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, size, align)
	}
}

func boundaryOutcomeResolution(
	states []uint64,
	outcomes []AnnotatedOutcome[autarch.PatternAcceptOutcome],
) (AnnotatedOutcome[autarch.PatternAcceptOutcome], bool) {
	var best AnnotatedOutcome[autarch.PatternAcceptOutcome]
	found := false
	bestState := ^uint64(0)

	for i, out := range outcomes {
		if !autarch.PatternAcceptOutcomeIsAccept(out.Value) {
			continue
		}
		if !found || states[i] < bestState {
			best = out
			bestState = states[i]
			found = true
		}
	}
	return best, found
}

/*
RegulaCompileToDFA compiles a single Regula pattern to a minimized boolean DFA.
*/
func RegulaCompileToDFA(
	ast RegulaAST[rune],
) (*autarch.DFA[rune, AnnotatedOutcome[autarch.PatternAcceptOutcome]], error) {
	ctx := CreateSharedCompilationContext[rune, RegulaAST[rune]](
		domain.DiscreteDomainRuneCreate(),
		ObservationFormatter[rune]{
			ToBytes: func(observations []rune) []byte {
				return bytes.StringToBytes(string(observations))
			},
		},
	)

	accept := autarch.PatternAcceptOutcome{Accept: true}
	reject := autarch.PatternAcceptOutcome{Accept: false}

	instructions := []PatternCompilationInstruction[rune, autarch.PatternAcceptOutcome, RegulaAST[rune]]{
		{Pattern: &ast, Outcome: accept},
	}

	nfas, err := RegulaCompileToNFAThompson(boundaryCompileAlloc(), instructions, ctx, reject)
	if err != nil {
		return nil, err
	}
	if len(nfas) == 0 || nfas[0] == nil {
		return nil, err
	}

	resolver := SharedCompilationContextDeterministicResolverGet(ctx)
	dfa := autarch.NFAToDFA(
		nfas[0],
		256*memcore.KiloByte,
		4*memcore.MegaByte,
		boundaryCompileAlloc(),
		resolver,
		boundaryOutcomeResolution,
	)

	return autarch.DFAMinimize(
		dfa,
		boundaryCompileAlloc(),
		256*memcore.KiloByte,
		4*memcore.MegaByte,
		func(o AnnotatedOutcome[autarch.PatternAcceptOutcome]) AnnotatedOutcome[autarch.PatternAcceptOutcome] {
			return o
		},
	), nil
}

/*
PatternLiteralBoundaryChars returns the first-character continuation set after literal
for strings in L(ast) strictly longer than literal. Empty ok means no competing extension.
*/
func PatternLiteralBoundaryChars(literal string, ast RegulaAST[rune]) (PatternAlphabet[rune], bool) {
	dfa, err := RegulaCompileToDFA(ast)
	if err != nil {
		return PatternAlphabet[rune]{}, false
	}

	prefix := []rune(literal)
	ranges := autarch.DFAContinuationAfterPrefix(
		dfa,
		prefix,
		func(o AnnotatedOutcome[autarch.PatternAcceptOutcome]) bool {
			return autarch.PatternAcceptOutcomeIsAccept(o.Value)
		},
	)
	if len(ranges) == 0 {
		return PatternAlphabet[rune]{}, false
	}

	cr := make([]CharRange[rune], len(ranges))
	for i, r := range ranges {
		cr[i] = CharRange[rune]{Lo: r.Lo, Hi: r.Hi}
	}
	order := func(a, b rune) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
	normalized := normalizeRanges(cr, order)
	return PatternAlphabet[rune]{ranges: normalized}, true
}
