// Package pattern provides a structural DSL for specifying regular and context-free grammars.
//
// Regular languages: the regula API builds patterns (concatenation, alternation, repetition)
// that compile to NFAs and can be converted to DFAs for matching.
//
// Context-free languages: the Contexta API builds CFGs (rules, productions, terminals and
// non-terminals) that compile to NPDAs (general) or DPDAs (LL(1)) for parsing. Use
// pattern.CompileNPDA or pattern.CompileDPDA with a Contexta grammar and autarch alphabet/indexer.
package pattern
