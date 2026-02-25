package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memstruct"
	"sort"
	"strings"
)

/*
NFA represents a Non-Deterministic Finite Automaton.

An NFA allows multiple transitions from a single state for the same input symbol,
and supports epsilon (ε) transitions that consume no input. This flexibility
makes NFAs easier to construct but less efficient to execute than DFAs.

Use cases:
- Building automata from regular expressions or patterns
- Combining multiple automata via union operations
- Converting to DFA for efficient execution

Time complexity:
- State transitions: O(1) per transition lookup
- Epsilon closure: O(n) where n is the number of states
- Input processing: O(n * m) where n is input length, m is states per step

Space complexity: O(s * a) where s is number of states, a is alphabet size

Prerequisites:
- Alphabet must be provided with corresponding indexer function
- States array must be pre-allocated with outcomes
- Transitions must use valid symbol IDs from the alphabet

Edge cases:
- Empty input returns outcomes from starting states' epsilon closure
- Invalid symbols return errors during execution
- Dead states (no transitions) terminate processing early
*/
type NFA[TObservation, TStateOutcome any] struct {
	outcomes memcore.MarkRaw

	transitions  map[[2]uint64][]uint64
	epsilonEdges map[uint64][]uint64

	alphabet       []SymbolDefinition[TObservation]
	startingStates []uint64

	indexer   SymbolIndexer[TObservation]
	numStates uint64
}

/*
NFACreate constructs a new Non-Deterministic Finite Automaton.

The function allocates memory for state outcomes and builds the transition table
from the provided transitions. Multiple transitions from the same state with the
same symbol are allowed, enabling non-deterministic behavior. Epsilon transitions
are provided separately and stored in the epsilonEdges map.

Use cases:
- Creating NFAs from regular expression patterns
- Building automata programmatically
- Constructing NFAs for later conversion to DFA

Time complexity: O(t) where t is the number of transitions
Space complexity: O(s + t) where s is number of states, t is transitions

Prerequisites:
- alphabet must contain all symbols used in transitions
- startingStates must contain valid state indices (0 <= state < len(states))
- transitions must use valid symbol IDs (0 <= SymbolID < len(alphabet))
- epsilonEdges can be nil (will be initialized as empty map)
- indexer must correctly map observations to symbols in the alphabet

Edge cases:
- Panics if symbol ID exceeds alphabet size
- Empty startingStates creates an automaton that accepts nothing
- Duplicate transitions are preserved (non-deterministic behavior)
*/
func NFACreate[TObservation, TStateOutcome any](
	allocFn memarch.AllocationFn,
	alphabet []SymbolDefinition[TObservation],
	transitions []Transition[TObservation],
	epsilonEdges map[uint64][]uint64,
	startingStates []uint64,
	outcomes []TStateOutcome,
	indexer SymbolIndexer[TObservation],
) *NFA[TObservation, TStateOutcome] {
	alphabetSize := uint64(len(alphabet))
	numStates := uint64(len(outcomes))

	validateAlphabet(alphabet, "nfa")

	outcomeTable, _ := memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)

	for i := uint64(0); i < numStates; i++ {
		memstruct.ArraySetAtUnsafe(outcomeTable, i, outcomes[i])
	}

	transitionTable := make(map[[2]uint64][]uint64)
	for _, transition := range transitions {
		if transition.Symbol.SymbolID >= alphabetSize {
			panic(fmt.Errorf("symbol ID must be less than the alphabetSize, got=%d,max=%d", transition.Symbol.SymbolID, alphabetSize))
		}

		key := [2]uint64{transition.CurrentState, transition.Symbol.SymbolID}
		if _, exist := transitionTable[key]; !exist {
			transitionTable[key] = []uint64{transition.NextState}
		} else {
			transitionTable[key] = append(transitionTable[key], transition.NextState)
		}
	}

	epsilonMap := make(map[uint64][]uint64)
	for state, nextStates := range epsilonEdges {
		epsilonMap[state] = append([]uint64{}, nextStates...)
	}

	return &NFA[TObservation, TStateOutcome]{
		outcomes:       outcomeTable,
		transitions:    transitionTable,
		epsilonEdges:   epsilonMap,
		startingStates: startingStates,
		indexer:        indexer,
		numStates:      numStates,
		alphabet:       alphabet,
	}
}

