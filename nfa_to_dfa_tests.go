package autarch

import (
	"fmt"
	foundationtesting "foundation/testing"
	"memcore"
	"memforge"
	"testing"
)

func TestNFAToDFA(t *testing.T) {
	allocator := memforge.DynamicLinearAllocatorCreateFunction(uint64(1*memcore.Byte), func(currentCap, neededCap uint64) uint64 {
		newSize := currentCap * 2
		if newSize < neededCap {
			newSize = neededCap
		}
		if newSize > uint64(1*memcore.GigaByte) {
			panic("too much memory for a test")
		}
		return newSize
	})
	defer memforge.DynamicLinearAllocatorDestroy(allocator)

	symbolA := SymbolCreate[rune]("a", 0)
	symbolB := SymbolCreate[rune]("b", 1)

	alphabet := []SymbolDefinition[rune]{
		{ID: 0, Name: "a", Match: func(r rune) bool { return r == 'a' }},
		{ID: 1, Name: "b", Match: func(r rune) bool { return r == 'b' }},
	}
	resolver := func(r rune) []uint64 {
		switch r {
		case 'a':
			return []uint64{0}
		case 'b':
			return []uint64{1}
		default:
			return nil
		}
	}
	deterministicResolver := func(r rune) (uint64, bool) {
		switch r {
		case 'a':
			return 0, true
		case 'b':
			return 1, true
		default:
			return 0, false
		}
	}

	// NFA for: strings containing at least one 'a'
	nfa := NFACreate(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		alphabet,
		[]Transition[rune]{
			{CurrentState: 0, Symbol: symbolA, NextState: 1},
			{CurrentState: 0, Symbol: symbolB, NextState: 0},
			{CurrentState: 1, Symbol: symbolA, NextState: 1},
			{CurrentState: 1, Symbol: symbolB, NextState: 1},
		},
		nil, // No epsilon edges
		[]uint64{0},
		[]bool{false, true},
		resolver,
	)

	// Convert NFA to DFA
	dfa := NFAToDFA(
		nfa,
		1*memcore.KiloByte,
		1*memcore.MegaByte,
		func(size, align uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, size, align)
		},
		deterministicResolver,
		nil, // Use default resolution (OutcomeResolutionFirst)
	)

	type testCase struct {
		input string
	}

	tests := []testCase{
		{""}, {"b"}, {"bb"}, {"bbbbbbb"},
		{"a"}, {"ba"}, {"ab"}, {"bab"}, {"bbbabb"},
		{"aaa"}, {"baaaaab"}, {"bababab"},
		{"c"}, {"ac"}, {"bca"},
	}

	for _, test := range tests {

		runes := []rune(test.input)

		// --------- NFA ACCEPTANCE ----------
		nfaOut, _ := NFARun(nfa, runes)

		nfaAccepts := false
		for _, o := range nfaOut {
			if o {
				nfaAccepts = true
				break
			}
		}

		// --------- DFA ACCEPTANCE ----------
		dfaOutcome, err := DFARun(dfa, runes)
		dfaAccepts := (err == nil && dfaOutcome)

		// --------- ASSERT EQUIVALENCE ----------
		errMsg := fmt.Sprintf("NFA/DFA mismatch for input %q: NFA=%v, DFA=%v",
			test.input, nfaAccepts, dfaAccepts)

		successMsg := fmt.Sprintf("NFA/DFA match for input %q: accepted=%v",
			test.input, nfaAccepts)

		foundationtesting.Assert(nfaAccepts == dfaAccepts, errMsg, successMsg, t)
	}
}

func TestNFAToDFAEpsilon(t *testing.T) {
	allocator := memforge.DynamicLinearAllocatorCreateFunction(uint64(1*memcore.Byte), func(currentCap, neededCap uint64) uint64 {
		newSize := currentCap * 2
		if newSize < neededCap {
			newSize = neededCap
		}
		if newSize > uint64(1*memcore.GigaByte) {
			panic("too much memory for a test")
		}
		return newSize
	})
	defer memforge.DynamicLinearAllocatorDestroy(allocator)

	symbolA := SymbolCreate[rune]("a", 0)
	symbolB := SymbolCreate[rune]("b", 1)

	alphabet := []SymbolDefinition[rune]{
		{ID: 0, Name: "a", Match: func(r rune) bool { return r == 'a' }},
		{ID: 1, Name: "b", Match: func(r rune) bool { return r == 'b' }},
	}
	resolver := func(r rune) []uint64 {
		switch r {
		case 'a':
			return []uint64{0}
		case 'b':
			return []uint64{1}
		default:
			return nil
		}
	}
	deterministicResolver := func(r rune) (uint64, bool) {
		switch r {
		case 'a':
			return 0, true
		case 'b':
			return 1, true
		default:
			return 0, false
		}
	}

	nfa := NFACreate(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		alphabet,
		[]Transition[rune]{
			// State 0 loops on 'a'
			{CurrentState: 0, Symbol: symbolA, NextState: 0},
			// State 1 loops on 'b'
			{CurrentState: 1, Symbol: symbolB, NextState: 1},
		},
		map[uint64][]uint64{
			0: {1}, // Epsilon transition allows moving from 'a's to 'b's
		},
		[]uint64{0},
		[]bool{true, true}, // State 0 (for a*) and State 1 (for b*) outcomes
		resolver,
	)

	// Convert NFA to DFA
	dfa := NFAToDFA(
		nfa,
		1*memcore.KiloByte,
		1*memcore.MegaByte,
		func(size, align uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, size, align)
		},
		deterministicResolver,
		nil, // Use default resolution (OutcomeResolutionFirst)
	)

	type testCase struct {
		input string
	}

	// Test cases for the language a*b*
	tests := []testCase{
		// --- Group 1: Accept (Valid a*b* strings) ---
		{""},
		{"a"},
		{"aaa"},
		{"b"},
		{"bbb"},
		{"ab"},
		{"aabbb"},

		// --- Group 2: Reject (Valid symbols, invalid pattern) ---
		{"ba"},
		{"bba"},
		{"aba"},
		{"bab"},
		{"bbbbba"},
		{"aabaa"},

		// --- Group 3: Reject (Invalid symbols) ---
		{"c"},
		{"ac"},
		{"bca"},
		{"acb"},
	}

	for _, test := range tests {

		runes := []rune(test.input)

		// --------- NFA ACCEPTANCE ----------
		nfaOut, _ := NFARun(nfa, runes)

		nfaAccepts := false
		for _, o := range nfaOut {
			if o {
				nfaAccepts = true
				break
			}
		}

		// --------- DFA ACCEPTANCE ----------
		// DFARun returns an error on an invalid symbol,
		// which correctly results in 'dfaAccepts = false'.
		dfaOutcome, err := DFARun(dfa, runes)
		dfaAccepts := (err == nil && dfaOutcome)

		// --------- ASSERT EQUIVALENCE ----------
		errMsg := fmt.Sprintf("NFA/DFA mismatch for input %q: NFA=%v, DFA=%v",
			test.input, nfaAccepts, dfaAccepts)

		successMsg := fmt.Sprintf("NFA/DFA match for input %q: accepted=%v",
			test.input, nfaAccepts)

		foundationtesting.Assert(nfaAccepts == dfaAccepts, errMsg, successMsg, t)
	}
}
