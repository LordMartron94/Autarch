package autarch

import (
	"fmt"
	"memarch"
	"memcore"
	"memstruct"
	"memstruct/extension"
	"strings"
)

/*
StackOperationKind identifies the stack operation performed by a DPDA transition.

Use cases:
- Defining push-down behavior (Push, Pop, Replace)
- Representing no stack change (NoOp)
- Stepping the DPDA and updating the stack
*/
type StackOperationKind uint8

/*
Stack operation kinds used in DPDA transitions. Each transition specifies one of:
Push (push a symbol), Pop (pop top), Replace (pop then push), or NoOp (no change).
*/
const (
	// Push pushes StackSymbol onto the stack.
	Push StackOperationKind = iota + 1
	// Pop removes the top stack symbol.
	Pop
	// Replace replaces the top stack symbol with StackSymbol.
	Replace
	// NoOp leaves the stack unchanged.
	NoOp
)

/*
StackOperation specifies the kind of stack update and, for Push and Replace, the stack symbol.

For Push and Replace, StackSymbol must be a valid symbol from the stack alphabet.
For Pop and NoOp, StackSymbol is ignored (symbolID is stored as 0 in the transition table).
*/
type StackOperation[TStackSymbol any] struct {
	Kind        StackOperationKind
	StackSymbol Symbol[TStackSymbol]
}

/*
DPDATransition defines one transition: from CurrentState with InputSymbol and CurrentStackTop,
to NextState, performing Operation on the stack.

Determinism requires at most one transition per (CurrentState, InputSymbol, CurrentStackTop).
Symbol IDs must be valid indices into the corresponding alphabet slices.
*/
type DPDATransition[TObservation, TStackSymbol any] struct {
	InputSymbol     Symbol[TObservation]
	CurrentStackTop Symbol[TStackSymbol]

	CurrentState uint64
	NextState    uint64

	Operation StackOperation[TStackSymbol]
}

// transitionValue is the value stored in the pooled transition grid (nextState, opKind, symbolID for Push/Replace).
type transitionValue struct {
	nextState uint64
	opKind    StackOperationKind
	symbolID  uint64
}

/*
DPDATransitionResult is the exported result of a transition lookup (DPDATransitionGet).
NextState is the target state; OpKind is the stack operation; PushOrReplaceSymbolID
is the stack symbol ID for Push/Replace (0 for Pop/NoOp).
*/
type DPDATransitionResult struct {
	NextState             uint64
	OpKind                StackOperationKind
	PushOrReplaceSymbolID uint64
}

/*
DPDAState holds the runtime state for stepping a DPDA: current state index and the
symbol stack. Create with DPDAStateCreate; mutate only via DPDAStep and stack
operations performed by the transition table. The stack always contains at least
the bottom-of-stack (BOS) symbol; BOS is set at create and used by Reset/ClearToBottom.

The stack stores stack-alphabet symbol IDs (indices). Peek/pop are used internally
by DPDAStep and DPDACurrentStackTop.
*/
type DPDAState struct {
	currentState        uint64
	stack               *memstruct.DynamicStack[uint64] // Stack of StackSymbols
	bottomStackSymbolID uint64
}

// --------------------------------------------------------------- PDA

