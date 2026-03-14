# autarch

Finite automaton library providing Non-Deterministic Finite Automata (NFA), Deterministic Finite Automata (DFA), and Nondeterministic Pushdown Automata (NPDA) for pattern matching, lexical analysis, and context-free parsing.

## Overview

autarch provides efficient, memory-managed implementations of finite automata with support for:
- **NFA construction and execution** with epsilon transitions
- **NFA-to-DFA conversion** via subset construction
- **DFA minimization** using Hopcroft's algorithm
- **NFA merging** for combining multiple automata
- **NPDA** for context-free languages (stack-based, epsilon moves, configurable branch/stack limits)
- **DPDA** for LL(1) context-free parsing (single path, O(n) time, slice-based stack)
- **Generic type support** for flexible observation and outcome types

The library is designed for use in lexers, parsers, and pattern matching systems where deterministic state machines are required for efficient recognition of regular languages, and where NPDAs are used for context-free grammar recognition (e.g., syntax highlighters, expression parsers).

## Design Philosophy

- **Explicit Memory Management**: All automata use manual memory allocation via allocation functions, enabling zero-allocation execution paths
- **Generic Type Safety**: Support for any observation type (runes, bytes, tokens) and comparable state outcomes
- **Deterministic Execution**: DFAs provide O(n) input processing with guaranteed single transition per state-symbol pair
- **Cache Efficiency**: Transition tables use flat arrays for optimal memory layout and cache performance
- **Separation of Concerns**: Data structures (automata) are separate from operations (functions), following C-style function-on-data patterns

## Performance Characteristics

- **Zero Allocations in Hot Paths**: DFA execution uses pre-allocated cursors and transition tables; NPDA execution uses a slab-allocated stack (reset at start of each run)
- **Cache Efficiency**: Transition tables stored as contiguous arrays (DFA) or map (NFA, NPDA)
- **Time Complexity**:
  - NFA execution: O(n * s * t) where n is input length, s is states, t is transitions
  - DFA execution: O(n) where n is input length
  - NPDA execution: O(n * b * (s + e)) where n is input length, b is active configs, s/e are transitions and epsilon closure size
  - DPDA execution: O(n) where n is input length
  - NFA-to-DFA: O(2^n * a) worst case (rare in practice)
  - DFA minimization: O(n log n) where n is states
- **Space Complexity**:
  - NFA: O(s + t) where s is states, t is transitions
  - DFA: O(s * a) where s is states, a is alphabet size
  - NPDA: O(states + transitions + maxStackNodes); stack depth and branch count bounded by constructor limits
  - DPDA: O(states + transitions); stack is a Go slice, depth bounded by derivation depth

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

### `NPDA[TObservation, TStateOutcome]`

Nondeterministic Pushdown Automaton with a stack for context-free language recognition. Execution maintains a set of (state, stack) configurations; each input observation is mapped to symbols via an indexer, and transitions are keyed by (state, input symbol, stack top). Stack operations are pop, push, or replace. Epsilon transitions (symbol ID `EpsilonSymbolID`) consume no input.

**Key Types:**
- **`StackSymbolID`** – Identifier for symbols on the stack (distinct from input alphabet).
- **`StackOperation`** – `STACK_POP`, `STACK_PUSH`, `STACK_REPLACE`.
- **`TransitionKey`** – (StateID, InputID, StackTop) keys into the transition table.
- **`TransitionResult`** – NextStateID plus **PushSymbolIDs** (non-empty = pop once then push all; empty/nil = pop only via STACK_POP).
- **`PDATransition`** – Single edge: Key + Result (shared by NPDA and DPDA).
- **`NPDAConfig`** – One active (state, stack) configuration; use `NPDAConfigStackRead` and `NPDAConfigFormat` for debugging.

**Key Functions:**
- **`NPDACreate`** – Build NPDA from alphabet, starting states, transitions, outcomes, indexer, and limits (max stack nodes, max stack depth, max branches, max epsilon steps).
- **`NPDADestroy`** – Release stack slab allocator; call when NPDA is no longer needed.
- **`NPDARun`** – Run on input with initial stack symbol; returns all distinct state outcomes from final configurations (or error if branch limit exceeded / invalid symbol).
- **`NPDAIterateTransitions`** – Walk transition graph for serialization (e.g., Sublime syntax), DOT generation, or static analysis.
- **`NPDAIndexerGet`**, **`NPDAAlphabetGet`**, **`NPDAStartingStatesGet`**, **`NPDANumStates`**, **`NPDAOutcomesGet`** – Accessors for alphabet, start states, state count, and outcome array.

