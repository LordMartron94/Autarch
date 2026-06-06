package autarch

import (
	"fmt"
	foundationtesting "foundation/testing"
	"memcore"
	"memforge"
	"testing"
)

func TestDFAMinimize(t *testing.T) {
	// ───────────────────────────────────────────────────────────────
	// Allocator Setup (same as TestDFA)
	// ───────────────────────────────────────────────────────────────
	allocator := memforge.DynamicLinearAllocatorCreateFunction(uint64(1*memcore.KiloByte), func(currentCap, neededCap uint64) uint64 {
		newSize := currentCap * 2
		if newSize < neededCap {
			newSize = neededCap
		}
		if newSize > uint64(1*memcore.GigaByte) {
			panic("too much memory for a test")
		}
		return newSize
	}, "test")
	defer memforge.DynamicLinearAllocatorDestroy(allocator)

	dfaAllocationFn := func(sizeBytes, alignment uint64) memcore.MarkRaw {
		return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
	}

	// ───────────────────────────────────────────────────────────────
	// Symbols & Indexer (same as TestDFA)
	// ───────────────────────────────────────────────────────────────
	symbolA := SymbolCreate[rune]("a", 0)
	symbolB := SymbolCreate[rune]("b", 1)

	alphabet := []SymbolDefinition[rune]{
		{ID: 0, Name: "a", Match: func(r rune) bool { return r == 'a' }},
		{ID: 1, Name: "b", Match: func(r rune) bool { return r == 'b' }},
	}
	resolver := func(r rune) (uint64, bool) {
		switch r {
		case 'a':
			return 0, true
		case 'b':
			return 1, true
		default:
			return 0, false
		}
	}

	// ───────────────────────────────────────────────────────────────
	// 1. Create a Non-Minimal DFA (6 states)
	// ───────────────────────────────────────────────────────────────
	// This DFA is non-minimal.
	// - States 1 and 2 are equivalent (both non-accepting, a->[3,4], b->5)
	// - States 3 and 4 are equivalent (both accepting, a->[3,4], b->5)
	// The minimal DFA should have 4 states: [0], [1,2], [3,4], [5]
	originalDFA := DFACreate(
		dfaAllocationFn,
		alphabet,
		[]Transition[rune]{
			// Start state 0
			{CurrentState: 0, Symbol: symbolA, NextState: 1},
			{CurrentState: 0, Symbol: symbolB, NextState: 2},
			// Equivalent states 1 and 2 (non-accepting)
			{CurrentState: 1, Symbol: symbolA, NextState: 3},
			{CurrentState: 1, Symbol: symbolB, NextState: 5},
			{CurrentState: 2, Symbol: symbolA, NextState: 4},
			{CurrentState: 2, Symbol: symbolB, NextState: 5},
			// Equivalent states 3 and 4 (accepting)
			{CurrentState: 3, Symbol: symbolA, NextState: 3},
			{CurrentState: 3, Symbol: symbolB, NextState: 5},
			{CurrentState: 4, Symbol: symbolA, NextState: 4},
			{CurrentState: 4, Symbol: symbolB, NextState: 5},
			// Dead state 5 (non-accepting)
			{CurrentState: 5, Symbol: symbolA, NextState: 5},
			{CurrentState: 5, Symbol: symbolB, NextState: 5},
		},
		// Accepting for 6 states: 0,1,2,3,4,5 → outcomes only (true = match)
		[]bool{false, false, false, true, true, false},
		resolver,
	)

	// ───────────────────────────────────────────────────────────────
	// 2. Minimize the DFA
	// ───────────────────────────────────────────────────────────────
	// These memory limits are for the *internal* temp allocator used by Hopcroft's
	minTempMem := uint64(1 * memcore.KiloByte)
	maxTempMem := uint64(1 * memcore.GigaByte)

	minimizedDFA := DFAMinimize(
		originalDFA,
		dfaAllocationFn, // Allocator for the *new* minimal DFA
		memcore.MemoryUnitBytes(minTempMem),
		memcore.MemoryUnitBytes(maxTempMem),
		func(outcome bool) bool {
			return outcome
		},
	)

	// ───────────────────────────────────────────────────────────────
	// 3. Define Test Cases
	// ───────────────────────────────────────────────────────────────
	// These test cases are designed to check all paths of the non-minimal DFA.
	// The minimal DFA must produce identical results.
	type testCase struct {
		input    string
		expected bool
	}

	tests := []testCase{
		// --- Basic failures ---
		{"", false},     // ends in 0 (reject)
		{"a", false},    // ends in 1 (reject)
		{"b", false},    // ends in 2 (reject)
		{"ab", false},   // 0->1->5 (reject)
		{"bb", false},   // 0->2->5 (reject)
		{"aba", false},  // 0->1->5->5 (reject)
		{"bba", false},  // 0->2->5->5 (reject)
		{"abbb", false}, // 0->1->5->5->5 (reject)

		// --- Basic accepts ---
		{"aa", true}, // 0->1->3 (accept)
		{"ba", true}, // 0->2->4 (accept)

		// --- Staying in accepting state ---
		{"aaa", true},  // 0->1->3->3 (accept)
		{"baa", true},  // 0->2->4->4 (accept)
		{"aaaa", true}, // 0->1->3->3->3 (accept)
		{"baaa", true}, // 0->2->4->4->4 (accept)

		// --- Falling out of accepting state ---
		{"aab", false},  // 0->1->3->5 (reject)
		{"bab", false},  // 0->2->4->5 (reject)
		{"aaab", false}, // 0->1->3->3->5 (reject)
		{"baab", false}, // 0->2->4->4->5 (reject)

		// --- Complex paths ---
		{"aabba", false}, // 0->1->3->5->5->5 (reject)
		{"baaba", false}, // 0->2->4->5->5->5 (reject)

		// --- Invalid symbols ---
		{"c", false},
		{"ac", false},
		{"aac", false},
		{"ca", false},
	}

	// ───────────────────────────────────────────────────────────────
	// 4. Run Tests against BOTH DFAs
	// ───────────────────────────────────────────────────────────────
	for _, test := range tests {
		// --- Run on Original DFA ---
		outcomeOrig, errOrig := DFARun(originalDFA, []rune(test.input))
		if errOrig != nil {
			outcomeOrig = false // Treat errors as non-accepting
		}

		// --- Run on Minimized DFA ---
		outcomeMin, errMin := DFARun(minimizedDFA, []rune(test.input))
		if errMin != nil {
			outcomeMin = false // Treat errors as non-accepting
		}

		// --- Assertion 1: Check that the original DFA is correct ---
		// This validates our test case logic.
		errMsgOrig := fmt.Sprintf("OriginalDFA: incorrect outcome, expected=%v, got=%v, input=%s", test.expected, outcomeOrig, test.input)
		succMsgOrig := fmt.Sprintf("OriginalDFA: correct outcome, expected=%v, got=%v, input=%s", test.expected, outcomeOrig, test.input)
		foundationtesting.Assert(outcomeOrig == test.expected, errMsgOrig, succMsgOrig, t)

		// --- Assertion 2: Check that the minimized DFA matches the original ---
		// This validates the minimizer.
		errMsgMin := fmt.Sprintf("MinimizedDFA: mismatch, original=%v, minimized=%v, input=%s", outcomeOrig, outcomeMin, test.input)
		succMsgMin := fmt.Sprintf("MinimizedDFA: match, original=%v, minimized=%v, input=%s", outcomeOrig, outcomeMin, test.input)
		foundationtesting.Assert(outcomeOrig == outcomeMin, errMsgMin, succMsgMin, t)
	}
}