/*
DPDA represents a Deterministic Pushdown Automaton.

Transitions are stored in a pooled dense local grid: one logical grid per state, keyed by
(input symbol ID, stack top symbol ID), with values encoding next state and stack operation.
Outcomes are stored per state. A bottom-of-stack (BOS) symbol ID is required: the stack
is never empty; it always contains at least BOS. This makes Peek and Replace safe.

Use cases:
- Parsing context-free languages
- Nested structure recognition (brackets, blocks)
- Deterministic PDA-based pattern matching

Time complexity:
- Lookup: O(log k) where k is the number of distinct (input, stack) pairs for that state
- Creation: O(t) where t is the number of transitions

Space complexity: O(pool sizes) — one allocation per pool (X keys, Y keys, values, bitmap), not per state.

Prerequisites:
- Input and stack symbol IDs must be less than the corresponding alphabet lengths
- At most one transition per (state, input, stack top) — duplicates cause DuplicateError at build time
- bottomStackSymbolID must be a valid stack alphabet index; the stack always contains at least BOS

Edge cases:
- numStates == 0: empty automaton (no transitions); outcomes and pool are empty
*/
type DPDA[TObservation, TStackSymbol, TStateOutcome any] struct {
	outcomes    memcore.MarkRaw // Array[TStateOutcome]
	transitions []*extension.DenseLocalGridDescriptor

	indexer SymbolIndexer[TObservation]

	inputAlphabet []SymbolDefinition[TObservation]
	stackAlphabet []SymbolDefinition[TStackSymbol]

	numStates    uint64
	alphabetSize uint64

	pooledGrid *extension.DenseLocalGridPool[uint64, uint64, transitionValue] // keys: ObservationSymbolID, StackSymbolID

	bottomStackSymbolID uint64
}

/*
DPDACreate builds a DPDA from input and stack alphabets, transitions, per-state outcomes, and a bottom-of-stack symbol ID.

It validates alphabets and symbol IDs, computes numStates from transition state indices,
builds a single DenseLocalGridPool (one grid per state, Sparse, DuplicateError), and fills outcomes.
bottomStackSymbolID must be < len(stackAlphabet); the stack will always contain at least this symbol. ε-input is not supported in v1.

Use cases:
- Constructing a DPDA for parsing or pattern matching
- Building automata from grammar or transition lists

Time complexity: O(t) where t is the number of transitions
Space complexity: O(pool sizes + numStates) for descriptors and outcomes

Prerequisites:
- inputAlphabet and stackAlphabet must pass validateAlphabet (symbol IDs match indices)
- bottomStackSymbolID must be < len(stackAlphabet)
- Every transition must use InputSymbol.SymbolID < len(inputAlphabet), CurrentStackTop.SymbolID < len(stackAlphabet)
- For Push/Replace, Operation.StackSymbol.SymbolID < len(stackAlphabet)
- If numStates > 0, len(outcomes) must equal numStates

Edge cases:
- Empty transitions: numStates == 0, pool has 0 grids, outcomes and transitions are empty
- State with no transitions: that state's grid has 0 combinations
- Duplicate (state, input, stackTop): returns error (DuplicateError policy)
- Unknown StackOperationKind: returns error
*/

