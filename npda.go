package autarch

import (
	"fmt"
	"foundation/bytes"
	"foundation/hash"
	"math"
	"memarch"
	"memcore"
	"memforge"
	"memstruct"
)

var xxh3Hasher = hash.XXH3HasherCreateWithSeed(6789)

/*
EpsilonSymbolID is the reserved symbol ID denoting an epsilon (empty) transition.

Epsilon transitions consume no input and allow the automaton to change state and
stack without advancing the input stream. Used as InputID in TransitionKey for
epsilon moves.

Time complexity: O(1)
Space complexity: O(1)

Edge cases:
- Must not be used as a normal alphabet symbol ID
- Comparisons use math.MaxUint64 for uniqueness
*/
const EpsilonSymbolID uint64 = math.MaxUint64

// ------------------------------------------------------------- TYPES

/*
StackSymbolID identifies a symbol stored on the NPDA stack.

Stack symbols are distinct from input alphabet symbols; they drive stack-based
grammar recognition (e.g., matching parentheses, nested structures). Valid IDs
are defined by the grammar; EpsilonSymbolID is not used as a stack symbol.

Time complexity: O(1) for all operations
Space complexity: O(1)
*/
type StackSymbolID uint64

/*
StackOperation specifies how a transition modifies the stack.

Use cases:
- STACK_POP: Pop top symbol (e.g., reduce in parsing)
- STACK_PUSH: Push a new symbol (e.g., shift or open a nested construct)
- STACK_REPLACE: Pop then push (replace top without changing depth semantics)

Time complexity: O(1)
Space complexity: O(1)
*/
type StackOperation uint8

const (
	STACK_POP StackOperation = iota
	STACK_PUSH
	STACK_REPLACE
)

/*
StackNode is a single node in the NPDA's manually managed stack linked list.

Each node holds a StackSymbolID and a mark to the next node. The stack is
allocated from a slab allocator for zero-allocation execution; traversal is
via MemcoreMarkDereferenceObject and the Next field.

Time complexity: O(1) for access
Space complexity: O(1) per node

Prerequisites:
- Next must be a valid MarkRaw (or invalid for bottom of stack)
- Nodes must only be mutated by NPDA internals
*/
type StackNode struct {
	SymbolID StackSymbolID
	Next     memcore.MarkRaw
}

/*
TransitionKey uniquely identifies a transition entry: current state, input symbol, and stack top.

The combination (StateID, InputID, StackTop) keys into the transition table.
InputID may be EpsilonSymbolID for epsilon moves.

Time complexity: O(1) for map lookup
Space complexity: O(1)
*/
type TransitionKey struct {
	StateID  uint64
	InputID  uint64
	StackTop StackSymbolID
}

/*
TransitionResult describes the effect of one transition: next state and stack operation.

NextStateID is the state after the transition. When PushSymbolIDs is non-empty
(one or more symbols), pop once then push each in order so that top of stack
equals the last element. When PushSymbolIDs is nil or empty, StackOp must be
STACK_POP (pop only).

Time complexity: O(len(PushSymbolIDs)) when PushSymbolIDs is used
Space complexity: O(1) plus slice when PushSymbolIDs is set
*/
type TransitionResult struct {
	NextStateID   uint64
	StackOp       StackOperation
	PushSymbolIDs []StackSymbolID // If non-empty, pop once then push all; top = last element (1 symbol = slice of 1)
}

/*
PDATransition represents one edge in the NPDA transition graph.

Each transition maps a (state, input, stack top) key to a result (next state and
stack operation). Multiple PDATransition values may share the same Key (nondeterminism).

Use cases:
- Building an NPDA from a grammar or compiler-generated table
- Serialization and code generation

Time complexity: O(1) per transition
Space complexity: O(1) per transition
*/
type PDATransition struct {
	Key    TransitionKey
	Result TransitionResult
}