**Performance:**
- Stack uses a slab allocator; **zero allocations** in the hot path per run (allocator reset at start of `NPDARun`).
- Time per run: O(n × b × (s + e)) with n = input length, b = active configs, s = transitions per config, e = epsilon closure size.
- Defensive limits (`maxStackDepth`, `maxBranches`, `maxEpsilonSteps`) prevent unbounded memory and infinite epsilon/stack loops.

**Example:**

```go
import (
    "autarch"
    "memarch"
    "memcore"
    "memstruct"
)

// Alphabet and indexer (same pattern as NFA/DFA)
alphabet := []autarch.SymbolDefinition[rune]{
    {ID: 0, Name: "a", Match: func(r rune) bool { return r == 'a' }},
    {ID: 1, Name: "b", Match: func(r rune) bool { return r == 'b' }},
}
indexer := autarch.SymbolIndexerBuild(alphabet)

// Stack symbol IDs (e.g. 0 = bottom, 1 = "A", 2 = "B")
const (
    stackBottom autarch.StackSymbolID = 0
    stackA      autarch.StackSymbolID = 1
    stackB      autarch.StackSymbolID = 2
)

// Transitions: (state, input, stack_top) -> (next_state, PushSymbolIDs or pop)
// Push: set PushSymbolIDs (one or more symbols); pop: leave PushSymbolIDs empty and use STACK_POP
transitions := []autarch.PDATransition{
    {
        Key:    autarch.TransitionKey{StateID: 0, InputID: 0, StackTop: stackBottom},
        Result: autarch.TransitionResult{NextStateID: 0, PushSymbolIDs: []autarch.StackSymbolID{stackA}},
    },
    {
        Key:    autarch.TransitionKey{StateID: 0, InputID: 0, StackTop: stackA},
        Result: autarch.TransitionResult{NextStateID: 0, PushSymbolIDs: []autarch.StackSymbolID{stackB}},
    },
    {
        Key:    autarch.TransitionKey{StateID: 0, InputID: 1, StackTop: stackB},
        Result: autarch.TransitionResult{NextStateID: 0, StackOp: autarch.STACK_POP},
    },
    {
        Key:    autarch.TransitionKey{StateID: 0, InputID: 1, StackTop: stackA},
        Result: autarch.TransitionResult{NextStateID: 1, StackOp: autarch.STACK_POP},
    },
}

outcomes := []string{"running", "accept"} // state 0 = running, state 1 = accept
npda := autarch.NPDACreate(
    allocFn,
    alphabet,
    []uint64{0},
    transitions,
    outcomes,
    indexer,
    4096,  // max stack nodes
    256,   // max stack depth
    1024,  // max branches
    256,   // max epsilon steps
)
defer autarch.NPDADestroy(npda)

input := []rune{'a', 'a', 'b', 'b'}
result, err := autarch.NPDARun(npda, input, stackBottom)
// result may contain "accept" if the run reaches state 1 with empty stack; client interprets outcomes
```

### `DPDA[TObservation, TStateOutcome]`

Deterministic Pushdown Automaton with a single execution path and a Go-slice stack (no slab allocator). Exactly one transition per (state, input symbol, stack top); the indexer must return exactly one symbol per observation. Used for LL(1) parsing; epsilon transitions perform predictive expansion, and input is consumed only when a terminal is matched.

**Key Functions:**
- **`DPDACreate`** – Build DPDA from alphabet, start state, transitions, outcomes, indexer, terminal stack symbol IDs, and max epsilon steps. Returns `*AutomatonError` with kind `AutomatonErrorNondeterminismDPDA` when transitions are nondeterministic (duplicate keys or epsilon/consuming conflict).
- **`DPDARun`** – Run on input with initial stack symbol; returns the single outcome of the final state or an error. Runtime failures are reported as `*AutomatonError` with kinds such as `AutomatonErrorIndexerAmbiguityDPDA` or `AutomatonErrorSyntaxDPDA`.

