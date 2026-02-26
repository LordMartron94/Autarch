// Package autarch provides finite automaton implementations for pattern matching,
// lexical analysis, and state machine construction.
//
// The package implements Non-Deterministic Finite Automata (NFA), Deterministic
// Finite Automata (DFA), with support for epsilon transitions (NFA),
// state minimization (DFA), NFA-to-DFA conversion, and NFA merging.
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