func DPDACreate[TObservation, TStackSymbol, TStateOutcome any](
	allocFn memarch.AllocationFn,
	inputAlphabet []SymbolDefinition[TObservation],
	stackAlphabet []SymbolDefinition[TStackSymbol],
	bottomStackSymbolID uint64,
	transitions []DPDATransition[TObservation, TStackSymbol],
	outcomes []TStateOutcome,
	indexer SymbolIndexer[TObservation],
) (*DPDA[TObservation, TStackSymbol, TStateOutcome], error) {
	validateAlphabet(inputAlphabet, "dpda input")
	validateAlphabet(stackAlphabet, "dpda stack")

	inputSize := uint64(len(inputAlphabet))
	stackSize := uint64(len(stackAlphabet))

	if stackSize < 1 {
		return nil, fmt.Errorf("dpda: stack alphabet size must contain at least 1 entry")
	}

	if stackSize > 0 && bottomStackSymbolID >= stackSize {
		return nil, fmt.Errorf("dpda: bottomStackSymbolID %d >= stack alphabet size %d", bottomStackSymbolID, stackSize)
	}
	numStates := uint64(0)
	for _, t := range transitions {
		n := t.CurrentState + 1
		if t.NextState+1 > n {
			n = t.NextState + 1
		}
		if n > numStates {
			numStates = n
		}
		if t.InputSymbol.SymbolID >= inputSize {
			return nil, fmt.Errorf("dpda: transition input symbol ID %d >= input alphabet size %d", t.InputSymbol.SymbolID, inputSize)
		}
		if t.CurrentStackTop.SymbolID >= stackSize {
			return nil, fmt.Errorf("dpda: transition stack top symbol ID %d >= stack alphabet size %d", t.CurrentStackTop.SymbolID, stackSize)
		}
		if t.Operation.Kind == Push || t.Operation.Kind == Replace {
			if t.Operation.StackSymbol.SymbolID >= stackSize {
				return nil, fmt.Errorf("dpda: transition operation stack symbol ID %d >= stack alphabet size %d", t.Operation.StackSymbol.SymbolID, stackSize)
			}
		}
	}

	if n := uint64(len(outcomes)); n > numStates {
		numStates = n
	}

	stateBuckets := make([][]extension.Combination[uint64, uint64, transitionValue], numStates)
	for _, t := range transitions {
		xID := t.InputSymbol.SymbolID
		yID := t.CurrentStackTop.SymbolID
		var symbolID uint64
		switch t.Operation.Kind {
		case Push, Replace:
			symbolID = t.Operation.StackSymbol.SymbolID
		case Pop, NoOp:
			symbolID = 0
		default:
			return nil, fmt.Errorf("dpda: unknown stack operation kind %d", t.Operation.Kind)
		}
		val := transitionValue{
			nextState: t.NextState,
			opKind:    t.Operation.Kind,
			symbolID:  symbolID,
		}
		stateBuckets[t.CurrentState] = append(stateBuckets[t.CurrentState], extension.Combination[uint64, uint64, transitionValue]{
			XKey:  xID,
			YKey:  yID,
			Value: val,
		})
	}

	gridSpecs := make([]extension.DenseLocalGridPoolGridSpec[uint64, uint64, transitionValue], numStates)
	for state := uint64(0); state < numStates; state++ {
		gridSpecs[state] = extension.DenseLocalGridPoolGridSpec[uint64, uint64, transitionValue]{
			Combinations:    stateBuckets[state],
			Sparse:          true,
			DuplicatePolicy: extension.DenseLocalGridDuplicateError,
		}
	}

	pool, err := extension.DenseLocalGridPoolBuild(
		extension.AllocationFn(allocFn),
		func(key uint64) uint64 { return key },
		func(key uint64) uint64 { return key },
		gridSpecs,
	)
	if err != nil {
		return nil, fmt.Errorf("dpda: could not build transition table: %w", err)
	}

	transitionTable := make([]*extension.DenseLocalGridDescriptor, numStates)
	for q := uint64(0); q < numStates; q++ {
		transitionTable[q] = extension.DenseLocalGridPoolDescriptorAt(pool, q)
	}

	var outcomeMark memcore.MarkRaw
	if numStates > 0 {
		if uint64(len(outcomes)) != numStates {
			return nil, fmt.Errorf("dpda: outcomes length %d must equal numStates %d", len(outcomes), numStates)
		}
		outcomeMark, _ = memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)
		for i := uint64(0); i < numStates; i++ {
			memstruct.ArraySetAtUnsafe(outcomeMark, i, outcomes[i])
		}
	}

	return &DPDA[TObservation, TStackSymbol, TStateOutcome]{
		outcomes:            outcomeMark,
		indexer:             indexer,
		inputAlphabet:       inputAlphabet,
		stackAlphabet:       stackAlphabet,
		numStates:           numStates,
		alphabetSize:        inputSize,
		pooledGrid:          pool,
		transitions:         transitionTable,
		bottomStackSymbolID: bottomStackSymbolID,
	}, nil
}

