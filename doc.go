// Package autarch provides finite automaton implementations for pattern matching,
// lexical analysis, and state machine construction.
//
// The package implements Non-Deterministic Finite Automata (NFA), Deterministic
// Finite Automata (DFA), and Deterministic Pushdown Automata (DPDA) with support
// for epsilon transitions (NFA), state minimization (DFA), NFA-to-DFA conversion,
// and stack-based parsing (DPDA). DPDA requires a bottom-of-stack (BOS) symbol;
// acceptance is client-defined via an isAccepting(outcome) callback.
//
// Key features:
//   - Generic type support for observations (input symbols) and state outcomes
//   - Memory-efficient implementations using manual memory management
//   - NFA to DFA conversion via subset construction
//   - DFA minimization using Hopcroft's algorithm
//   - NFA merging operations for combining multiple automata
//   - DPDA for context-free parsing and nested structure recognition (BOS, Run, RunAndAccept)
//
// The package is designed for use in lexers, parsers, and pattern matching
// systems where deterministic state machines are required for efficient
// recognition of regular languages and pushdown automata for context-free cases.
package autarch
