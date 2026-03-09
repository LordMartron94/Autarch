// Package autarch provides finite automaton implementations for pattern matching,
// lexical analysis, and state machine construction.
//
// The package implements Non-Deterministic Finite Automata (NFA), Deterministic
// Finite Automata (DFA), and Nondeterministic Pushdown Automata (NPDA), with
// support for epsilon transitions (NFA, NPDA), state minimization (DFA), NFA-to-DFA
// conversion, and NFA merging.
//
// Key features:
//   - Generic type support for observations (input symbols) and state outcomes
//   - Memory-efficient implementations using manual memory management
//   - NFA to DFA conversion via subset construction
//   - DFA minimization using Hopcroft's algorithm
//   - NFA merging operations for combining multiple automata
//   - NPDA for context-free parsing (stack-based, epsilon moves, branch limits)
//
// The package is designed for use in lexers, parsers, and pattern matching
// systems where deterministic state machines are required for efficient
// recognition of regular languages, and where NPDAs are used for
// context-free grammar recognition (e.g., syntax highlighters, expression parsers).
package autarch
