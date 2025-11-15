package autarch

import (
	"fmt"
	foundationtesting "foundation/testing"
	"memcore"
	"memforge"
	"testing"
)

func TestDFA(t *testing.T) {
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

	dfa := DFACreate(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		[]rune{'a', 'b'},
		[]Transition[rune]{
			{currentState: 0, symbol: symbolA, nextState: 1},
			{currentState: 0, symbol: symbolB, nextState: 0},
			{currentState: 1, symbol: symbolA, nextState: 2},
			{currentState: 1, symbol: symbolB, nextState: 2},
			{currentState: 2, symbol: symbolA, nextState: 2},
			{currentState: 2, symbol: symbolB, nextState: 0},
		},
		[]bool{false, false, true},
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
		false, // invalid state
	)

	type testCase struct {
		input    string
		expected bool
	}

	tests := []testCase{
		// --- Basic failures (empty, no path to accepting) ---
		{"", false},
		{"b", false},
		{"bb", false},
		{"bbb", false},

		// --- Minimal accept ---
		// Must reach state 2 (final)
		{"aa", true}, // 0 -a-> 1 -a-> 2
		{"ab", true}, // 0 -a-> 1 -b-> 2

		// --- Once in state 2, staying or leaving correctly ---
		{"aaa", true},   // 0->1->2->2
		{"aab", false},  // 0->1->2->0
		{"aaba", false}, // ends in 0
		{"abaa", true},  // 0->1->2->2->2
		{"abab", false}, // ends in 0

		// --- More complex valid ones ---
		{"baa", true},   // 0 -b->0 -a->1 -a->2
		{"baba", true},  // 0->0->1->2->2
		{"bbbaa", true}, // stays in 0 for bbb, then a→1, a→2

		// --- Paths that enter accepting state then fall out ---
		{"aab", false},  // 0->1->2->0
		{"aaab", false}, // 0->1->2->2->0
		{"baab", false}, // ends in 0

		// --- Longer staying in accepting state ---
		{"aaaaa", true},   // once in 2, 'a' keeps in 2
		{"abaaab", false}, // ends in 0
		{"abaaa", true},   // ends in 2

		// --- Invalid symbol cases ---
		{"c", false},
		{"ac", false},
		{"abc", false},
		{"cab", false},
	}

	for _, test := range tests {
		outcome, _ := DFARun(dfa, []rune(test.input))

		errMsg := fmt.Sprintf("incorrect outcome, expected=%v,got=%v,input=%s", test.expected, outcome, test.input)
		successMsg := fmt.Sprintf("correct outcome, expected=%v,got=%v,input=%s", test.expected, outcome, test.input)

		foundationtesting.Assert(outcome == test.expected, errMsg, successMsg, t)
	}
}
