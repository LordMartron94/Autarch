# autarch

Finite automaton library providing Non-Deterministic Finite Automata (NFA) and Deterministic Finite Automata (DFA) implementations for pattern matching, lexical analysis, and state machine construction.

## Overview

autarch provides efficient, memory-managed implementations of finite automata with support for:
- **NFA construction and execution** with epsilon transitions
- **NFA-to-DFA conversion** via subset construction
- **DFA minimization** using Hopcroft's algorithm
- **NFA merging** for combining multiple automata
- **DPDA (Deterministic Pushdown Automaton)** for context-free parsing and nested structure recognition
- **Generic type support** for flexible observation and outcome types

The library is designed for use in lexers, parsers, and pattern matching systems where deterministic state machines are required for efficient recognition of regular languages.

## Design Philosophy

- **Explicit Memory Management**: All automata use manual memory allocation via allocation functions, enabling zero-allocation execution paths
- **Generic Type Safety**: Support for any observation type (runes, bytes, tokens) and comparable state outcomes
- **Deterministic Execution**: DFAs provide O(n) input processing with guaranteed single transition per state-symbol pair
- **Cache Efficiency**: Transition tables use flat arrays for optimal memory layout and cache performance
- **Separation of Concerns**: Data structures (automata) are separate from operations (functions), following C-style function-on-data patterns

## Performance Characteristics

- **Zero Allocations in Hot Paths**: DFA execution uses pre-allocated cursors and transition tables
- **Cache Efficiency**: Transition tables stored as contiguous arrays (state * alphabetSize + symbolID)
- **Time Complexity**:
  - NFA execution: O(n * s * t) where n is input length, s is states, t is transitions
  - DFA execution: O(n) where n is input length
  - NFA-to-DFA: O(2^n * a) worst case (rare in practice)
  - DFA minimization: O(n log n) where n is states
- **Space Complexity**:
  - NFA: O(s + t) where s is states, t is transitions
  - DFA: O(s * a) where s is states, a is alphabet size

## Integration

autarch integrates with the memory management stack:

```
autarch
  ├── memarch (memory allocation)
  ├── memcore (memory primitives)
  ├── memstruct (array/queue structures)
  └── memforge (allocator implementations)
```

## Core Types

### `NFA[TObservation, TStateOutcome]`

Non-Deterministic Finite Automaton supporting multiple transitions per state-symbol pair and epsilon transitions.

**Key Functions:**
- `NFACreate` - Constructs an NFA from transitions and states
- `NFARun` - Processes input and returns all possible outcomes
- `NFAEpsilonClosureCompute` - Computes epsilon closure of state sets
- `NFATransitionsForStates` - Gets next states for a set of current states
- `NFAMergeOr` - Combines two NFAs via union operation, deduplicating symbols by name

**Example:**

```go
import (
    "autarch"
    "memarch"
    "memcore"
    "memstruct"
)

// Create alphabet and indexer
alphabet := []autarch.SymbolDefinition[rune]{
    {ID: 0, Name: "a", Match: func(r rune) bool { return r == 'a' }},
    {ID: 1, Name: "b", Match: func(r rune) bool { return r == 'b' }},
    {ID: 2, Name: "c", Match: func(r rune) bool { return r == 'c' }},
}
indexer := autarch.SymbolIndexerBuild(alphabet)

// Define transitions: state 0 -> state 1 on 'a', state 1 -> state 2 on 'b'
transitions := []autarch.Transition[rune]{
    {
        CurrentState: 0,
        Symbol:       autarch.SymbolCreate[rune]("a", 0),
        NextState:    1,
    },
    {
        CurrentState: 1,
        Symbol:       autarch.SymbolCreate[rune]("b", 1),
        NextState:    2,
    },
}

// State outcomes: one per state (e.g. true = match at state 2)
outcomes := []bool{false, false, true}

// Create NFA
nfa := autarch.NFACreate(
    allocFn,
    alphabet,
    transitions,
    nil, // epsilon edges (none in this example)
    []uint64{0}, // starting states
    outcomes,
    indexer,
)

// Process input
input := []rune{'a', 'b'}
outcomes, err := autarch.NFARun(nfa, input)
// outcomes contains the outcome(s) from the final state set; client interprets which denote acceptance
```