/*
NPDAConfig represents a single active configuration during NPDA execution.

A configuration is a (state, stack) pair. StateID is the current state; StackTop
and StackDepth represent the stack (linked list head and depth). EpsilonStreak
tracks consecutive epsilon moves to enforce maxEpsilonSteps and break cycles.

Use cases:
- Tracing and debugging execution
- Inspecting stack via NPDAConfigStackRead and NPDAConfigFormat

Time complexity: O(1) for field access
Space complexity: O(1) plus stack nodes held by allocator

Prerequisites:
- StackTop must be from the same NPDA's slab allocator
- StackDepth must match actual stack length
*/
type NPDAConfig struct {
	StateID       uint64
	StackTop      memcore.MarkRaw
	StackDepth    uint64
	EpsilonStreak uint64 // Tracks consecutive epsilon moves to break non-pushing loops
}

// ------------------------------------------------------------- NPDA STRUCT

/*
NPDA is a Nondeterministic Pushdown Automaton parameterized by observation and state outcome types.

An NPDA extends an NFA with a stack, enabling recognition of context-free languages.
Execution maintains a set of (state, stack) configurations; each input observation
may trigger multiple transitions (nondeterminism) and epsilon moves. Outcomes are
collected from all configurations that remain after consuming the full input.

Use cases:
- Parsing context-free grammars (e.g., expression parsers, syntax highlighters)
- Nested structure recognition (parentheses, brackets, blocks)
- Sublime-style syntax definitions and compiler backends

Time complexity (NPDARun): O(n * b * (s + e)) where n is input length, b is branch count,
s is transitions per config, e is epsilon closure size
Space complexity: O(states + transitions + maxStackNodes); stack uses slab allocator

Prerequisites:
- Built with NPDACreate; alphabet, starting states, transitions, and outcomes must be consistent
- indexer must return valid symbol IDs for all observations that will be fed to NPDARun
- Defensive limits (maxStackDepth, maxBranches, maxEpsilonSteps) must be set to avoid runaway execution
*/
type NPDA[TObservation, TStateOutcome any] struct {
	outcomes                 memcore.MarkRaw
	alphabet                 []SymbolDefinition[TObservation]
	startingStates           []uint64
	nondeterministicResolver NondeterministicSymbolResolver[TObservation]

	transitionTable map[TransitionKey][]TransitionResult
	numStates       uint64

	stackAllocator memcore.MarkRaw

	// Defensive limits to prevent memory exhaustion
	maxStackDepth   uint64
	maxBranches     uint64
	maxEpsilonSteps uint64
}

// ------------------------------------------------------------- INITIALIZATION & TEARDOWN

/*
NPDACreate constructs an NPDA from alphabet, starting states, transitions, and state outcomes.

allocFn is used to allocate the outcome array and (indirectly) the slab allocator for
stack nodes. alphabet and indexer define how observations map to symbol IDs.
startingStates and transitions define the transition graph; outcomes is a slice
indexed by state ID (len(outcomes) = numStates). maxStackNodes, maxStackDepth,
maxBranches, and maxEpsilonSteps are defensive limits to prevent unbounded memory or loops.

Use cases:
- Building an NPDA from a grammar compiler or hand-written transition table
- One-time setup before repeated NPDARun calls

Time complexity: O(t) where t is len(transitions); map insertion and slice copy
Space complexity: O(states + transitions + maxStackNodes)

Prerequisites:
- allocFn must remain valid for the NPDA lifetime
- len(outcomes) must equal the maximum state ID plus one (states are 0..numStates-1)
- All transition StateID and NextStateID must be < len(outcomes)
- startingStates must contain valid state IDs
- Symbol IDs in transitions must be valid (alphabet size or EpsilonSymbolID for input)

Edge cases:
- Multiple transitions with same Key add multiple results (nondeterminism)
- NPDADestroy must be called to release slab allocator when the NPDA is discarded
*/
func NPDACreate[TObservation, TStateOutcome any](
	allocFn memarch.AllocationFn,
	alphabet []SymbolDefinition[TObservation],
	startingStates []uint64,
	transitions []PDATransition,
	outcomes []TStateOutcome,
	resolver NondeterministicSymbolResolver[TObservation],
	maxStackNodes uint64,
	maxStackDepth uint64,
	maxBranches uint64,
	maxEpsilonSteps uint64,
) *NPDA[TObservation, TStateOutcome] {

	numStates := uint64(len(outcomes))
	outcomeTable, _ := memarch.MemArchArrayCreate[TStateOutcome](allocFn, numStates)
	memstruct.ArraySetFromSliceUnsafe(outcomeTable, outcomes)

	transitionTable := make(map[TransitionKey][]TransitionResult)

	for _, transition := range transitions {
		key := transition.Key
		transitionTable[key] = append(transitionTable[key], transition.Result)
	}

	stackAllocator := memforge.SlabAllocatorCreate[StackNode](maxStackNodes, "autarch npda stack")

	return &NPDA[TObservation, TStateOutcome]{
		outcomes:                 outcomeTable,
		alphabet:                 alphabet,
		startingStates:           startingStates,
		nondeterministicResolver: resolver,
		transitionTable:          transitionTable,
		numStates:                numStates,
		stackAllocator:           stackAllocator,
		maxStackDepth:            maxStackDepth,
		maxBranches:              maxBranches,
		maxEpsilonSteps:          maxEpsilonSteps,
	}
}