/*
DPDAStateCreate allocates and initializes a DPDAState for the given DPDA.

The state starts in state 0 with the stack containing only the DPDA's bottom-of-stack (BOS) symbol.
The stack is grown by scratchAllocationFn; initialStackCapacity hints the initial capacity to reduce
reallocations during stepping.

Use cases:
- Initializing a run before calling DPDAStep in a loop
- Resetting the automaton for a new input without reallocating the DPDA

Time complexity: O(1)
Space complexity: O(initialStackCapacity) for the initial stack buffer

Prerequisites:
- dpda must be a valid DPDA from DPDACreate
- scratchAllocationFn must remain valid for the state's lifetime

Edge cases:
- initialStackCapacity 0: stack still valid; will grow on first push
*/
func DPDAStateCreate[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	scratchAllocationFn memarch.AllocationFn,
	initialStackCapacity uint64,
) *DPDAState {
	stack := memstruct.DynamicStackCreate[uint64](
		memstruct.DynamicStackAllocationFn(scratchAllocationFn),
		func(currentCap, neededCap uint64) uint64 {
			newSize := max(currentCap*2, neededCap)
			return newSize
		},
		initialStackCapacity,
	)
	if err := memstruct.DynamicStackPush(stack, dpda.bottomStackSymbolID); err != nil {
		panic(err)
	}
	return &DPDAState{
		currentState:        0,
		stack:               stack,
		bottomStackSymbolID: dpda.bottomStackSymbolID,
	}
}

/*
DPDAStateReset clears the stack to only the bottom-of-stack symbol and sets the current state.

If keepCapacity is true, the stack buffer is not shrunk (only logical length is cleared).
The DPDA package does not expose stack buffer shrinking; keepCapacity is reserved for future use.
*/
func DPDAStateReset(state *DPDAState, startState uint64, keepCapacity bool) {
	_ = keepCapacity
	memstruct.DynamicStackClear(state.stack)
	if err := memstruct.DynamicStackPush(state.stack, state.bottomStackSymbolID); err != nil {
		panic(err)
	}
	state.currentState = startState
}

/*
DPDAStateClone allocates a new DPDAState with the same current state and stack contents,
using the given scratch allocator. The clone shares no memory with the original.
Uses stack primitives only (StackContentsCopy); no heap allocation.
Useful for backtracking and debugging.
*/
func DPDAStateClone(state *DPDAState, scratchAllocationFn memarch.AllocationFn) *DPDAState {
	cap := memstruct.DynamicStackCapacityGet(state.stack)
	cloneStack := memstruct.DynamicStackCreate[uint64](
		memstruct.DynamicStackAllocationFn(scratchAllocationFn),
		func(currentCap, neededCap uint64) uint64 {
			newSize := max(currentCap*2, neededCap)
			return newSize
		},
		cap,
	)
	if err := memstruct.StackContentsCopy[uint64](
		memstruct.DynamicStackStackMarkGet(cloneStack),
		memstruct.DynamicStackStackMarkGet(state.stack),
	); err != nil {
		panic(err)
	}
	return &DPDAState{
		currentState:        state.currentState,
		stack:               cloneStack,
		bottomStackSymbolID: state.bottomStackSymbolID,
	}
}

