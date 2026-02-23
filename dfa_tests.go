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

	alphabet := []SymbolDefinition[rune]{
		{ID: 0, Name: "a", Match: func(r rune) bool { return r == 'a' }},
		{ID: 1, Name: "b", Match: func(r rune) bool { return r == 'b' }},
	}
	indexer := SymbolIndexerBuild(alphabet)

	dfa := DFACreate(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		alphabet,
		[]Transition[rune]{
			{CurrentState: 0, Symbol: symbolA, NextState: 1},
			{CurrentState: 0, Symbol: symbolB, NextState: 0},
			{CurrentState: 1, Symbol: symbolA, NextState: 2},
			{CurrentState: 1, Symbol: symbolB, NextState: 2},
			{CurrentState: 2, Symbol: symbolA, NextState: 2},
			{CurrentState: 2, Symbol: symbolB, NextState: 0},
		},
		[]bool{false, false, true},
		[]bool{false, false, true},
		indexer,
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
		outcome, _, err := DFARun(dfa, []rune(test.input))
		if err != nil {
			outcome = false
		}

		errMsg := fmt.Sprintf("incorrect outcome, expected=%v,got=%v,input=%s", test.expected, outcome, test.input)
		successMsg := fmt.Sprintf("correct outcome, expected=%v,got=%v,input=%s", test.expected, outcome, test.input)

		foundationtesting.Assert(outcome == test.expected, errMsg, successMsg, t)
	}
}