/*
NPDADestroy releases resources owned by the NPDA, including the stack node slab allocator.

Call when the NPDA is no longer needed. The NPDA pointer itself is not cleared;
the caller must not use it after destroy. Outcome and transition memory are not
freed here (they were allocated by the caller's allocFn).

Time complexity: O(1) with allocator teardown
Space complexity: O(1)

Prerequisites:
- npda must have been created with NPDACreate
- Must not be called concurrently with NPDARun or NPDAIterateTransitions
*/
func NPDADestroy[TObservation, TStateOutcome any](npda *NPDA[TObservation, TStateOutcome]) {
	memforge.SlabAllocatorDestroy[StackNode](npda.stackAllocator)
}

/*
NPDAAlphabetGet returns the alphabet definitions (symbol ID, name, match predicate) for the NPDA.

Use cases:
- Debugging and error messages (symbol names)
- Building a compatible indexer or validating observations

Time complexity: O(1)
Space complexity: O(1) — returns slice held by NPDA
*/
func NPDAAlphabetGet[TObservation, TStateOutcome any](npda *NPDA[TObservation, TStateOutcome]) []SymbolDefinition[TObservation] {
	return npda.alphabet
}

/*
NPDAStartingStatesGet returns the list of state IDs from which execution may begin.

Use cases:
- Debugging and visualization
- Checking that the NPDA has at least one start state

Time complexity: O(1)
Space complexity: O(1)
*/
func NPDAStartingStatesGet[TObservation, TStateOutcome any](npda *NPDA[TObservation, TStateOutcome]) []uint64 {
	return npda.startingStates
}

/*
NPDANumStates returns the total number of states (length of the outcome array).

Use cases:
- Bounds checking and validation
- Iterating over state IDs in [0, NPDANumStates)

Time complexity: O(1)
Space complexity: O(1)
*/
func NPDANumStates[TObservation, TStateOutcome any](npda *NPDA[TObservation, TStateOutcome]) uint64 {
	return npda.numStates
}

/*
NPDAOutcomesGet returns the raw memory array (MarkRaw) containing state outcomes.

State outcomes are indexed by state ID; use memstruct.ArrayItemGetAtUnsafe to read
TStateOutcome values. Do not modify the underlying memory.

Use cases:
- Custom outcome collection or filtering
- Integration with code that expects raw array access

Time complexity: O(1)
Space complexity: O(1)

Prerequisites:
- Outcome array was created with NPDACreate; element count equals NPDANumStates
*/
func NPDAOutcomesGet[TObservation, TStateOutcome any](npda *NPDA[TObservation, TStateOutcome]) memcore.MarkRaw {
	return npda.outcomes
}