/*
DPDAStep performs one transition: maps the observation to a single input symbol via
the DPDA's indexer, looks up the transition for (currentState, inputSymbol, stackTop),
applies the stack operation, updates state.currentState to the next state, and returns that next state.

The indexer must return exactly one symbol for the observation (deterministic input
symbol). Panics if the observation maps to zero or multiple symbols. Returns an error
if the observation maps to no symbol. The stack always contains at least BOS, so Peek is safe.

Use cases:
- Driving a DPDA over a stream of observations (tokens, runes, etc.)
- Parsing nested structures where the stack tracks nesting depth or context

Time complexity: O(log k) for transition lookup, where k is the number of distinct
(input, stack) pairs for the current state; O(1) for stack push/pop/replace
Space complexity: O(1) for step; stack may grow on Push

Prerequisites:
  - state must have been created with DPDAStateCreate for this dpda
  - observation must be in the input alphabet (indexer returns exactly one symbol)
  - A transition must exist for (currentState, inputSymbol, stackTop); otherwise the
    implementation panics (invalid DPDA configuration or missing transition)

Edge cases:
  - After step, nextState may have no outgoing transitions or may be accepting; use
    DPDAOutcome and client-defined isAccepting to interpret.
*/
func DPDAStep[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	state *DPDAState,
	observation TObservation,
) (nextState uint64, err error) {
	stackTopSymbolID := memstruct.DynamicStackPeekUnsafe(state.stack)

	symbols := dpda.indexer(observation)
	if len(symbols) == 0 {
		var zero uint64
		return zero, fmt.Errorf("invalid symbol: %v", observation)
	}

	if len(symbols) != 1 {
		panic(fmt.Errorf(
			"DPDA invariant violated: observation %v matched %d symbols: %v",
			observation,
			len(symbols),
			symbols,
		))
	}

	symbol := symbols[0]
	transition := getTransitionForState(dpda, state.currentState, symbol.SymbolID, stackTopSymbolID)

	applyTransition(state, transition)

	state.currentState = transition.nextState
	return transition.nextState, nil
}

/*
DPDACurrentStackTop returns the stack-alphabet symbol (observation value) at the
top of the state's stack, without popping. The stack always contains at least BOS,
so the stack is never empty after create/reset.

Use cases:
- Inspecting current context during parsing (e.g. which bracket or block is open)
- Debugging or diagnostics

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- state must have been created for this dpda (stack contains at least BOS)
*/
func DPDACurrentStackTop[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	state *DPDAState,
) TStackSymbol {
	stackTopSymbolID := memstruct.DynamicStackPeekUnsafe(state.stack)
	stackTopSymbol := dpda.stackAlphabet[stackTopSymbolID]
	return *stackTopSymbol.Observation
}

/*
DPDANumStates returns the number of states in the DPDA.
*/
func DPDANumStates[TObservation, TStackSymbol, TStateOutcome any](dpda *DPDA[TObservation, TStackSymbol, TStateOutcome]) uint64 {
	return dpda.numStates
}

/*
DPDAInputAlphabet returns the input alphabet (symbol definitions) for the DPDA.
*/
func DPDAInputAlphabet[TObservation, TStackSymbol, TStateOutcome any](dpda *DPDA[TObservation, TStackSymbol, TStateOutcome]) []SymbolDefinition[TObservation] {
	return dpda.inputAlphabet
}

/*
DPDAStackAlphabet returns the stack alphabet (symbol definitions) for the DPDA.
*/
func DPDAStackAlphabet[TObservation, TStackSymbol, TStateOutcome any](dpda *DPDA[TObservation, TStackSymbol, TStateOutcome]) []SymbolDefinition[TStackSymbol] {
	return dpda.stackAlphabet
}

/*
DPDAOutcome returns the outcome for the given state ID. Returns (zero, false) if stateID is out of range.
*/
func DPDAOutcome[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	stateID uint64,
) (TStateOutcome, bool) {
	if stateID >= dpda.numStates {
		var zero TStateOutcome
		return zero, false
	}
	outcome := memstruct.ArrayItemGetAtUnsafe[TStateOutcome](dpda.outcomes, stateID)
	return outcome, true
}

/*
DPDAIsAccepting returns whether the given state is accepting according to the client-provided callback.
*/
func DPDAIsAccepting[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	stateID uint64,
	isAccepting func(TStateOutcome) bool,
) bool {
	outcome, ok := DPDAOutcome(dpda, stateID)
	if !ok {
		return false
	}
	return isAccepting(outcome)
}

