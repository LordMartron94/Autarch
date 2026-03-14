package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memstruct"
	"strings"
)

// ------------------------------------------------------------- DPDA STRUCT

/*
DPDA is a Deterministic Pushdown Automaton parameterized by observation and state outcome types.

Execution follows a single path: one transition per (state, input symbol, stack top). The
stack is a Go slice (no slab allocator), so there are zero manual allocations in the hot
path. The indexer must return exactly one symbol per observation. Epsilon transitions are
used for predictive expansion (e.g., LL(1)); terminal IDs are tracked so the DPDA consumes
input only when a terminal is matched.

Use cases:
- LL(1) parsing via pattern.CompileDPDA
- Expression parsers, config parsers, and syntax highlighters with unambiguous grammars

Time complexity (DPDARun): O(n) where n is input length
Space complexity: O(states + transitions); stack grows with derivation depth

Prerequisites:
- Built with DPDACreate; transitions must be deterministic (no duplicate keys, no epsilon/consuming conflict)
- terminalIDs must list all stack symbol IDs that represent terminals (for lookahead consumption)
*/
type DPDA[TObservation, TStateOutcome any] struct {
	outcomes   memcore.MarkRaw
	alphabet   []SymbolDefinition[TObservation]
	startState uint64
	indexer    SymbolIndexer[TObservation]

	transitionTable map[TransitionKey]TransitionResult
	terminalIDs     map[StackSymbolID]struct{}

	numStates       uint64
	maxEpsilonSteps uint64
}

// ------------------------------------------------------------- INITIALIZATION

/*
DPDACreate builds a DPDA from alphabet, start state, transitions, outcomes, indexer, terminal IDs, and epsilon limit.

Ensures determinism: duplicate (StateID, InputID, StackTop) keys or a conflict between an
epsilon transition and a consuming transition for the same (state, stack top) cause an error.
terminalIDs are the stack symbol IDs that represent terminals; the DPDA consumes input only
when such a symbol is matched. maxEpsilonSteps caps consecutive epsilon moves to detect cycles.

Use cases:
- Constructing a DPDA from pattern.CompileDPDA output (transitions and terminalIDs)
- Hand-built transition tables for known LL(1) grammars

Time complexity: O(t) where t is len(transitions)
Space complexity: O(states + transitions)

Prerequisites:
- len(outcomes) must cover all state IDs used in transitions
- indexer must return exactly one symbol per observation when running (DPDARun checks at runtime)

Edge cases:
- Returns error on nondeterminism (duplicate key or epsilon/consuming conflict)
*/
func DPDACreate[TObservation, TStateOutcome any](
	allocFn memarch.AllocationFn,
	alphabet []SymbolDefinition[TObservation],
	startState uint64,
	transitions []PDATransition,
	outcomes []TStateOutcome,
	indexer SymbolIndexer[TObservation],
	terminalIDs []StackSymbolID,
	maxEpsilonSteps uint64,
) (*DPDA[TObservation, TStateOutcome], error) {

	numStates := uint64(len(outcomes))
	outcomeTable, _ := memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)
	memstruct.ArraySetFromSliceUnsafe(outcomeTable, outcomes)

	transitionTable := make(map[TransitionKey]TransitionResult)
	var conflicts []DPDANondeterminismConflict

	for _, t := range transitions {
		if _, exists := transitionTable[t.Key]; exists {
			conflicts = append(conflicts, DPDANondeterminismConflict{
				StateID:  t.Key.StateID,
				InputID:  t.Key.InputID,
				StackTop: t.Key.StackTop,
			})
		} else {
			transitionTable[t.Key] = t.Result
		}
	}

	for key := range transitionTable {
		if key.InputID == EpsilonSymbolID {
			continue
		}
		epsilonKey := TransitionKey{StateID: key.StateID, InputID: EpsilonSymbolID, StackTop: key.StackTop}
		if _, conflict := transitionTable[epsilonKey]; conflict {
			conflicts = append(conflicts, DPDANondeterminismConflict(key))
		}
	}

	if len(conflicts) > 0 {
		builder := strings.Builder{}
		builder.WriteString("nondeterminism detected in DPDA transitions")
		for _, c := range conflicts {
			builder.WriteString(fmt.Sprintf(
				"\n- multiple transitions for state %d, input %d, stack %d",
				c.StateID,
				c.InputID,
				c.StackTop,
			))
		}
		return nil, &AutomatonError{
			Kind:          AutomatonErrorNondeterminismDPDA,
			Automaton:     "DPDA",
			Message:       builder.String(),
			DPDAConflicts: conflicts,
		}
	}

	termMap := make(map[StackSymbolID]struct{}, len(terminalIDs))
	for _, id := range terminalIDs {
		termMap[id] = struct{}{}
	}

	return &DPDA[TObservation, TStateOutcome]{
		outcomes:        outcomeTable,
		alphabet:        alphabet,
		startState:      startState,
		indexer:         indexer,
		transitionTable: transitionTable,
		terminalIDs:     termMap,
		numStates:       numStates,
		maxEpsilonSteps: maxEpsilonSteps,
	}, nil
}

// ------------------------------------------------------------- EXECUTION