/*
NPDAConfigStackRead converts the configuration's stack (linked list) into a slice of stack symbol IDs.

Order is top-first (index 0 = top of stack). Allocates a new slice; do not use in the
hot path. Intended for debugging, logging, or tracing execution paths.

Use cases:
- Human-readable stack dumps
- Serializing execution traces
- Debugging grammar and transition issues

Time complexity: O(d) where d is config.StackDepth
Space complexity: O(d) for the returned slice

Prerequisites:
- config must be a valid NPDAConfig from the same NPDA (stack nodes still valid)
Edge cases:
- Returns nil if config has no stack (StackTop invalid)
*/
func NPDAConfigStackRead(config NPDAConfig) []StackSymbolID {
	if !memcore.MemcoreMarkIsValid(config.StackTop) {
		return nil
	}

	// Allocate precisely using the depth tracker to avoid slice append overhead
	symbols := make([]StackSymbolID, config.StackDepth)
	currentMark := config.StackTop

	for i := uint64(0); i < config.StackDepth; i++ {
		node := memcore.MemcoreMarkDereferenceObject[StackNode](currentMark)
		symbols[i] = node.SymbolID
		currentMark = node.Next
	}

	return symbols
}

/*
NPDAConfigFormat returns a human-readable string for a single execution configuration.

Format includes state ID, stack depth, and stack contents (top-first) via NPDAConfigStackRead.
Use for logging and debugging only.

Time complexity: O(d) where d is config.StackDepth
Space complexity: O(d) for stack slice used in formatting
*/
func NPDAConfigFormat(config NPDAConfig) string {
	stackSlice := NPDAConfigStackRead(config)
	return fmt.Sprintf("State: %d | Stack Depth: %d | Stack: %v", config.StateID, config.StackDepth, stackSlice)
}

/*
NPDAIterateTransitions walks the static transition graph, invoking fn for each (key, results) pair.

The callback receives every unique (StateID, InputID, StackTop) key and the slice of
TransitionResult values (multiple results = nondeterminism). If fn returns false,
iteration stops.

Use cases:
- Serializing the NPDA into target configurations (e.g., Sublime Text syntaxes)
- Generating DOT graphs for Graphviz visualization
- Static analysis of reachability or dead states

Time complexity: O(t) where t is number of distinct keys in the transition table
Space complexity: O(1) aside from callback use

Prerequisites:
- npda must be a valid NPDA; transition table must not be modified during iteration

Edge cases:
- Iteration order is unspecified (map iteration)
- Epsilon transitions appear with InputID == EpsilonSymbolID
*/
func NPDAIterateTransitions[TObservation, TStateOutcome any](
	npda *NPDA[TObservation, TStateOutcome],
	fn func(key TransitionKey, results []TransitionResult) bool,
) {
	for key, results := range npda.transitionTable {
		if !fn(key, results) {
			break
		}
	}
}