/*
DPDAIsAcceptingStateAndStackDepthOne returns true iff the state is in an accepting outcome and the
stack depth is 1 (only BOS remains). Use this for nested-structure DPDAs (e.g. from Vistra) where
full acceptance requires both an accepting state and a closed stack.
*/
func DPDAIsAcceptingStateAndStackDepthOne[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	state *DPDAState,
	isAccepting func(TStateOutcome) bool,
) bool {
	if memstruct.DynamicStackLengthGet(state.stack) != 1 {
		return false
	}
	return DPDAIsAccepting(dpda, state.currentState, isAccepting)
}

/*
DPDATransitionGet returns the transition for (q, inputID, stackTopID) if it exists. Zero-allocation introspection.
*/
func DPDATransitionGet[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	q, inputID, stackTopID uint64,
) (DPDATransitionResult, bool) {
	tv, ok := getTransitionForStateOptional(dpda, q, inputID, stackTopID)
	if !ok {
		return DPDATransitionResult{}, false
	}
	return DPDATransitionResult{
		NextState:             tv.nextState,
		OpKind:                tv.opKind,
		PushOrReplaceSymbolID: tv.symbolID,
	}, true
}

/*
DPDATryStep performs one transition like DPDAStep but does not panic when no transition exists:
returns ok=false and err set when the observation has no symbol or multiple symbols, or when no transition exists.
State is not mutated when ok is false.
*/
func DPDATryStep[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	state *DPDAState,
	observation TObservation,
) (ok bool, err error) {
	stackTopSymbolID := memstruct.DynamicStackPeekUnsafe(state.stack)
	symbols := dpda.indexer(observation)
	if len(symbols) == 0 {
		return false, fmt.Errorf("invalid symbol: %v", observation)
	}
	if len(symbols) != 1 {
		return false, fmt.Errorf("observation %v matched %d symbols", observation, len(symbols))
	}
	symbol := symbols[0]
	tv, ok := getTransitionForStateOptional(dpda, state.currentState, symbol.SymbolID, stackTopSymbolID)
	if !ok {
		return false, nil
	}
	applyTransition(state, tv)
	state.currentState = tv.nextState
	return true, nil
}

/*
DPDAStepSymbol performs one transition by input symbol ID only (no indexer). Returns true if a transition existed and state was updated.
*/
func DPDAStepSymbol[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	state *DPDAState,
	inputSymbolID uint64,
) bool {
	stackTopSymbolID := memstruct.DynamicStackPeekUnsafe(state.stack)
	tv, ok := getTransitionForStateOptional(dpda, state.currentState, inputSymbolID, stackTopSymbolID)
	if !ok {
		return false
	}
	applyTransition(state, tv)
	state.currentState = tv.nextState
	return true
}

/*
DPDAStackTopID returns the symbol ID at the top of the stack and true, or (0, false) if the stack is empty.
With BOS, the stack is never empty after create/reset.
*/
func DPDAStackTopID(state *DPDAState) (id uint64, ok bool) {
	id, err := memstruct.DynamicStackPeek(state.stack)
	return id, err == nil
}

/*
DPDAStackDepth returns the number of elements on the stack (including BOS).
*/
func DPDAStackDepth(state *DPDAState) uint64 {
	return memstruct.DynamicStackLengthGet(state.stack)
}

/*
DPDAStackClearToBottom clears the stack and pushes the bottom-of-stack symbol so the stack has exactly one element.
*/
func DPDAStackClearToBottom(state *DPDAState) {
	memstruct.DynamicStackClear(state.stack)
	if err := memstruct.DynamicStackPush(state.stack, state.bottomStackSymbolID); err != nil {
		panic(err)
	}
}

/*
DPDAStackPushID pushes a stack symbol ID onto the state's stack.
*/
func DPDAStackPushID(state *DPDAState, id uint64) error {
	return memstruct.DynamicStackPush(state.stack, id)
}

/*
DPDAStackPopID pops the top stack symbol ID. With BOS, popping the last (BOS) element is illegal and returns an error.
*/
func DPDAStackPopID(state *DPDAState) (id uint64, err error) {
	return memstruct.DynamicStackPop(state.stack)
}