// ============================================================
// NFA DEBUG PRINT (DFA-like, deterministic, table-based)
// ============================================================

/*
NFADebugFormatter provides optional formatting functions to make NFA debug output
more readable by converting numeric values to human-readable representations.

Each formatting function is optional (nil means use default formatting). This allows
partial formatting (e.g., only format symbol names, not IDs).

FormatStateIndicator supplies the single character shown between brackets for each
state (e.g. [A] for accepting, [S] for start). If nil, no bracket column is shown.
Return a single character; only the first rune is used for alignment.

Notes vs DFA debug:
  - NFAs can have *sets* of target states per (state, symbol), so cells render as "{1,2,7}"
    (or "—" if empty).
  - Epsilon transitions are displayed as a dedicated section and also (optionally) as
    an "ε" column in the transition table.

Performance guidance:
- Formatting hooks should be fast; avoid heavy allocations in hot loops.
*/
type NFADebugFormatter[TObservation, TStateOutcome any] struct {
	FormatSymbolName   func(id uint64, def SymbolDefinition[TObservation]) string
	FormatStateOutcome func(outcome TStateOutcome) string

	FormatSymbolID func(symbolID uint64) string
	FormatStateID  func(stateID uint64) string

	// FormatStateIndicator returns the single-character indicator shown in [ ] for this state.
	// E.g. "A" for accepting, "S" for start. If nil, no bracket is shown. Only the first rune is used.
	FormatStateIndicator func(stateID uint64, outcome TStateOutcome) string

	// FormatStateSet formats a set of state IDs (already sorted ascending).
	// If nil, a default "{a,b,c}" format is used.
	FormatStateSet func(sortedStates []uint64) string

	// MaxCellWidth caps rendered cell width in the transition table.
	// If 0, a sane default is used.
	MaxCellWidth int
}

// ------------------------------------------------------------ helpers

func nfaDebugFormatStateID[TObservation, TStateOutcome any](
	formatter *NFADebugFormatter[TObservation, TStateOutcome],
	id uint64,
) string {
	if formatter != nil && formatter.FormatStateID != nil {
		return formatter.FormatStateID(id)
	}
	return fmt.Sprintf("%d", id)
}

func nfaDebugFormatSymbolID[TObservation, TStateOutcome any](
	formatter *NFADebugFormatter[TObservation, TStateOutcome],
	id uint64,
) string {
	if formatter != nil && formatter.FormatSymbolID != nil {
		return formatter.FormatSymbolID(id)
	}
	return fmt.Sprintf("%d", id)
}

func nfaDebugFormatSymbolName[TObservation, TStateOutcome any](
	formatter *NFADebugFormatter[TObservation, TStateOutcome],
	id uint64,
	def SymbolDefinition[TObservation],
) string {
	if formatter != nil && formatter.FormatSymbolName != nil {
		return formatter.FormatSymbolName(id, def)
	}
	return def.Name
}

func nfaDebugFormatOutcome[TObservation, TStateOutcome any](
	formatter *NFADebugFormatter[TObservation, TStateOutcome],
	out TStateOutcome,
) string {
	if formatter != nil && formatter.FormatStateOutcome != nil {
		return formatter.FormatStateOutcome(out)
	}
	return fmt.Sprintf("%v", out)
}

func nfaDebugDefaultFormatStateSet(sorted []uint64) string {
	if len(sorted) == 0 {
		return "—"
	}
	if len(sorted) == 1 {
		return fmt.Sprintf("{%d}", sorted[0])
	}
	var sb strings.Builder
	sb.Grow(2 + len(sorted)*3)
	sb.WriteByte('{')
	for i, v := range sorted {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%d", v))
	}
	sb.WriteByte('}')
	return sb.String()
}