**Performance:** O(n) time and O(d) stack space where n is input length and d is derivation depth. Zero manual allocations in the hot path.

**Typical use:** Build via `pattern.CompileDPDA` from a Contexta grammar; see **Contexta (Context-Free Grammar)** below.

### Structured Automaton Errors

All automata in this package may return a structured `*AutomatonError` from constructor and execution paths. The error implements Go's `error` interface while exposing a machine-readable `Kind` and optional payloads so callers do not need to parse human-readable strings.

- **`AutomatonErrorKind`** enumerates high-level categories (for example: DPDA nondeterminism, DPDA syntax error, NPDA branch limit exceeded, invalid input symbol for DFA/NFA).
- **`AutomatonError`** carries:
  - `Kind` – error classification
  - `Automaton` – component name (`"DFA"`, `"NFA"`, `"DPDA"`, `"NPDA"`)
  - `Message` – formatted summary for logs
  - Optional DPDA payloads (`DPDANondeterminismConflict`, `DPDARuntimeContext`)
  - Optional symbol context (`InputID`)

Callers can type-assert `err.(*AutomatonError)` to branch on `Kind` and feed structured diagnostics into higher layers (for example: Contexta, Syntaxa, editor integrations).

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

**NPDA stack types:** `StackSymbolID`, `StackOperation` (STACK_POP for pop-only; push uses `PushSymbolIDs`), `TransitionKey`, `TransitionResult`, `PDATransition` (shared by NPDA and DPDA), `NPDAConfig`. Use `EpsilonSymbolID` for epsilon transitions in PDAs.

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

## Contexta (Context-Free Grammar)

The `autarch/pattern` package also provides **Contexta**, a DSL for defining context-free grammars that compile to PDAs. Use this for parsing (e.g., expressions, config files, nested structures) instead of regular patterns.

### Key Concepts

- **Grammar**: Start symbol + map of rules (non-terminal → productions).
- **Rule**: One non-terminal with one or more productions (alternatives). Optional **AnnotationID** (set via **`RuleAnnotation(ruleName, id)`** after `Define`) is carried into the accept-state outcome when compiling to PDA.
- **Production**: Ordered sequence of symbols (terminals, non-terminals, or epsilon).
- **Builder**: Fluent API to define rules with `BuilderCreate`, `NonTerm`, `Term`, `Epsilon`, `Seq`, `Define`, `RuleAnnotation`, and `Build`.
- **GrammarAnalysis**: FIRST and FOLLOW sets (from `ComputeAnalysis`) used for LL(1) predictive parsing.
- **Annotated outcomes**: `CompileNPDA` and `CompileDPDA` return automata with state outcome type **`AnnotatedOutcome[TOutcome]`** (same as Regula/NFA/DFA). `NPDARun` returns `[]AnnotatedOutcome[TOutcome]`; `DPDARun` returns a single `AnnotatedOutcome[TOutcome]`. The accept state’s annotation comes from the start rule’s `AnnotationID` (if set).

### Compilation Modes

- **CompileNPDA**: Compiles any valid CFG to an NPDA (epsilon branching; grammar need not be LL(1)). Use when the grammar may be ambiguous or you want to explore all parses.
- **CompileDPDA**: Compiles an LL(1) grammar to a DPDA. Uses FIRST/FOLLOW to drive deterministic expansion. Returns an error if the grammar has predictive set overlaps (not LL(1)).

### Example: Contexta Grammar to DPDA