/*
DPDAAvailableInputs returns the set of input symbol IDs that have a transition from state q with the given stack top.
Used for diagnostics and expected-token reporting.
*/
func DPDAAvailableInputs[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	q, stackTopID uint64,
) []uint64 {
	var out []uint64
	for inputID := uint64(0); inputID < dpda.alphabetSize; inputID++ {
		if _, ok := DPDATransitionGet(dpda, q, inputID, stackTopID); ok {
			out = append(out, inputID)
		}
	}
	return out
}

/*
DPDADebugFormatter provides optional formatting for DPDA debug output (symbol names, state IDs, outcomes, stack op kind).
*/
type DPDADebugFormatter[TObservation, TStackSymbol, TStateOutcome any] struct {
	FormatInputSymbolName func(id uint64, def SymbolDefinition[TObservation]) string
	FormatStackSymbolName func(id uint64, def SymbolDefinition[TStackSymbol]) string
	FormatStateOutcome    func(outcome TStateOutcome) string
	FormatStateID         func(stateID uint64) string
	FormatOpKind          func(kind StackOperationKind) string
}

/*
DPDADebugPrint generates a human-readable string representation of the DPDA (alphabets, state outcomes, transitions).
*/
func DPDADebugPrint[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	formatter *DPDADebugFormatter[TObservation, TStackSymbol, TStateOutcome],
) string {
	var sb strings.Builder
	sb.WriteString("DPDA Debug Print\n")
	sb.WriteString("=============================\n\n")

	sb.WriteString("Input alphabet:\n")
	for id, def := range dpda.inputAlphabet {
		name := def.Name
		if formatter != nil && formatter.FormatInputSymbolName != nil {
			name = formatter.FormatInputSymbolName(uint64(id), def)
		}
		sb.WriteString(fmt.Sprintf("  %d: %s\n", id, name))
	}
	sb.WriteString("\nStack alphabet:\n")
	for id, def := range dpda.stackAlphabet {
		name := def.Name
		if formatter != nil && formatter.FormatStackSymbolName != nil {
			name = formatter.FormatStackSymbolName(uint64(id), def)
		}
		sb.WriteString(fmt.Sprintf("  %d: %s\n", id, name))
	}
	sb.WriteString(fmt.Sprintf("\nBOS symbol ID: %d\n\n", dpda.bottomStackSymbolID))

	sb.WriteString("States (outcome):\n")
	for s := uint64(0); s < dpda.numStates; s++ {
		outcome, _ := DPDAOutcome(dpda, s)
		outStr := fmt.Sprintf("%v", outcome)
		if formatter != nil && formatter.FormatStateOutcome != nil {
			outStr = formatter.FormatStateOutcome(outcome)
		}
		stateStr := fmt.Sprintf("%d", s)
		if formatter != nil && formatter.FormatStateID != nil {
			stateStr = formatter.FormatStateID(s)
		}
		sb.WriteString(fmt.Sprintf("  %s → %s\n", stateStr, outStr))
	}
	sb.WriteString("\nTransitions:\n")
	for q := uint64(0); q < dpda.numStates; q++ {
		descriptor := dpda.transitions[q]
		if descriptor == nil {
			continue
		}
		extension.DenseLocalGridPooledIterateValid(dpda.pooledGrid, descriptor, func(xID, yID uint64, val transitionValue) bool {
			opStr := fmt.Sprintf("%v", val.opKind)
			if formatter != nil && formatter.FormatOpKind != nil {
				opStr = formatter.FormatOpKind(val.opKind)
			}
			sb.WriteString(fmt.Sprintf("  (%d, %d, %d) → state %d, %s", q, xID, yID, val.nextState, opStr))
			if val.opKind == Push || val.opKind == Replace {
				sb.WriteString(fmt.Sprintf(" %d", val.symbolID))
			}
			sb.WriteString("\n")
			return true
		})
	}
	return sb.String()
}