func nfaDebugFormatStateSet[TObservation, TStateOutcome any](
	formatter *NFADebugFormatter[TObservation, TStateOutcome],
	sorted []uint64,
) string {
	if len(sorted) == 0 {
		return "—"
	}
	if formatter != nil && formatter.FormatStateSet != nil {
		return formatter.FormatStateSet(sorted)
	}
	return nfaDebugDefaultFormatStateSet(sorted)
}

func nfaDebugStateIndicatorRune[TObservation, TStateOutcome any](
	formatter *NFADebugFormatter[TObservation, TStateOutcome],
	stateID uint64,
	outcome TStateOutcome,
) string {
	if formatter != nil && formatter.FormatStateIndicator != nil {
		s := formatter.FormatStateIndicator(stateID, outcome)
		for _, r := range s {
			return string(r)
		}
		return " "
	}
	return ""
}

func nfaDebugTruncateCell(s string, max int) string {
	if max <= 0 {
		max = 32
	}
	if len(s) <= max {
		return s
	}
	// Keep it deterministic + useful.
	// Example: "{1,2,3,4,5,6,7,8,9}" -> "{1,2,3,4,5,6,7,8…}"
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

// ------------------------------------------------------------ PUBLIC ENTRY

/*
NFADebugPrint generates a human-readable string representation of the NFA.

The output includes:
- alphabet (ID → name)
- states (outcome per state)
- starting states
- transition table (state × symbol → set-of-target-states)
- epsilon edges (per-state)

Layout is deterministic:
- starting states sorted ascending
- transition keys rendered as a dense table by symbol ID order
- epsilon edges sorted by from-state, and targets sorted ascending

Time complexity:
  - O(s * a + t log t + e log e) to build a stable table where:
    s = states, a = alphabet size, t = symbol transitions, e = epsilon edges

Space complexity:
- O(s * a) for cell buffers (string table) + temporary sorting buffers

Prerequisites:
- nfa must be a valid NFA instance

Edge cases:
  - Handles empty alphabets and state sets gracefully
  - Invalid symbol IDs are ignored in the dense table (they still exist in the map,
    but you can’t render them into the [0..alphabetSize) table deterministically)
  - If formatter is nil, uses default formatting (backward compatible)
*/
func NFADebugPrint[TObservation, TStateOutcome any](
	nfa *NFA[TObservation, TStateOutcome],
	formatter *NFADebugFormatter[TObservation, TStateOutcome],
) string {
	var sb strings.Builder

	// ============================================================
	// 1. Pre-calculation for Layout
	// ============================================================

	// State ID strings + width
	maxStateWidth := 5 // min header width
	stateStrings := make([]string, nfa.numStates)
	for s := uint64(0); s < nfa.numStates; s++ {
		stateStrings[s] = nfaDebugFormatStateID(formatter, s)
		if len(stateStrings[s]) > maxStateWidth {
			maxStateWidth = len(stateStrings[s])
		}
	}

	// Symbol ID strings + width
	alphabetSize := uint64(len(nfa.alphabet))
	symbolStrings := make([]string, alphabetSize)
	maxSymbolWidth := 3
	for id := uint64(0); id < alphabetSize; id++ {
		symbolStrings[id] = nfaDebugFormatSymbolID(formatter, id)
		if len(symbolStrings[id]) > maxSymbolWidth {
			maxSymbolWidth = len(symbolStrings[id])
		}
	}

	maxCellWidth := 32
	if formatter != nil && formatter.MaxCellWidth > 0 {
		maxCellWidth = formatter.MaxCellWidth
	}

	hasStateIndicator := formatter != nil && formatter.FormatStateIndicator != nil
	stateIndicators := make([]string, nfa.numStates)
	if hasStateIndicator {
		outCurForInd := memstruct.ArrayCursorCreate[TStateOutcome](nfa.outcomes)
		for s := uint64(0); s < nfa.numStates; s++ {
			outcome := *outCurForInd.PtrAt(s)
			stateIndicators[s] = nfaDebugStateIndicatorRune(formatter, s, outcome)
		}
	}

	// Build dense table cells (state x symbol).
	// Each cell: "—" or "{...}" already truncated.
	// Deterministic because we sort target sets.
	cells := make([]string, nfa.numStates*alphabetSize)
	for i := range cells {
		cells[i] = "—"
	}

	// Fill table from sparse map.
	if alphabetSize > 0 {
		// Collect keys deterministically (from, sym) but we will render as table anyway.
		// Still, sorting helps make any debugging of anomalies easier if you instrument.
		keys := make([][2]uint64, 0, len(nfa.transitions))
		for k := range nfa.transitions {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i][0] == keys[j][0] {
				return keys[i][1] < keys[j][1]
			}
			return keys[i][0] < keys[j][0]
		})

		for _, k := range keys {
			from := k[0]
			symID := k[1]

			if from >= nfa.numStates {
				continue
			}
			if symID >= alphabetSize {
				// Can't place into dense [0..alphabetSize) columns.
				continue
			}

			nexts := nfa.transitions[k]
			if len(nexts) == 0 {
				continue
			}

			nsCopy := make([]uint64, len(nexts))
			copy(nsCopy, nexts)
			sort.Slice(nsCopy, func(i, j int) bool { return nsCopy[i] < nsCopy[j] })

			cell := nfaDebugFormatStateSet(formatter, nsCopy)
			cell = nfaDebugTruncateCell(cell, maxCellWidth)

			cells[from*alphabetSize+symID] = cell
		}
	}

	// Determine max cell width for table alignment (bounded by maxCellWidth).
	maxRenderedCellWidth := 1
	for _, c := range cells {
		if len(c) > maxRenderedCellWidth {
			maxRenderedCellWidth = len(c)
		}
	}
	if maxRenderedCellWidth > maxCellWidth {
		maxRenderedCellWidth = maxCellWidth
	}

	// Starting states (sorted)
	startCopy := make([]uint64, len(nfa.startingStates))
	copy(startCopy, nfa.startingStates)
	sort.Slice(startCopy, func(i, j int) bool { return startCopy[i] < startCopy[j] })

	// Epsilon keys (sorted)
	epsilonKeys := make([]uint64, 0, len(nfa.epsilonEdges))
	for from := range nfa.epsilonEdges {
		epsilonKeys = append(epsilonKeys, from)
	}
	sort.Slice(epsilonKeys, func(i, j int) bool { return epsilonKeys[i] < epsilonKeys[j] })

	// ============================================================
	// 2. Header & Alphabet
	// ============================================================
	sb.WriteString("NFA Debug Print\n")
	sb.WriteString("=============================\n\n")

	sb.WriteString("Alphabet:\n")
	if alphabetSize == 0 {
		sb.WriteString("  (empty)\n")
	} else {
		for id, symDef := range nfa.alphabet {
			name := nfaDebugFormatSymbolName(formatter, uint64(id), symDef)
			sb.WriteString(fmt.Sprintf("  %s → %s\n", symbolStrings[id], name))
		}
	}
	sb.WriteString("\n")

	// ============================================================
	// 3. States (Outcome)
	// ============================================================
	sb.WriteString("States:\n")
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](nfa.outcomes)

	for s := uint64(0); s < nfa.numStates; s++ {
		outcome := *outCur.PtrAt(s)
		outStr := nfaDebugFormatOutcome(formatter, outcome)
		if hasStateIndicator {
			sb.WriteString(fmt.Sprintf("  [%s] state %*s → %s\n", stateIndicators[s], maxStateWidth, stateStrings[s], outStr))
		} else {
			sb.WriteString(fmt.Sprintf("  state %*s → %s\n", maxStateWidth, stateStrings[s], outStr))
		}
	}
	sb.WriteString("\n")

	// ============================================================
	// 4. Starting States
	// ============================================================
	sb.WriteString("Starting States:\n")
	if len(startCopy) == 0 {
		sb.WriteString("  (none)\n\n")
	} else {
		for _, st := range startCopy {
			if st < nfa.numStates {
				sb.WriteString(fmt.Sprintf("  %s\n", stateStrings[st]))
			} else {
				sb.WriteString(fmt.Sprintf("  <invalid state id=%d>\n", st))
			}
		}
		sb.WriteString("\n")
	}

	// ============================================================
	// 5. Transition Table (Symbol)
	// ============================================================
	sb.WriteString("Transitions (Symbol):\n")

	if nfa.numStates == 0 {
		sb.WriteString("  (no states)\n\n")
	} else if alphabetSize == 0 {
		sb.WriteString("  (alphabet empty)\n\n")
	} else {
		// Header row
		leadWidth := maxStateWidth + 4
		if hasStateIndicator {
			leadWidth += 3 // " [X]"
		}
		sb.WriteString(strings.Repeat(" ", leadWidth) + "|")
		for id := uint64(0); id < alphabetSize; id++ {
			sb.WriteString(fmt.Sprintf(" %*s ", maxRenderedCellWidth, symbolStrings[id]))
		}
		sb.WriteString("\n")

		// Separator
		sb.WriteString(strings.Repeat("-", leadWidth) + "+")
		for id := uint64(0); id < alphabetSize; id++ {
			sb.WriteString(strings.Repeat("-", maxRenderedCellWidth+2))
		}
		sb.WriteString("\n")

		// Rows
		for s := uint64(0); s < nfa.numStates; s++ {
			if hasStateIndicator {
				sb.WriteString(fmt.Sprintf("  [%s] %*s |", stateIndicators[s], maxStateWidth, stateStrings[s]))
			} else {
				sb.WriteString(fmt.Sprintf("  %*s |", maxStateWidth, stateStrings[s]))
			}

			rowStart := s * alphabetSize
			for symID := uint64(0); symID < alphabetSize; symID++ {
				cell := cells[rowStart+symID]
				// Ensure bounded width alignment
				if len(cell) > maxRenderedCellWidth {
					cell = nfaDebugTruncateCell(cell, maxRenderedCellWidth)
				}
				sb.WriteString(fmt.Sprintf(" %*s ", maxRenderedCellWidth, cell))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// ============================================================
	// 6. Epsilon Edges
	// ============================================================
	sb.WriteString("Epsilon Edges:\n")
	if len(epsilonKeys) == 0 {
		sb.WriteString("  (none)\n")
	} else {
		for _, from := range epsilonKeys {
			nexts := nfa.epsilonEdges[from]
			nsCopy := make([]uint64, len(nexts))
			copy(nsCopy, nexts)
			sort.Slice(nsCopy, func(i, j int) bool { return nsCopy[i] < nsCopy[j] })

			fromStr := fmt.Sprintf("%d", from)
			if from < nfa.numStates {
				fromStr = stateStrings[from]
			}

			// Format using the same state-set formatter, but keep truncation conservative.
			setStr := nfaDebugFormatStateSet(formatter, nsCopy)
			setStr = nfaDebugTruncateCell(setStr, maxCellWidth)

			sb.WriteString(fmt.Sprintf("  (%s) --[ε]--> %s\n", fromStr, setStr))
		}
	}

	return sb.String()
}

/*
NFAIndexerGet retrieves the symbol indexer function for the NFA.

The indexer maps observations to symbols, enabling the NFA to process input
and determine valid transitions.

Use cases:
- Accessing the indexer for custom processing
- Building compatible automata with the same alphabet
- Debugging symbol mapping issues

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- nfa must be a valid NFA instance
*/
func NFAIndexerGet[TObservation, TStateOutcome any](nfa *NFA[TObservation, TStateOutcome]) SymbolIndexer[TObservation] {
	return nfa.indexer
}

/*
NFAOutcomesGet retrieves the raw memory array containing state outcomes.

The returned array is indexed by state ID and contains the outcome value
for each state (e.g., token type, accept/reject flag).

Use cases:
- Inspecting state outcomes directly
- Building compatible automata
- Debugging state assignments

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- nfa must be a valid NFA instance

Edge cases:
- Returns raw memory handle - use memstruct.ArrayItemGetAtUnsafe to access
*/
func NFAOutcomesGet[TObservation, TStateOutcome any](nfa *NFA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return nfa.outcomes
}

/*
NFAStartingStatesGet retrieves the list of starting state IDs.

NFAs can have multiple starting states, unlike DFAs which have a single start.
All starting states are included in the epsilon closure at the beginning of
input processing.

Use cases:
- Understanding automaton initialization
- Building compatible automata
- Debugging start state configuration

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- nfa must be a valid NFA instance

Edge cases:
- Empty slice indicates no valid starting states
- Multiple starting states enable parallel exploration
*/
func NFAStartingStatesGet[TObservation, TStateOutcome any](nfa *NFA[TObservation, TStateOutcome]) []uint64 {
	return nfa.startingStates
}

/*
NFANumStates returns the number of states in the NFA.

Use cases:
- NFA-to-DFA conversion (subset construction)
- Allocation sizing

Time complexity: O(1)
Space complexity: O(1)
*/
func NFANumStates[TObservation, TStateOutcome any](nfa *NFA[TObservation, TStateOutcome]) uint64 {
	return nfa.numStates
}

/*
NFAAlphabetGet retrieves the alphabet (set of valid input symbols) for the NFA.

The alphabet defines all possible observations that can be processed by the
automaton. Symbol IDs correspond to indices in this slice.

Use cases:
- Building compatible automata with matching alphabets
- Understanding valid input symbols
- Debugging symbol mapping

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- nfa must be a valid NFA instance

Edge cases:
- Empty alphabet means no valid input symbols
- Alphabet order determines symbol ID assignment
*/
func NFAAlphabetGet[TObservation, TStateOutcome any](nfa *NFA[TObservation, TStateOutcome]) []SymbolDefinition[TObservation] {
	return nfa.alphabet
}

/*
NFARun processes an input sequence through the NFA and returns all possible outcomes.

The function simulates the NFA by maintaining a set of active states and computing
epsilon closures at each step. Returns all outcomes from the final state set
after processing the entire input. The client interprets which outcomes denote acceptance.

Use cases:
- Pattern matching with regular expressions
- Lexical analysis
- Language recognition

Time complexity: O(n * s * t) where n is input length, s is states, t is transitions per state
Space complexity: O(s) for active state sets

Prerequisites:
- nfa must be a valid NFA instance
- input must contain only symbols from the alphabet

Edge cases:
- Returns empty slice if no states are reached
- Returns error if invalid symbol encountered
- Empty input returns outcomes from starting states' epsilon closure
*/
func NFARun[TObservation, TStateOutcome any](nfa *NFA[TObservation, TStateOutcome], input []TObservation) ([]TStateOutcome, error) {
	current := epsilonClosure(nfa, nfa.startingStates)

	for _, observation := range input {
		symbols := nfa.indexer(observation)
		if len(symbols) == 0 {
			return nil, fmt.Errorf("invalid symbol: %v", observation)
		}

		nextSet := make(map[uint64]struct{})

		for _, state := range current {
			for _, symbol := range symbols {
				key := [2]uint64{state, symbol.SymbolID}
				for _, ns := range nfa.transitions[key] {
					nextSet[ns] = struct{}{}
				}
			}
		}

		if len(nextSet) == 0 {
			return nil, nil
		}

		current = epsilonClosure(nfa, mapKeys(nextSet))
	}

	results := make([]TStateOutcome, 0)

	outCur := memstruct.ArrayCursorCreate[TStateOutcome](nfa.outcomes)

	for _, s := range current {
		results = append(results, *outCur.PtrAt(s))
	}

	return results, nil

}

/*
NFAEpsilonClosureCompute calculates the epsilon closure of a set of states.

The epsilon closure includes all states reachable from the input states via
zero or more epsilon transitions. This is a fundamental operation in NFA
processing and NFA-to-DFA conversion.

Use cases:
- Computing reachable states before processing input
- Subset construction for NFA-to-DFA conversion
- Analyzing state reachability

Time complexity: O(s * t) where s is states, t is epsilon transitions
Space complexity: O(s) for closure set and DFS stack

Prerequisites:
- nfa must be a valid NFA instance
- states must contain valid state indices

Edge cases:
- Empty input states returns empty closure
- States with no epsilon transitions return themselves
- Handles cycles in epsilon transitions correctly
*/
func NFAEpsilonClosureCompute[TSymbol, TStateOutcome any](
	nfa *NFA[TSymbol, TStateOutcome],
	states []uint64,
) []uint64 {
	return epsilonClosure(nfa, states)
}

/*
NFATransitionsForStates computes the set of next states reachable from the given
states when processing the specified observation.

This function is used during NFA execution and NFA-to-DFA conversion to determine
which states can be reached from a set of current states with a given input symbol.

Use cases:
- NFA execution step computation
- Subset construction in NFA-to-DFA conversion
- Analyzing transition behavior

Time complexity: O(s * t) where s is input states, t is transitions per state
Space complexity: O(s) for result set

Prerequisites:
- nfa must be a valid NFA instance
- states must contain valid state indices
- observation must be in the alphabet

Edge cases:
- Returns empty slice if no transitions exist
- Returns error if observation is not in alphabet
- Deduplicates resulting states automatically
*/
func NFATransitionsForStates[TObservation, TStateOutcome any](
	nfa *NFA[TObservation, TStateOutcome],
	states []uint64,
	observation TObservation,
) ([]uint64, error) {
	symbols := nfa.indexer(observation)
	if len(symbols) == 0 {
		return nil, fmt.Errorf("invalid symbol: %v", observation)
	}

	out := make(map[uint64]struct{})

	for _, s := range states {
		for _, symbol := range symbols {
			key := [2]uint64{s, symbol.SymbolID}
			next, exists := nfa.transitions[key]
			if !exists {
				continue
			}

			for _, ns := range next {
				out[ns] = struct{}{}
			}
		}
	}

	// Convert to slice
	result := make([]uint64, 0, len(out))
	for ns := range out {
		result = append(result, ns)
	}

	return result, nil
}

// ------------------------------------------------------------ PRIVATE HELPERS

func mapKeys(m map[uint64]struct{}) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func epsilonClosure[TObservation, TStateOutcome any](
	nfa *NFA[TObservation, TStateOutcome],
	states []uint64,
) []uint64 {

	// Result set (deduplicated)
	closure := make(map[uint64]struct{}, len(states))

	// Stack for DFS/graph walk
	stack := make([]uint64, 0, len(states))

	// Initialize closure and stack with starting states
	for _, s := range states {
		if _, exists := closure[s]; !exists {
			closure[s] = struct{}{}
			stack = append(stack, s)
		}
	}

	// Explore all reachable epsilon transitions
	for len(stack) > 0 {
		// Pop last element
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		nextStates, exists := nfa.epsilonEdges[s]
		if !exists {
			continue
		}

		// Visit all epsilon-neighbors
		for _, ns := range nextStates {
			if _, seen := closure[ns]; !seen {
				closure[ns] = struct{}{}
				stack = append(stack, ns)
			}
		}
	}

	// Convert closure map to slice
	return mapKeys(closure)
}