```go
import (
    "autarch"
    "autarch/pattern"
    "memarch"
)

// Token IDs from your lexer (e.g., Regula token types)
const (
    TokenLPAR uint64 = 1
    TokenRPAR uint64 = 2
    TokenNum  uint64 = 3
)

// Build grammar: S -> '(' S ')' S | epsilon (balanced parentheses with optional trailing S)
b := pattern.BuilderCreate[uint64]("S")
b.Define("S",
    pattern.Seq(b.NonTerm("S"), b.Term(TokenLPAR), b.NonTerm("S"), b.Term(TokenRPAR), b.NonTerm("S")),
    pattern.Seq(b.Epsilon()),
)
grammar, err := b.Build()
if err != nil { /* handle */ }

// Alphabet and indexer: map runes (or tokens) to symbol IDs matching grammar terminals
alphabet := []autarch.SymbolDefinition[uint64]{ /* ... */ }
indexer := autarch.SymbolIndexerBuild(alphabet)

// Compile to LL(1) DPDA (returns error if grammar is not LL(1))
// Outcome type is pattern.AnnotatedOutcome[T]; accept state gets start rule's annotation if set via RuleAnnotation.
dpda, debugMap, err := pattern.CompileDPDA(
    grammar,
    allocFn,
    alphabet,
    indexer,
    "accept",  // outcome value for accept state
    256,       // maxEpsilonSteps
)
if err != nil { /* e.g. "grammar is not LL(1) compliant" */ }

// Run on token stream (e.g., after lexing)
outcome, err := autarch.DPDARun(dpda, tokenStream, pattern.BottomMarkerID)
// outcome is pattern.AnnotatedOutcome with Value and optional Annotation; debugMap maps StackSymbolID to names
```

### Contexta Types and Functions

- **`Grammar[TTokenID]`**, **`Rule`** (with optional **AnnotationID**), **`Production`**, **`Symbol`**, **`SymbolType`** – grammar structures.
- **`BuilderCreate`**, **`Builder`** – **`NonTerm`**, **`Term`**, **`Epsilon`**, **`Seq`**, **`Define`**, **`RuleAnnotation`**, **`Build`** – define and validate a grammar.
- **`ContextaDebugFormatter`**, **`ContextaDebugger`**, **`NewContextaDebugger`**, **`NewContextaCleanFormatter`** – human-readable grammar dumps (similar to Regula’s debugger). Use **`Grammar.DebugDump(formatter)`** for a one-liner.
- **`GrammarAnalysis`**, **`TokenSet`**, **`ComputeAnalysis`** – FIRST/FOLLOW for LL(1).
- **`CompilerCreate`**, **`LLCompiler`**, **`Compile`** – low-level compiler (MODE_NPDA / MODE_DPDA); **`CompileNPDA`**, **`CompileDPDA`** – build autarch NPDA/DPDA and optional debug map.
- **`BottomMarkerID`**, **`StateInit`**, **`StateLoop`**, **`StateAccept`** – constants used by the compiler and automata.

## Use Cases

- **Lexical Analysis**: Building tokenizers that recognize keywords, identifiers, numbers, etc.
- **Pattern Matching**: Implementing regular expression engines and string matching
- **Language Recognition**: Validating input against formal language specifications (regular and context-free)
- **Context-Free Parsing**: NPDAs for expression parsers, nested structure recognition, syntax highlighters (e.g., Sublime-style syntax export via NPDAIterateTransitions)
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

7. **Epsilon Transitions**: Epsilon transitions are only valid in NFAs and NPDAs. They are eliminated during NFA-to-DFA conversion.

8. **NPDA Limits**: Set `maxStackDepth`, `maxBranches`, and `maxEpsilonSteps` in `NPDACreate` to prevent unbounded memory or infinite loops. If `NPDARun` returns an error for branch limit exceeded, increase `maxBranches` or simplify the grammar. Call `NPDADestroy` when the NPDA is no longer needed.

9. **DPDA**: The indexer must return exactly one symbol per observation for `DPDARun`. Use `pattern.CompileDPDA` only with LL(1) grammars; otherwise `DPDACreate` returns a nondeterminism error.

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

### NPDA Execution

The NPDA uses a slab allocator for stack nodes; it is reset at the start of each `NPDARun`, so no stack memory persists between runs. A transition may specify **PushSymbolIDs** (one or more symbols); execution pops once then pushes each in order (top = last element). Epsilon closure is computed with cycle detection (hash of state, stack depth, stack top) and enforced limits on consecutive epsilon steps and stack depth. Branch count is capped by `maxBranches` to avoid exponential blowup on highly ambiguous grammars.

### DPDA Execution

The DPDA uses a single Go slice for the stack (no slab). Epsilon moves are applied until no more are possible (or `maxEpsilonSteps` is hit); input is consumed only when a transition matches a terminal (stack top in `terminalIDs`). This yields O(n) time and deterministic behavior for LL(1) grammars produced by `pattern.CompileDPDA`.

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