/*
DPDAValidate checks basic DPDA invariants (BOS valid, etc.). Determinism and transition validity are enforced at build time.
*/
func DPDAValidate[TObservation, TStackSymbol, TStateOutcome any](dpda *DPDA[TObservation, TStackSymbol, TStateOutcome]) error {
	stackSize := uint64(len(dpda.stackAlphabet))
	if stackSize > 0 && dpda.bottomStackSymbolID >= stackSize {
		return fmt.Errorf("dpda: bottomStackSymbolID %d >= stack alphabet size %d", dpda.bottomStackSymbolID, stackSize)
	}
	return nil
}

/*
DPDARun resets state to initialState then steps through observations. Returns final state and ok=true, or ok=false and err if a step fails.
*/
func DPDARun[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	initialState uint64,
	state *DPDAState,
	observations []TObservation,
) (finalState uint64, ok bool, err error) {
	DPDAStateReset(state, initialState, true)
	for _, obs := range observations {
		stepOk, stepErr := DPDATryStep(dpda, state, obs)
		if stepErr != nil {
			return 0, false, stepErr
		}
		if !stepOk {
			return 0, false, nil
		}
	}
	return state.currentState, true, nil
}

/*
DPDARunAndAccept runs the DPDA and returns whether the final state is accepting according to isAccepting(outcome).
*/
func DPDARunAndAccept[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	initialState uint64,
	state *DPDAState,
	observations []TObservation,
	isAccepting func(TStateOutcome) bool,
) (accepted bool, err error) {
	finalState, ok, err := DPDARun(dpda, initialState, state, observations)
	if err != nil || !ok {
		return false, err
	}
	outcome, _ := DPDAOutcome(dpda, finalState)
	return isAccepting(outcome), nil
}

// --------------------------------------------------------------- PRIVATE HELPERS

func getTransitionForState[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	currentState uint64,
	observationSymbolID uint64,
	stackSymbolID uint64,
) transitionValue {
	descriptor := dpda.transitions[currentState]
	transition, ok := extension.DenseLocalGridPooledValueGetByID(dpda.pooledGrid, descriptor, observationSymbolID, stackSymbolID)
	if !ok {
		panic(fmt.Errorf("(currentState=%d, observation=%d, stack=%d) resulted in nil-transition", currentState, observationSymbolID, stackSymbolID))
	}

	return transition
}

func getTransitionForStateOptional[TObservation, TStackSymbol, TStateOutcome any](
	dpda *DPDA[TObservation, TStackSymbol, TStateOutcome],
	currentState uint64,
	observationSymbolID uint64,
	stackSymbolID uint64,
) (transitionValue, bool) {
	if currentState >= dpda.numStates {
		var zero transitionValue
		return zero, false
	}
	descriptor := dpda.transitions[currentState]
	return extension.DenseLocalGridPooledValueGetByID(dpda.pooledGrid, descriptor, observationSymbolID, stackSymbolID)
}

func applyTransition(
	state *DPDAState,
	transition transitionValue,
) {
	switch transition.opKind {
	case Push:
		if err := memstruct.DynamicStackPush(state.stack, transition.symbolID); err != nil {
			panic(err)
		}
	case Pop:
		if memstruct.DynamicStackLengthGet(state.stack) < 2 {
			return
		}

		if _, err := memstruct.DynamicStackPop(state.stack); err != nil {
			panic(err)
		}
	case Replace:
		if memstruct.DynamicStackLengthGet(state.stack) > 1 {
			if _, err := memstruct.DynamicStackPop(state.stack); err != nil {
				panic(err)
			}

			if err := memstruct.DynamicStackPush(state.stack, transition.symbolID); err != nil {
				panic(err)
			}
		}
	case NoOp:
		return
	default:
		panic(fmt.Errorf("unknown opKind %T", transition.opKind))
	}
}