/*
NPDARun executes the NPDA on the given input and returns all distinct state outcomes from accepting configurations.

Execution starts from each starting state with a stack containing only initialStackSymbol.
For each observation, the current configuration set is closed under epsilon moves, then
advanced by consuming the observation (indexer maps observation to symbol IDs). Configurations
that cannot consume the symbol are dropped. After the full input is consumed, epsilon closure
is applied again and outcomes are collected from all remaining configurations (by state ID, deduplicated).

Use cases:
- Parsing a token stream or character sequence with a context-free grammar
- Syntax highlighting and structural analysis
- Validation of nested structures

Time complexity: O(n * b * (s + e)) where n = len(input), b = active config count, s = transitions per config, e = epsilon closure size
Space complexity: O(b * stack_depth) during run; stack uses slab allocator reset at start

Prerequisites:
- npda must be created with NPDACreate; stack allocator is reset at entry
- indexer(observation) must return at least one symbol for every observation in input (else error)
- initialStackSymbol must be a valid stack symbol for the grammar

Edge cases:
- Returns (nil, nil) if no configurations remain after some input step (input rejected)
- Returns error if indexer returns no symbols for an observation
- Returns error if active config count exceeds maxBranches
- Outcomes may be empty if no configurations reach an outcome state; order of outcomes is unspecified
*/
func NPDARun[TObservation, TStateOutcome any](
	npda *NPDA[TObservation, TStateOutcome],
	input []TObservation,
	initialStackSymbol StackSymbolID,
) ([]TStateOutcome, error) {

	memforge.SlabAllocatorReset[StackNode](npda.stackAllocator)

	activeConfigs := initializeConfigs(npda, initialStackSymbol)

	for idx, observation := range input {
		activeConfigs = computeEpsilonClosure(npda, activeConfigs)

		if len(activeConfigs) == 0 {
			return nil, nil
		}

		var err error
		activeConfigs, err = stepAllConfigs(npda, activeConfigs, observation)
		if err != nil {
			return nil, err
		}

		// Enforce maximum branch limit to prevent exponential explosion
		if uint64(len(activeConfigs)) > npda.maxBranches {
			return nil, &AutomatonError{
				Kind:      AutomatonErrorBranchLimitNPDA,
				Automaton: "NPDA",
				Message: fmt.Sprintf(
					"NPDA branch limit exceeded: %d > %d at input index %d",
					len(activeConfigs),
					npda.maxBranches,
					idx,
				),
			}
		}
	}

	activeConfigs = computeEpsilonClosure(npda, activeConfigs)
	return collectValidOutcomes(npda, activeConfigs), nil
}

// ------------------------------------------------------------- STEP LOGIC

func stepAllConfigs[TObservation, TStateOutcome any](
	npda *NPDA[TObservation, TStateOutcome],
	configs []NPDAConfig,
	observation TObservation,
) ([]NPDAConfig, error) {

	symbolIDs := npda.nondeterministicResolver(observation)
	if len(symbolIDs) == 0 {
		return nil, &AutomatonError{
			Kind:      AutomatonErrorInvalidSymbolNPDA,
			Automaton: "NPDA",
			Message:   fmt.Sprintf("invalid symbol: %v", observation),
		}
	}

	var nextConfigs []NPDAConfig

	for _, config := range configs {
		if isStackEmpty(config) {
			continue
		}

		topSymbol := getStackTopSymbol(config)

		for _, symbolID := range symbolIDs {
			nextConfigs = processSymbolTransitions(npda, config, symbolID, topSymbol, nextConfigs)
		}
	}

	// Reset epsilon streaks because a symbol was successfully consumed
	for i := range nextConfigs {
		nextConfigs[i].EpsilonStreak = 0
	}

	return nextConfigs, nil
}

func computeEpsilonClosure[TObservation, TStateOutcome any](
	npda *NPDA[TObservation, TStateOutcome],
	configs []NPDAConfig,
) []NPDAConfig {

	queue := make([]NPDAConfig, len(configs))
	copy(queue, configs)

	var closure []NPDAConfig
	visited := make(map[uint64]struct{})

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// 1. Epsilon Streak Defense (prevents zero-mutation loops)
		if current.EpsilonStreak > npda.maxEpsilonSteps {
			continue
		}

		// 2. Cyclic Hash Defense (prevents redundant branch exploration)
		hash := hashConfigCycleState(current)
		if _, exists := visited[hash]; exists {
			continue
		}
		visited[hash] = struct{}{}
		closure = append(closure, current)

		if isStackEmpty(current) {
			continue
		}

		// 3. Stack Depth Defense (prevents infinite push loops)
		if current.StackDepth >= npda.maxStackDepth {
			continue
		}

		topSymbol := getStackTopSymbol(current)
		queue = processSymbolTransitions(npda, current, EpsilonSymbolID, topSymbol, queue)
	}

	return closure
}

