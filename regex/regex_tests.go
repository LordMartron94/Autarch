package regex

import (
	"autarch"
	"fmt"
	foundationtesting "foundation/testing"
	"memcore"
	"memforge"
	"testing"
)

func TestRegex(t *testing.T) {
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

	const testRegex string = "(a|b)*c[0-9]?d+"

	nfa, err := RegexToNFA(
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		testRegex,
	)

	if err != nil {
		panic(err)
	}

	dfa := autarch.NFAToDFA(nfa, 1*memcore.Byte, 1*memcore.GigaByte,
		func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		false,
	)

	minimized := autarch.DFAMinimize(
		dfa, func(sizeBytes, alignment uint64) memcore.MarkRaw {
			return memforge.DynamicLinearAllocatorMallocUnsafe(allocator, sizeBytes, alignment)
		},
		1*memcore.Byte, 1*memcore.GigaByte,
		false,
	)

	type testCase struct {
		input    string
		expected bool
	}

	tests := []testCase{
		{"cd", true},
		{"cdd", true},
		{"c5d", true},
		{"ac3ddd", true},
		{"babac9dd", true},
		{"abacd", true},

		{"c", false},
		{"d", false},
		{"c5", false},
		{"cd5", false},
		{"cdd5", false},
		{"acb", false},
		{"cddddx", false},
		{"ac?d", false},
	}

	for _, test := range tests {
		outcome, err := autarch.DFARun(minimized, []rune(test.input))
		if err != nil {
			outcome = false
		}

		errMsg := fmt.Sprintf("incorrect outcome, expected=%v,got=%v,input=%s", test.expected, outcome, test.input)
		successMsg := fmt.Sprintf("correct outcome, expected=%v,got=%v,input=%s", test.expected, outcome, test.input)

		foundationtesting.Assert(outcome == test.expected, errMsg, successMsg, t)
	}
}
