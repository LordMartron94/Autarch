# autarch

Finite automaton library providing Non-Deterministic Finite Automata (NFA) and Deterministic Finite Automata (DFA) implementations for pattern matching, lexical analysis, and state machine construction.

## Overview

autarch provides efficient, memory-managed implementations of finite automata with support for:
- **NFA construction and execution** with epsilon transitions
- **NFA-to-DFA conversion** via subset construction
- **DFA minimization** using Hopcroft's algorithm
- **NFA merging** for combining multiple automata
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

The library is used by:
- **lexarch**: Lexical analysis and tokenization
- **matcharch**: Pattern matching systems

## Core Types

### `NFA[TObservation, TStateOutcome]`

Non-Deterministic Finite Automaton supporting multiple transitions per state-symbol pair and epsilon transitions.

**Key Functions:**
- `NFACreate` - Constructs an NFA from transitions and states
- `NFARun` - Processes input and returns all possible outcomes
- `NFAEpsilonClosureCompute` - Computes epsilon closure of state sets
- `NFATransitionsForStates` - Gets next states for a set of current states
- `NFAMergeOr` - Combines two NFAs via union operation

**Example:**

```go
import (
    "autarch"
    "memarch"
    "memcore"
    "memstruct"
)

// Create alphabet and indexer
alphabet := []rune{'a', 'b', 'c'}
indexer := func(r rune) (autarch.Symbol[rune], bool) {
    for i, sym := range alphabet {
        if sym == r {
            return autarch.SymbolCreate[rune](string(r), uint64(i)), true
        }
    }
    return autarch.Symbol[rune]{}, false
}

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

// State outcomes: state 2 is accepting
states := []bool{false, false, true}

// Create NFA
nfa := autarch.NFACreate(
    allocFn,
    alphabet,
    transitions,
    []uint64{0}, // starting states
    states,
    indexer,
)

// Process input
input := []rune{'a', 'b'}
outcomes, err := autarch.NFARun(nfa, input)
// outcomes contains [true] if input matches
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
// Convert NFA to DFA
dfa := autarch.NFAToDFA(
    nfa,
    1*memcore.Byte,      // min temp allocator size
    1*memcore.GigaByte,   // max temp allocator size
    allocFn,
    false,               // invalid outcome
)

// Minimize DFA
minimized := autarch.DFAMinimize(
    dfa,
    allocFn,
    1*memcore.Byte,
    1*memcore.GigaByte,
    false,
)

// Process input efficiently
outcome, err := autarch.DFARun(minimized, []rune{'a', 'b'})
// outcome is true if input matches
```

### Supporting Types

**`Symbol[TObservation]`**: Represents a symbol in the alphabet with ID and description.

**`Transition[TObservation]`**: Defines a state transition with current state, symbol, and next state.

**`SymbolIndexer[TObservation]`**: Function type mapping observations to symbols.

## Use Cases

- **Lexical Analysis**: Building tokenizers that recognize keywords, identifiers, numbers, etc.
- **Pattern Matching**: Implementing regular expression engines and string matching
- **Language Recognition**: Validating input against formal language specifications
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
// Define token types
type TokenType int
const (
    TokenInvalid TokenType = iota
    TokenNumber
    TokenIdentifier
    TokenKeyword
)

// Create NFA for numbers (simplified: [0-9]+)
// ... construct NFA with transitions for digits ...

// Create NFA for identifiers ([a-zA-Z][a-zA-Z0-9]*)
// ... construct NFA with transitions for letters and digits ...

// Merge NFAs
merged := autarch.NFAMergeOr(
    numberNFA,
    identifierNFA,
    allocFn,
    TokenInvalid,
    func(r rune) rune { return r }, // key function
)

// Convert to DFA and minimize
dfa := autarch.NFAToDFA(merged, minTemp, maxTemp, allocFn, TokenInvalid)
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
    
    // Check outcome
    outcome := autarch.DFAStateOutcome(dfa, currentState)
    if outcome != invalidOutcome {
        // Accepting state reached
    }
}
```
