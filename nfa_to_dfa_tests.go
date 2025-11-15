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
	errSymbol := SymbolCreate[rune]("error", 2)

	// NFA for: strings containing at least one 'a'
	nfa := NFACreate(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		[]rune{'a', 'b'},
		[]Transition[rune]{
			{currentState: 0, symbol: symbolA, nextState: 1},
			{currentState: 0, symbol: symbolB, nextState: 0},
			{currentState: 1, symbol: symbolA, nextState: 1},
			{currentState: 1, symbol: symbolB, nextState: 1},
		},
		[]uint64{0},
		[]bool{false, true},
		func(observation rune) (Symbol[rune], bool) {
			switch observation {
			case 'a':
				return symbolA, true
			case 'b':
				return symbolB, true
			default:
				return errSymbol, false
			}
		},
	)

	// Convert NFA to DFA
	dfa := NFAToDFA(
		nfa,
		1*memcore.KiloByte,
		1*memcore.MegaByte,
		func(size, align uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, size, align)
		},
		false,
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
	epsilonSymbol := EpsilonSymbolCreate[rune]() // Epsilon symbol
	errSymbol := SymbolCreate[rune]("error", 2)

	// NFA for: a*b* (any number of 'a's followed by any number of 'b's)
	// This NFA uses an epsilon transition.
	nfa := NFACreate(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		[]rune{'a', 'b'},
		[]Transition[rune]{
			// State 0 loops on 'a'
			{currentState: 0, symbol: symbolA, nextState: 0},
			// Epsilon transition allows moving from 'a's to 'b's
			{currentState: 0, symbol: epsilonSymbol, nextState: 1},
			// State 1 loops on 'b'
			{currentState: 1, symbol: symbolB, nextState: 1},
		},
		[]uint64{0},
		[]bool{true, true}, // State 0 (for a*) and State 1 (for b*) are accepting
		func(observation rune) (Symbol[rune], bool) {
			switch observation {
			case 'a':
				return symbolA, true
			case 'b':
				return symbolB, true
			default:
				return errSymbol, false
			}
		},
	)

	// Convert NFA to DFA
	dfa := NFAToDFA(
		nfa,
		1*memcore.KiloByte,
		1*memcore.MegaByte,
		func(size, align uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, size, align)
		},
		false,
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
