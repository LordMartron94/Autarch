package pattern

import (
	"autarch"
	foundationtesting "foundation/testing"
	"memcore"
	"memforge"
	"testing"
)

func vistraRuneCmp(a, b rune) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func vistraTestAllocators(t *testing.T) (
	allocFn func(uint64, uint64) memcore.MarkRaw,
	mainAlloc memcore.MarkRaw,
	scratchAlloc memcore.MarkRaw,
) {
	mainAlloc = memforge.DynamicLinearAllocatorCreateFunction(
		1024,
		func(currentCap, neededCap uint64) uint64 {
			if neededCap > currentCap {
				return neededCap * 2
			}
			return currentCap
		},
	)
	allocFn = func(sizeBytes, alignment uint64) memcore.MarkRaw {
		return memforge.DynamicLinearAllocatorMallocUnsafe(mainAlloc, sizeBytes, alignment)
	}
	scratchAlloc = memforge.DynamicLinearAllocatorCreateFunction(
		1024,
		func(currentCap, neededCap uint64) uint64 {
			if neededCap > currentCap {
				return neededCap * 2
			}
			return currentCap
		},
	)
	return allocFn, mainAlloc, scratchAlloc
}

/*
runVistraDPDAAccept compiles instructions (single DPDA expected), runs on input, and asserts
accept and stack depth 1. Uses rune alphabet and string outcomes.
*/
func runVistraDPDAAccept(
	t *testing.T,
	allocFn func(uint64, uint64) memcore.MarkRaw,
	scratchAlloc memcore.MarkRaw,
	instructions []PatternCompilationInstruction[rune, string, VistraAST[rune]],
	input []rune,
	isAccepting func(AnnotatedOutcome[string]) bool,
) {
	t.Helper()
	formatter := ObservationFormatter[rune]{
		ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
	}
	ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
	dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, instructions, ctx, "")
	if err != nil {
		t.Fatalf("VistraCompileToDPDA: %v", err)
	}
	foundationtesting.Assert(len(dpdas) == 1, "expected 1 DPDA", "one DPDA", t)
	dpda := dpdas[0]
	scratchAllocFn := func(sizeBytes, alignment uint64) memcore.MarkRaw {
		return memforge.DynamicLinearAllocatorMallocUnsafe(scratchAlloc, sizeBytes, alignment)
	}
	state := autarch.DPDAStateCreate(dpda, scratchAllocFn, 8)
	accepted, err := autarch.DPDARunAndAccept(dpda, 0, state, input, isAccepting)
	if err != nil {
		t.Fatalf("DPDARunAndAccept: %v", err)
	}
	foundationtesting.Assert(accepted, "expected accept for input", "accept", t)
	foundationtesting.Assert(
		autarch.DPDAIsAcceptingStateAndStackDepthOne(dpda, state, isAccepting),
		"expected accepting state and stack depth 1",
		"accepting state and stack depth 1",
		t,
	)
}

/*
runVistraDPDAReject compiles instructions (single DPDA expected), runs on input, and asserts
the input is not accepted. If the run returns an error (e.g. invalid symbol), that is treated as rejection.
*/
func runVistraDPDAReject(
	t *testing.T,
	allocFn func(uint64, uint64) memcore.MarkRaw,
	scratchAlloc memcore.MarkRaw,
	instructions []PatternCompilationInstruction[rune, string, VistraAST[rune]],
	input []rune,
) {
	t.Helper()
	formatter := ObservationFormatter[rune]{
		ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
	}
	ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
	dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, instructions, ctx, "")
	if err != nil {
		t.Fatalf("VistraCompileToDPDA: %v", err)
	}
	foundationtesting.Assert(len(dpdas) == 1, "expected 1 DPDA", "one DPDA", t)
	dpda := dpdas[0]
	scratchAllocFn := func(sizeBytes, alignment uint64) memcore.MarkRaw {
		return memforge.DynamicLinearAllocatorMallocUnsafe(scratchAlloc, sizeBytes, alignment)
	}
	state := autarch.DPDAStateCreate(dpda, scratchAllocFn, 8)
	accepted, runErr := autarch.DPDARunAndAccept(dpda, 0, state, input, func(o AnnotatedOutcome[string]) bool { return o.Value != "" })
	if runErr != nil {
		return
	}
	acceptingAndStackOne := autarch.DPDAIsAcceptingStateAndStackDepthOne(dpda, state, func(o AnnotatedOutcome[string]) bool { return o.Value != "" })
	foundationtesting.Assert(!accepted || !acceptingAndStackOne, "expected reject for input", "reject", t)
}

