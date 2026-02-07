// Package autarch provides finite automaton implementations for pattern matching,
// lexical analysis, and state machine construction.
//
// The package implements both Non-Deterministic Finite Automata (NFA) and
// Deterministic Finite Automata (DFA) with support for epsilon transitions,
// state minimization, and conversion between automaton types.
//
// Key features:
//   - Generic type support for observations (input symbols) and state outcomes
//   - Memory-efficient implementations using manual memory management
//   - NFA to DFA conversion via subset construction
//   - DFA minimization using Hopcroft's algorithm
//   - NFA merging operations for combining multiple automata
//
// The package is designed for use in lexers, parsers, and pattern matching
// systems where deterministic state machines are required for efficient
// recognition of regular languages.
package autarch