### `DFA[TObservation, TStateOutcome]`

Deterministic Finite Automaton with exactly one transition per state-symbol pair for efficient execution.

**Key Functions:**
- `DFACreate` - Constructs a DFA from transitions and states
- `DFARun` - Processes input and returns final state outcome
- `DFAStep` - Performs single transition step with error checking
- `DFATransition` - Optimized single transition using cursor
- `DFAMinimize` - Reduces DFA to minimal equivalent form
- `DFADebugPrint` - Generates human-readable automaton description

**Example:**

```go
// Convert NFA to DFA (resolution function maps subset outcomes to DFA state outcome)
dfa := autarch.NFAToDFA(
    nfa,
    1*memcore.Byte,      // min temp allocator size
    1*memcore.GigaByte,   // max temp allocator size
    allocFn,
    nil,                 // resolution function (nil = default: OutcomeResolutionFirst)
)

// Minimize DFA (outcomeKeyFn partitions states by outcome for equivalence)
minimized := autarch.DFAMinimize(
    dfa,
    allocFn,
    1*memcore.Byte,
    1*memcore.GigaByte,
    func(outcome bool) bool { return outcome },
)

// Process input efficiently
outcome, err := autarch.DFARun(minimized, []rune{'a', 'b'})
// outcome is the state outcome; client interprets it as accept/reject (e.g. outcome == true)
```

### `DPDA[TObservation, TStackSymbol, TStateOutcome]`

Deterministic Pushdown Automaton with a stack for context-free parsing and nested structure recognition. Transitions are keyed by (state, input symbol, stack top) and specify a stack operation (Push, Pop, Replace, NoOp) and next state. A **bottom-of-stack (BOS) symbol** is required: the stack always contains at least BOS, so Peek and Replace are safe.

**Key Types:**
- `DPDA` - The automaton (input alphabet, stack alphabet, transition table, outcomes, BOS symbol ID)
- `DPDAState` - Runtime state (current state index and symbol stack; BOS stored at create)
- `DPDATransition` - One transition: from (CurrentState, InputSymbol, CurrentStackTop) to NextState with Operation
- `DPDATransitionResult` - Result of DPDATransitionGet (NextState, OpKind, PushOrReplaceSymbolID)
- `StackOperationKind` / `StackOperation` - Push, Pop, Replace, NoOp and optional stack symbol

**Key Functions:**
- `DPDACreate` - Builds a DPDA from alphabets, **bottomStackSymbolID**, transitions, outcomes, and indexer
- `DPDAStateCreate` - Allocates initial state (state 0, stack with BOS only)
- `DPDAStateReset` - Clears stack to BOS and sets current state; optional keepCapacity
- `DPDAStateDestroy` - Releases state (no-op for arena allocators)
- `DPDAStateClone` - Copies state and stack for backtracking/debugging
- `DPDAStep` - One transition via observation; updates state.currentState; panics if no transition
- `DPDATryStep` - Like DPDAStep but returns ok=false instead of panicking
- `DPDAStepSymbol` - One transition by input symbol ID (no indexer); returns true if transition existed
- `DPDATransitionGet` - Zero-allocation transition lookup (q, inputID, stackTopID) → result, ok
- `DPDACurrentStackTop` - Stack-alphabet symbol at top of stack
- `DPDAStackTopID`, `DPDAStackDepth`, `DPDAStackClearToBottom`, `DPDAStackPushID`, `DPDAStackPopID` - Stack accessors
- `DPDANumStates`, `DPDAInputAlphabet`, `DPDAStackAlphabet`, `DPDAOutcome`, `DPDAIsAccepting` - Metadata and acceptance
- `DPDAIsAcceptingStateAndStackDepthOne` - Accepting state and stack depth 1 (for nested DPDAs)
- `DPDAAvailableInputs` - Input symbol IDs with a transition from (q, stackTopID) (diagnostics)
- `DPDADebugPrint`, `DPDAValidate` - Diagnostics
- `DPDARun` - Reset state, step through observations; returns finalState, ok, err
- `DPDARunAndAccept` - Run then return isAccepting(outcome) for final state

