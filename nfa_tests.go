package autarch

import (
	"fmt"
	foundationtesting "foundation/testing"
	"memcore"
	"memforge"
	"testing"
)

func TestNFA(t *testing.T) {
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

	nfa := NFACreate(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		[]rune{'a', 'b'},
		[]Transition[rune]{
			{CurrentState: 0, Symbol: symbolA, NextState: 1},
			{CurrentState: 0, Symbol: symbolB, NextState: 0},
			{CurrentState: 1, Symbol: symbolA, NextState: 1},
			{CurrentState: 1, Symbol: symbolB, NextState: 1},
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

	type testCase struct {
		input    string
		expected bool
	}

	tests := []testCase{
		// --- Reject: no 'a' ---
		{"", false},
		{"b", false},
		{"bb", false},
		{"bbbbbbb", false},

		// --- Accept: contains at least one 'a' ---
		{"a", true},
		{"ba", true},
		{"ab", true},
		{"bab", true},
		{"bbbabb", true},

		// --- Accept: many 'a's ---
		{"aaa", true},
		{"baaaaab", true},
		{"bababab", true},

		// --- Reject: invalid symbol ---
		{"c", false},
		{"ac", false},
		{"bca", false},
	}

	for _, test := range tests {
		outcomes, _ := NFARun(nfa, []rune(test.input))

		accepted := false
		for _, o := range outcomes {
			if o {
				accepted = true
				break
			}
		}

		errMsg := fmt.Sprintf("incorrect outcome, expected=%v, got=%v, input=%s", test.expected, accepted, test.input)
		successMsg := fmt.Sprintf("correct outcome, expected=%v, got=%v, input=%s", test.expected, accepted, test.input)

		foundationtesting.Assert(accepted == test.expected, errMsg, successMsg, t)
	}
}

func TestNFAEpsilon(t *testing.T) {
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
	epsilonSymbol := EpsilonSymbolCreate[rune]() // Epsilon is back
	errSymbol := SymbolCreate[rune]("error", 2)

	nfa := NFACreate(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		[]rune{'a', 'b'},
		[]Transition[rune]{
			{CurrentState: 0, Symbol: symbolA, NextState: 0},
			{CurrentState: 0, Symbol: epsilonSymbol, NextState: 1},
			{CurrentState: 1, Symbol: symbolB, NextState: 1},
		},
		[]uint64{0},
		[]bool{true, true}, // State 0 and State 1 are both accepting
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

	type testCase struct {
		input    string
		expected bool
	}

	// Test cases for the language a*b*
	tests := []testCase{
		// --- Group 1: Accept (Valid a*b* strings) ---
		{"", true},      // Empty string is accepted at state 0
		{"a", true},     // Accepted at state 0
		{"aaa", true},   // Accepted at state 0
		{"b", true},     // Accepted via (0) --[ε]--> (1) --[b]--> (1)
		{"bbb", true},   // Accepted via (0) --[ε]--> (1) --[b*]--> (1)
		{"ab", true},    // Accepted via (0) --[a]--> (0) --[ε]--> (1) --[b]--> (1)
		{"aabbb", true}, // Accepted via (0) --[a*]--> (0) --[ε]--> (1) --[b*]--> (1)

		// --- Group 2: Reject (Valid symbols, invalid pattern) ---
		{"ba", false},     // Fails: 'b' cannot come before 'a'
		{"bba", false},    // Fails
		{"aba", false},    // Fails: 'a' cannot come after 'b'
		{"bab", false},    // Fails
		{"bbbbba", false}, // Fails
		{"aabaa", false},  // Fails

		// --- Group 3: Reject (Invalid symbols) ---
		{"c", false},
		{"ac", false},
		{"bca", false},
		{"acb", false},
	}

	for _, test := range tests {
		outcomes, _ := NFARun(nfa, []rune(test.input))

		accepted := false
		for _, o := range outcomes {
			if o {
				accepted = true
				break
			}
		}

		errMsg := fmt.Sprintf("incorrect outcome, expected=%v, got=%v, input=%s", test.expected, accepted, test.input)
		successMsg := fmt.Sprintf("correct outcome, expected=%v, got=%v, input=%s", test.expected, accepted, test.input)

		foundationtesting.Assert(accepted == test.expected, errMsg, successMsg, t)
	}
}