// RunVistraDPDACompilerTests runs the Vistra DPDA compiler test suite.
// It is exported so external test runners (e.g. tests/entry_test.go) can invoke it
// via testWithTempOkMessages(pattern.RunVistraDPDACompilerTests, t).
func RunVistraDPDACompilerTests(t *testing.T) {
	allocFn, mainAlloc, scratchAlloc := vistraTestAllocators(t)
	defer memforge.DynamicLinearAllocatorDestroy(mainAlloc)
	defer memforge.DynamicLinearAllocatorDestroy(scratchAlloc)

	fac := VistraASTFactoryCreate(vistraRuneCmp)
	acceptAny := func(o AnnotatedOutcome[string]) bool { return o.Value != "" }

	t.Run("Literal", func(t *testing.T) {
		pat := fac.Literal('a', 'b')
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "ab"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'a', 'b'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'b', 'a'})
	})

	t.Run("Nest", func(t *testing.T) {
		inner := fac.Literal('a')
		nested := fac.Nest([]rune{'('}, []rune{')'}, inner)
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &nested, Outcome: "paren_a"},
		}
		isParenA := func(o AnnotatedOutcome[string]) bool { return o.Value == "paren_a" }
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'(', 'a', ')'}, isParenA)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'(', 'a'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a', ')'})
	})

	t.Run("NestedNesting", func(t *testing.T) {
		inner := fac.Literal('a')
		innerNest := fac.Nest([]rune{'['}, []rune{']'}, inner)
		nested := fac.Nest([]rune{'('}, []rune{')'}, innerNest)
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &nested, Outcome: "paren_bracket_a"},
		}
		isMatch := func(o AnnotatedOutcome[string]) bool { return o.Value == "paren_bracket_a" }
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'(', '[', 'a', ']', ')'}, isMatch)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'(', '[', 'a', ')', ']'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'(', '[', 'a', ']'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'[', 'a', ']', ')'})
	})

	t.Run("Class", func(t *testing.T) {
		pat := fac.Class(fac.Range('0', '9'))
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "digit"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'3'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'3', '3'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a'})
	})

	t.Run("Concat", func(t *testing.T) {
		pat := fac.Sequence(fac.Literal('x'), fac.Literal('y'))
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "xy"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'x', 'y'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'x'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'y', 'x'})
	})

	t.Run("Union", func(t *testing.T) {
		pat := fac.AnyOf(fac.Literal('a'), fac.Literal('b'))
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "a_or_b"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'a'}, acceptAny)
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'b'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a', 'b'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'c'})
	})

	t.Run("DeterminismFailureUnionSameNestSymbols", func(t *testing.T) {
		nestA := fac.Nest([]rune{'('}, []rune{')'}, fac.Literal('a'))
		nestB := fac.Nest([]rune{'('}, []rune{')'}, fac.Literal('b'))
		pat := fac.AnyOf(nestA, nestB)
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "x"},
		}
		formatter := ObservationFormatter[rune]{
			ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
		}
		ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
		dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, instructions, ctx, "")
		if err == nil {
			t.Fatal("expected error for union of two nests with same input symbols (determinism / stack alphabet ambiguity)")
		}
		if dpdas != nil {
			t.Fatal("expected nil DPDAs when compilation returns error")
		}
	})

	t.Run("Star", func(t *testing.T) {
		pat := fac.Star(fac.Literal('a'))
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "as"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{}, acceptAny)
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'a', 'a', 'a'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a', 'a', 'b'})
	})

	t.Run("StarNest", func(t *testing.T) {
		inner := fac.Literal('a')
		nested := fac.Nest([]rune{'('}, []rune{')'}, inner)
		pat := fac.Star(nested)
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "star_paren_a"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{}, acceptAny)
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'(', 'a', ')'}, acceptAny)
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'(', 'a', ')', '(', 'a', ')'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'(', 'a'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a', ')'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{')', 'a', '('})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'(', 'a', ')', ')'})
	})

	t.Run("Plus", func(t *testing.T) {
		pat := fac.Plus(fac.Literal('a'))
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "one_or_more_a"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'a'}, acceptAny)
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'a', 'a', 'a'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{})
	})

	t.Run("Optional", func(t *testing.T) {
		pat := fac.Optional(fac.Literal('a'))
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "opt_a"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{}, acceptAny)
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'a'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a', 'a'})
	})

	t.Run("RepeatBounded", func(t *testing.T) {
		pat := fac.Repeat(fac.Literal('a'), 2, 3)
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "aa_or_aaa"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'a', 'a'}, acceptAny)
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{'a', 'a', 'a'}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a'})
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a', 'a', 'a', 'a'})
	})

	t.Run("Empty", func(t *testing.T) {
		pat := fac.Empty()
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "epsilon"},
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, []rune{}, acceptAny)
		runVistraDPDAReject(t, allocFn, scratchAlloc, instructions, []rune{'a'})
	})

	t.Run("StressDeepNesting", func(t *testing.T) {
		const depth = 100
		inner := fac.Literal('a')
		nested := inner
		for d := 0; d < depth; d++ {
			nested = fac.Nest([]rune{'('}, []rune{')'}, nested)
		}
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &nested, Outcome: "deep"},
		}
		input := make([]rune, 0, depth*2+1)
		for i := 0; i < depth; i++ {
			input = append(input, '(')
		}
		input = append(input, 'a')
		for i := 0; i < depth; i++ {
			input = append(input, ')')
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, input, func(o AnnotatedOutcome[string]) bool { return o.Value == "deep" })
	})

	t.Run("StressStarLongInput", func(t *testing.T) {
		pat := fac.Star(fac.Literal('a'))
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "as"},
		}
		input := make([]rune, 1000)
		for i := range input {
			input[i] = 'a'
		}
		runVistraDPDAAccept(t, allocFn, scratchAlloc, instructions, input, acceptAny)
	})

	t.Run("MultipleInstructions", func(t *testing.T) {
		patA := fac.Literal('a')
		patB := fac.Literal('b')
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &patA, Outcome: "a"},
			{Pattern: &patB, Outcome: "b"},
		}
		formatter := ObservationFormatter[rune]{
			ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
		}
		ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
		dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, instructions, ctx, "")
		if err != nil {
			t.Fatalf("VistraCompileToDPDA: %v", err)
		}
		foundationtesting.Assert(len(dpdas) == 2, "expected 2 DPDAs", "two DPDAs", t)
		scratchAllocFn := func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(scratchAlloc, sizeBytes, alignment)
		}
		state0 := autarch.DPDAStateCreate(dpdas[0], scratchAllocFn, 8)
		accepted0, err := autarch.DPDARunAndAccept(dpdas[0], 0, state0, []rune{'a'}, acceptAny)
		if err != nil {
			t.Fatalf("DPDARunAndAccept dpda[0]: %v", err)
		}
		foundationtesting.Assert(accepted0, "expected accept for 'a' on first DPDA", "accept a", t)
		state1 := autarch.DPDAStateCreate(dpdas[1], scratchAllocFn, 8)
		accepted1, err := autarch.DPDARunAndAccept(dpdas[1], 0, state1, []rune{'b'}, acceptAny)
		if err != nil {
			t.Fatalf("DPDARunAndAccept dpda[1]: %v", err)
		}
		foundationtesting.Assert(accepted1, "expected accept for 'b' on second DPDA", "accept b", t)
	})

	t.Run("EmptyInstructionsError", func(t *testing.T) {
		formatter := ObservationFormatter[rune]{
			ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
		}
		ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
		dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, nil, ctx, "")
		if err == nil {
			t.Fatal("expected error for nil instructions")
		}
		if dpdas != nil {
			t.Fatal("expected nil DPDAs on error")
		}
		dpdas, err = VistraCompileToDPDA[rune, uint64, string](allocFn, []PatternCompilationInstruction[rune, string, VistraAST[rune]]{}, ctx, "")
		if err == nil {
			t.Fatal("expected error for empty instructions")
		}
		if dpdas != nil {
			t.Fatal("expected nil DPDAs on error")
		}
	})

	t.Run("InvalidInputSymbol", func(t *testing.T) {
		pat := fac.Literal('a')
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "a"},
		}
		formatter := ObservationFormatter[rune]{
			ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
		}
		ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
		dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, instructions, ctx, "")
		if err != nil {
			t.Fatalf("VistraCompileToDPDA: %v", err)
		}
		if len(dpdas) != 1 {
			t.Fatalf("expected 1 DPDA, got %d", len(dpdas))
		}
		dpda := dpdas[0]
		scratchAllocFn := func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(scratchAlloc, sizeBytes, alignment)
		}
		state := autarch.DPDAStateCreate(dpda, scratchAllocFn, 8)
		input := []rune{'§'}
		accepted, runErr := autarch.DPDARunAndAccept(dpda, 0, state, input, acceptAny)
		if runErr == nil && accepted {
			t.Fatal("expected error or rejection when input rune is not in alphabet")
		}
	})

	t.Run("DeterminismFailureOverlappingUnion", func(t *testing.T) {
		pat := fac.AnyOf(fac.Literal('a'), fac.Literal('a'))
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "x"},
		}
		formatter := ObservationFormatter[rune]{
			ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
		}
		ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
		dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, instructions, ctx, "")
		if err == nil {
			t.Fatal("expected error for overlapping union (determinism violated); safety guard must fail compilation")
		}
		if dpdas != nil {
			t.Fatal("expected nil DPDAs when compilation returns error")
		}
	})

	t.Run("CompilerSanityStateCount", func(t *testing.T) {
		formatter := ObservationFormatter[rune]{
			ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
		}
		ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
		checkStates := func(name string, pat VistraAST[rune], maxStates uint64) {
			t.Helper()
			instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
				{Pattern: &pat, Outcome: "o"},
			}
			dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, instructions, ctx, "")
			if err != nil {
				t.Fatalf("%s: compile: %v", name, err)
			}
			if len(dpdas) != 1 {
				t.Fatalf("%s: expected 1 DPDA", name)
			}
			n := autarch.DPDANumStates(dpdas[0])
			if n > maxStates {
				t.Errorf("%s: state count %d exceeds bound %d (possible transition explosion)", name, n, maxStates)
			}
		}
		checkStates("StarLiteral", fac.Star(fac.Literal('a')), 10)
		checkStates("RepeatBounded", fac.Repeat(fac.Literal('a'), 2, 3), 15)
		checkStates("OptionalLiteral", fac.Optional(fac.Literal('a')), 10)
	})

	t.Run("DPDAStructuralValidation", func(t *testing.T) {
		pat := fac.Literal('a')
		instructions := []PatternCompilationInstruction[rune, string, VistraAST[rune]]{
			{Pattern: &pat, Outcome: "a"},
		}
		formatter := ObservationFormatter[rune]{
			ToBytes: func(obs []rune) []byte { return []byte(string(obs)) },
		}
		ctx := CreateSharedCompilationContext[rune, VistraAST[rune]](nil, vistraRuneCmp, formatter)
		dpdas, err := VistraCompileToDPDA[rune, uint64, string](allocFn, instructions, ctx, "")
		if err != nil {
			t.Fatalf("VistraCompileToDPDA: %v", err)
		}
		if len(dpdas) != 1 {
			t.Fatalf("expected 1 DPDA, got %d", len(dpdas))
		}
		dpda := dpdas[0]
		numStates := autarch.DPDANumStates(dpda)
		if numStates < 1 {
			t.Fatalf("DPDA must have at least one state (entry); got numStates=%d", numStates)
		}
		scratchAllocFn := func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(scratchAlloc, sizeBytes, alignment)
		}
		state := autarch.DPDAStateCreate(dpda, scratchAllocFn, 8)
		finalState, ok, runErr := autarch.DPDARun(dpda, 0, state, []rune{'a'})
		if runErr != nil {
			t.Fatalf("run from entry state 0 must not error: %v", runErr)
		}
		if !ok {
			t.Fatal("run from entry state 0 with valid input must succeed")
		}
		if finalState >= numStates {
			t.Fatalf("final state %d must be < numStates %d (all transitions reference valid state IDs)", finalState, numStates)
		}
	})

	t.Run("DPDAReplaceOperation", func(t *testing.T) {
		inputAlphabet := []autarch.SymbolDefinition[rune]{
			{ID: 0, Name: "a", Match: func(r rune) bool { return r == 'a' }},
			{ID: 1, Name: "b", Match: func(r rune) bool { return r == 'b' }},
		}
		stackAlphabet := []autarch.SymbolDefinition[uint64]{
			{ID: 0, Name: "BOS", Match: func(u uint64) bool { return u == 0 }},
			{ID: 1, Name: "X", Match: func(u uint64) bool { return u == 1 }},
		}
		symA := autarch.SymbolCreate[rune]("a", 0)
		symB := autarch.SymbolCreate[rune]("b", 1)
		stackBOS := autarch.SymbolCreate[uint64]("BOS", 0)
		stackX := autarch.SymbolCreate[uint64]("X", 1)
		transitions := []autarch.DPDATransition[rune, uint64]{
			{CurrentState: 0, InputSymbol: symA, CurrentStackTop: stackBOS, NextState: 1, Operation: autarch.StackOperation[uint64]{Kind: autarch.Push, StackSymbol: stackX}},
			{CurrentState: 1, InputSymbol: symB, CurrentStackTop: stackX, NextState: 0, Operation: autarch.StackOperation[uint64]{Kind: autarch.Replace, StackSymbol: stackBOS}},
		}
		outcomes := []string{"even", "odd"}
		indexer := autarch.SymbolIndexerBuild(inputAlphabet)
		dpda, err := autarch.DPDACreate(allocFn, inputAlphabet, stackAlphabet, 0, transitions, outcomes, indexer)
		if err != nil {
			t.Fatalf("DPDACreate: %v", err)
		}
		scratchAllocFn := func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(scratchAlloc, sizeBytes, alignment)
		}
		state := autarch.DPDAStateCreate(dpda, scratchAllocFn, 8)
		accepted, err := autarch.DPDARunAndAccept(dpda, 0, state, []rune{'a', 'b'}, func(s string) bool { return s != "" })
		if err != nil {
			t.Fatalf("DPDARunAndAccept: %v", err)
		}
		if !accepted {
			t.Fatal("expected accept for input 'a' then 'b' (Push then Replace)")
		}
		depth := autarch.DPDAStackDepth(state)
		if depth != 2 {
			t.Fatalf("after Replace (pop X, push BOS) from [X,BOS] runtime has stack depth %d; Replace was exercised", depth)
		}
	})
}