**Acceptance** is client-defined via a callback: `DPDAIsAccepting(dpda, stateID, isAccepting)` and `DPDARunAndAccept(..., isAccepting)`. For **nested-structure DPDAs** (e.g. compiled from Vistra), full acceptance should require both an accepting state and stack depth 1 (only BOS remains); use `DPDAIsAcceptingStateAndStackDepthOne(dpda, state, isAccepting)` after a run.

**Example:**

```go
// Input alphabet (e.g. tokens), stack alphabet (e.g. bracket types), and indexer
inputAlphabet := []autarch.SymbolDefinition[Token]{ ... }
stackAlphabet := []autarch.SymbolDefinition[Bracket]{ ... }
indexer := autarch.SymbolIndexerBuild(inputAlphabet)

// Transitions: (currentState, inputSymbol, stackTop) -> (nextState, stack op). BOS is stack symbol 0.
transitions := []autarch.DPDATransition[Token, Bracket]{
    {CurrentState: 0, InputSymbol: openSym, CurrentStackTop: bottomSym, NextState: 1, Operation: autarch.StackOperation[Bracket]{Kind: autarch.Push, StackSymbol: bracketSym}},
    {CurrentState: 1, InputSymbol: closeSym, CurrentStackTop: bracketSym, NextState: 1, Operation: autarch.StackOperation[Bracket]{Kind: autarch.Pop}},
    // ...
}
outcomes := []Outcome{ ... }

// BOS symbol ID (e.g. 0) must be a valid index into stackAlphabet; stack always contains at least BOS
dpda, err := autarch.DPDACreate(allocFn, inputAlphabet, stackAlphabet, 0 /* BOS symbol ID */, transitions, outcomes, indexer)
state := autarch.DPDAStateCreate(dpda, scratchAllocFn, 8)

for _, tok := range tokens {
    nextState, err := autarch.DPDAStep(dpda, state, tok)
    if err != nil { ... }
    // Optional: autarch.DPDACurrentStackTop(dpda, state), or use DPDATryStep / DPDARun / DPDARunAndAccept
}
```

### Outcome Resolution

When converting an NFA to a DFA, multiple NFA states may map to a single DFA state. The outcome
resolution function determines which outcome value to assign to the DFA state.

**Built-in Resolution Functions:**
- `OutcomeResolutionFirst`: Picks the first valid outcome (lowest state ID) - default behavior
- `OutcomeResolutionLast`: Picks the last valid outcome (highest state ID)

**Custom Resolution:**

You can provide a custom `OutcomeResolutionFn` to implement domain-specific outcome selection.
Acceptance is a client-defined semantic: every state has an outcome; the resolver picks one for the DFA state from the subset's outcomes.

```go
customResolution := func(states []uint64, outcomes []TokenType) (TokenType, bool) {
    // Custom logic to select from outcomes (e.g. first non-sentinel)
    if len(outcomes) == 0 {
        return invalidToken, false
    }
    return outcomes[0], true
}

dfa := autarch.NFAToDFA(nfa, minTemp, maxTemp, allocFn, customResolution)
```

### Supporting Types

**`Symbol[TObservation]`**: Represents a symbol in the alphabet with ID and description.

**`SymbolDefinition[TObservation]`**: Represents a symbol definition with ID, name, and match predicate.

**`SymbolKind`**: Enum representing symbol types (literal, range, class, wildcard, epsilon).

**`SymbolKey`**: Struct containing SymbolKind and Hash for unique symbol identity.

**`Transition[TObservation]`**: Defines a state transition with current state, symbol, and next state.

**`SymbolIndexer[TObservation]`**: Function type mapping observations to symbols.

## Pattern Builder (regula)

The `autarch/pattern` package provides a fluent API for building regular expression-like patterns
that can be compiled into NFAs. This eliminates the need to manually construct transitions and
alphabet definitions for common pattern matching scenarios.

### Key Features

- **Fluent API**: Chain operations to build complex patterns (e.g., `Literal('a').Then(Class(Range('0', '9'))).Star()`)
- **Type-Safe**: Generic over observation types (runes, bytes, custom types)
- **Automatic Symbol Management**: Deduplicates symbols and builds alphabet automatically
- **Thompson's Construction**: Uses proven algorithm for NFA construction
- **Predefined Classes**: Common character classes (Digit, Lower, Upper, Word) available