func processSymbolTransitions[TObservation, TStateOutcome any](
	npda *NPDA[TObservation, TStateOutcome],
	config NPDAConfig,
	inputID uint64,
	topSymbol StackSymbolID,
	collection []NPDAConfig,
) []NPDAConfig {
	key := TransitionKey{
		StateID:  config.StateID,
		InputID:  inputID,
		StackTop: topSymbol,
	}

	results, exists := npda.transitionTable[key]
	if !exists {
		return collection
	}

	for _, result := range results {
		nextConfig := executeTransitionResult(npda, config, result, inputID == EpsilonSymbolID)
		collection = append(collection, nextConfig)
	}

	return collection
}

func executeTransitionResult[TObservation, TStateOutcome any](
	npda *NPDA[TObservation, TStateOutcome],
	config NPDAConfig,
	result TransitionResult,
	isEpsilon bool,
) NPDAConfig {

	var nextStack memcore.MarkRaw
	var nextDepth uint64

	if !isStackEmpty(config) {
		topNode := memcore.MemcoreMarkDereferenceObject[StackNode](config.StackTop)
		nextStack = topNode.Next
		nextDepth = config.StackDepth - 1
	}

	if len(result.PushSymbolIDs) > 0 {
		for _, sym := range result.PushSymbolIDs {
			nextStack = allocateStackNode(npda.stackAllocator, sym, nextStack)
			nextDepth++
		}
	}
	// Else: pop only (baseline already applied above)

	streak := uint64(0)
	if isEpsilon {
		streak = config.EpsilonStreak + 1
	}

	return NPDAConfig{
		StateID:       result.NextStateID,
		StackTop:      nextStack,
		StackDepth:    nextDepth,
		EpsilonStreak: streak,
	}
}

// ------------------------------------------------------------- PRIVATE HELPERS

func initializeConfigs[TObservation, TStateOutcome any](
	npda *NPDA[TObservation, TStateOutcome],
	initialStackSymbol StackSymbolID,
) []NPDAConfig {

	configs := make([]NPDAConfig, len(npda.startingStates))
	initialStackMark := allocateStackNode(npda.stackAllocator, initialStackSymbol, memcore.MarkRaw{})

	for i, state := range npda.startingStates {
		configs[i] = NPDAConfig{
			StateID:       state,
			StackTop:      initialStackMark,
			StackDepth:    1,
			EpsilonStreak: 0,
		}
	}
	return configs
}

func allocateStackNode(allocator memcore.MarkRaw, symbolID StackSymbolID, next memcore.MarkRaw) memcore.MarkRaw {
	newNodeMark := memforge.SlabAllocatorMalloc[StackNode](allocator)
	newNode := memcore.MemcoreMarkDereferenceObject[StackNode](newNodeMark)
	newNode.SymbolID = symbolID
	newNode.Next = next
	return newNodeMark
}

func isStackEmpty(config NPDAConfig) bool {
	return !memcore.MemcoreMarkIsValid(config.StackTop)
}

func getStackTopSymbol(config NPDAConfig) StackSymbolID {
	node := memcore.MemcoreMarkDereferenceObject[StackNode](config.StackTop)
	return node.SymbolID
}

func collectValidOutcomes[TObservation, TStateOutcome any](
	npda *NPDA[TObservation, TStateOutcome],
	configs []NPDAConfig,
) []TStateOutcome {

	seenStates := make(map[uint64]bool)
	var outcomes []TStateOutcome

	for _, config := range configs {
		if !seenStates[config.StateID] {
			seenStates[config.StateID] = true
			outcomes = append(outcomes, memstruct.ArrayItemGetAtUnsafe[TStateOutcome](npda.outcomes, config.StateID))
		}
	}

	return outcomes
}

func hashConfigCycleState(config NPDAConfig) uint64 {
	var topSymbol uint64
	if !isStackEmpty(config) {
		topSymbol = uint64(getStackTopSymbol(config))
	}

	byteContent := bytes.IntegerSliceToBytes([]uint64{
		config.StateID,
		config.StackDepth,
		topSymbol,
	})
	return hash.XXH3HasherHash64(xxh3Hasher, byteContent)
}