/*
DPDARun runs the DPDA on the input and returns the outcome of the single final state, or an error.

Stack is initialized with initialStackSymbol. For each input position, epsilon moves are
exhausted (up to maxEpsilonSteps), then the current observation is mapped to one symbol via
the indexer (error if not exactly one). The transition (state, symbol, stack top) is applied;
if the stack top is a terminal, the input is consumed. After consuming all input, epsilon
closure is applied once more and the outcome at the current state is returned.

Use cases:
- Parsing a token stream produced by a lexer (e.g., Regula + DFA)
- Validating or evaluating structure in one pass

Time complexity: O(n) where n is len(input)
Space complexity: O(d) for stack where d is derivation depth

Prerequisites:
- indexer(observation) must return exactly one symbol for every observation in input

Edge cases:
- Returns error if stack empties before input is fully consumed
- Returns error if indexer returns zero or multiple symbols for an observation
- Returns error on syntax error (no transition for current state/symbol/stack)
*/
func DPDARun[TObservation, TStateOutcome any](
	dpda *DPDA[TObservation, TStateOutcome],
	input []TObservation,
	initialStackSymbol StackSymbolID,
) (*TStateOutcome, error) {

	// A single, flat, mutable stack. Cap at a reasonable initial size to avoid reallocations.
	stack := make([]StackSymbolID, 0, 128)
	stack = append(stack, initialStackSymbol)

	currentState := dpda.startState
	inputIdx := 0

	// REWRITTEN: Manual index tracking instead of a range loop
	for inputIdx < len(input) {
		// 1. Process all available epsilon moves before looking at the token
		currentState, stack = processEpsilons(dpda, currentState, stack)
		if len(stack) == 0 {
			return nil, fmt.Errorf("DPDA crashed: stack empty before input fully consumed")
		}

		// 2. Peek at the current observation (Lookahead)
		observation := input[inputIdx]
		symbols := dpda.indexer(observation)
		if len(symbols) != 1 {
			return nil, &AutomatonError{
				Kind:      AutomatonErrorIndexerAmbiguityDPDA,
				Automaton: "DPDA",
				Message: fmt.Sprintf(
					"DPDA requires unambiguous indexer, got %d symbols for observation",
					len(symbols),
				),
				DPDAContext: &DPDARuntimeContext{
					Position:    inputIdx,
					StateID:     currentState,
					StackTop:    stack[len(stack)-1],
					Observation: observation,
				},
			}
		}
		inputID := symbols[0].SymbolID

		// 3. Process the transition (Lookahead Expand OR Terminal Match)
		topSymbol := stack[len(stack)-1]
		key := TransitionKey{StateID: currentState, InputID: inputID, StackTop: topSymbol}

		result, exists := dpda.transitionTable[key]
		if !exists {
			return nil, &AutomatonError{
				Kind:      AutomatonErrorSyntaxDPDA,
				Automaton: "DPDA",
				Message: fmt.Sprintf(
					"syntax error: unexpected token %v at state %d, stack top %d",
					observation,
					currentState,
					topSymbol,
				),
				DPDAContext: &DPDARuntimeContext{
					Position:    inputIdx,
					StateID:     currentState,
					StackTop:    topSymbol,
					Observation: observation,
				},
			}
		}

		// Check if we are matching a Terminal BEFORE we alter the stack
		_, isTerminalMatch := dpda.terminalIDs[topSymbol]

		// 4. Execute the stack operation
		currentState, stack = executeStackOp(stack, result)

		// 5. Input Consumption Logic
		// If we just successfully operated on a terminal, we actually consume the token.
		// Otherwise, it was just a predictive lookahead expansion, and the token remains for the next loop.
		if isTerminalMatch {
			inputIdx++
		}
	}

	// 6. Final epsilon closure to reach the accepting state
	currentState, _ = processEpsilons(dpda, currentState, stack)

	// Fetch outcome
	outCur := memstruct.ArrayCursorCreate[TStateOutcome](dpda.outcomes)
	outcome := *outCur.PtrAt(currentState)
	return &outcome, nil
}

// ------------------------------------------------------------- STEP LOGIC

func processEpsilons[TObservation, TStateOutcome any](
	dpda *DPDA[TObservation, TStateOutcome],
	state uint64,
	stack []StackSymbolID,
) (uint64, []StackSymbolID) {

	streak := uint64(0)

	for len(stack) > 0 {
		if streak > dpda.maxEpsilonSteps {
			// Breaking loop: grammar design flaw detected
			break
		}

		topSymbol := stack[len(stack)-1]
		key := TransitionKey{StateID: state, InputID: EpsilonSymbolID, StackTop: topSymbol}

		result, exists := dpda.transitionTable[key]
		if !exists {
			break // No more epsilon moves available
		}

		state, stack = executeStackOp(stack, result)
		streak++
	}

	return state, stack
}

func executeStackOp(stack []StackSymbolID, result TransitionResult) (uint64, []StackSymbolID) {
	if len(result.PushSymbolIDs) > 0 {
		stack = stack[:len(stack)-1]
		stack = append(stack, result.PushSymbolIDs...)
	} else {
		stack = stack[:len(stack)-1]
	}
	return result.NextStateID, stack
}