### Building Patterns

Patterns are built using a combination of:
- **Literals**: Exact sequences (`Literal('h', 'e', 'l', 'l', 'o')`)
- **Character Classes**: Ranges of observations (`Class(Range('0', '9'))`)
- **Combinators**: Concatenation (`Then`), alternation (`Or`), repetition (`Star`, `Plus`, `Optional`, `Repeat`)
- **Helpers**: Multi-expression builders (`Sequence`, `AnyOf`)

### Example: Building a Pattern

```go
import (
    "autarch"
    "autarch/pattern"
    "memarch"
)

// Build a pattern: letter followed by zero or more letters/digits/underscores
identifierPattern := pattern.Class(
    pattern.Range('a', 'z'),
    pattern.Range('A', 'Z'),
).Then(
    pattern.Class(
        pattern.Range('a', 'z'),
        pattern.Range('A', 'Z'),
        pattern.Range('0', '9'),
        pattern.Range('_', '_'),
    ).Star(),
)

// Or use predefined classes
identifierPattern := pattern.Lower.Or(pattern.Upper).Then(
    pattern.Word.Star(),
)

// Compile to NFA
nfa := pattern.RegulaCompileToNFA(
    allocFn,
    identifierPattern,
    TokenIdentifier,  // outcome for that state
    TokenInvalid,     // invalid outcome
)

// Convert to DFA for efficient execution
dfa := autarch.NFAToDFA(nfa, minTemp, maxTemp, allocFn, TokenInvalid, nil) // Uses default resolution
```

### Pattern Builder Functions

**Basic Constructors:**
- `Literal[TObservation](values...)` - Matches exact sequence
- `Class[TObservation](ranges...)` - Matches any observation in ranges
- `Range[TObservation](lo, hi)` - Creates a character range

**Combinators (methods on `regulaAST`):**
- `Then(b)` - Concatenation (a then b)
- `Or(b)` - Alternation (a or b)
- `Star()` - Zero or more repetitions
- `Plus()` - One or more repetitions
- `Optional()` - Zero or one repetition
- `Repeat(min, max)` - Bounded repetition

**Multi-Expression Builders:**
- `Sequence(exprs...)` - Concatenates multiple expressions
- `AnyOf(exprs...)` - Alternation of multiple expressions

**Predefined Classes:**
- `pattern.Digit` - Matches '0'-'9'
- `pattern.Lower` - Matches 'a'-'z'
- `pattern.Upper` - Matches 'A'-'Z'
- `pattern.Word` - Matches letters, digits, and underscore

### Compilation

The `RegulaCompileToNFA` function compiles a pattern AST into an NFA:
- Automatically collects and deduplicates symbols
- Builds alphabet with stable symbol IDs
- Constructs NFA using Thompson's algorithm
- Returns NFA ready for execution or DFA conversion

**Time Complexity**: O(n) where n is AST nodes
**Space Complexity**: O(s + t) where s is states, t is transitions

## Use Cases

- **Lexical Analysis**: Building tokenizers that recognize keywords, identifiers, numbers, etc.
- **Pattern Matching**: Implementing regular expression engines and string matching
- **Language Recognition**: Validating input against formal language specifications
- **Context-Free Parsing**: Using DPDA for bracket matching, nested blocks, and grammar-driven recognition
- **Protocol Parsing**: Recognizing structured data formats and protocols
- **Text Processing**: Finding and extracting patterns from text streams

## Safety Guidelines

⚠️ **Important:**

1. **Memory Lifetime**: Automata hold references to allocated memory. Ensure allocators remain valid for the automaton's lifetime.

2. **Input Validation**: Always validate that input observations are in the automaton's alphabet. Invalid symbols return errors in `DFARun` and `DFAStep`.

3. **State Indices**: State indices must be valid (0 <= state < numStates). Invalid indices cause undefined behavior.

4. **Allocator Sizing**: For NFA-to-DFA conversion and minimization, ensure `maxTempAllocatorMemory` is sufficient. The algorithms panic if exceeded.

5. **Symbol ID Consistency**: Symbol IDs must be consistent with the alphabet. Symbol ID i must correspond to alphabet[i] (except special IDs like epsilon).

6. **DFA Determinism**: DFAs must have exactly one transition per state-symbol pair. `DFACreate` panics if duplicates are found.

7. **Epsilon Transitions**: Epsilon transitions are only valid in NFAs. They are eliminated during NFA-to-DFA conversion.

8. **DPDA Determinism**: The indexer must return exactly one symbol per observation; otherwise `DPDAStep` errors or panics. Every (currentState, inputSymbol, stackTop) encountered during stepping must have a transition (or use `DPDATryStep` for non-panicking behavior). The **bottom-of-stack (BOS)** symbol ID must be valid at create; the stack always contains at least BOS. Do not pop the last (BOS) element with `DPDAStackPopID`.

## Implementation Notes

### NFA-to-DFA Conversion

The subset construction algorithm uses a worklist approach with linear search for subset matching. This avoids hash map complexity while maintaining correctness. Worst-case exponential state blowup is possible but rare in practice.

### DFA Minimization

Hopcroft's algorithm partitions states into equivalence classes based on transition behavior. The implementation uses bitsets for efficient set operations, achieving O(n log n) time complexity.

### Memory Layout

- **NFA transitions**: Stored in Go map for flexibility (multiple transitions per key)
- **DFA transitions**: Stored as flat array `[state * alphabetSize + symbolID]` for cache efficiency
- **State outcomes**: Stored as contiguous array indexed by state ID

### Epsilon Closure

Computed using depth-first search with a stack-based approach. The algorithm handles cycles correctly and deduplicates states automatically.

## Accuracy and Limitations

- **State Count Limits**: NFAs support up to 256 states (configurable via `maxNFAStates` constant)
- **Alphabet Size**: No hard limit, but large alphabets increase memory usage quadratically in DFAs
- **Exponential Blowup**: NFA-to-DFA conversion can create up to 2^n states from n NFA states
- **Minimization Guarantees**: Hopcroft's algorithm produces the minimal DFA, but may not be unique (canonical form depends on state ordering)

## Examples

### Building a Simple Lexer

```go
import (
    "autarch"
    "autarch/pattern"
    "memarch"
)

// Define token types
type TokenType int
const (
    TokenInvalid TokenType = iota
    TokenNumber
    TokenIdentifier
    TokenKeyword
)

// Build patterns using the regula pattern builder
numberPattern := pattern.Digit.Plus()  // [0-9]+
identifierPattern := pattern.Class(
    pattern.Range('a', 'z'),
    pattern.Range('A', 'Z'),
).Then(pattern.Word.Star())  // [a-zA-Z][a-zA-Z0-9_]*

// Compile patterns to NFAs
numberNFA := pattern.RegulaCompileToNFA(
    allocFn,
    numberPattern,
    TokenNumber,
    TokenInvalid,
)

identifierNFA := pattern.RegulaCompileToNFA(
    allocFn,
    identifierPattern,
    TokenIdentifier,
    TokenInvalid,
)

// Merge NFAs (alternation of all token patterns)
merged := autarch.NFAMergeOr(
    numberNFA,
    identifierNFA,
    allocFn,
    TokenInvalid,
    func(r rune) rune { return r }, // key function
)

// Convert to DFA and minimize for efficient execution
dfa := autarch.NFAToDFA(merged, minTemp, maxTemp, allocFn, TokenInvalid, nil) // Uses default resolution
minimized := autarch.DFAMinimize(dfa, allocFn, minTemp, maxTemp, TokenInvalid)

// Use for lexing
tokens, err := lexWithDFA(minimized, input)
```

### Efficient DFA Execution

```go
// Get cursor for optimized access
cursor := autarch.DFACursorGet(dfa)
currentState := uint64(0)

// Process input with minimal overhead
for _, obs := range input {
    // Single transition with cursor (no error checking for performance)
    currentState = autarch.DFATransition(dfa, obs, currentState, cursor)
    
    // Check outcome (client interprets whether it denotes a match)
    outcome, _ := autarch.DFAStateOutcome(dfa, currentState)
    if outcome != invalidOutcome {
        // State has a match outcome
    }
}
```
