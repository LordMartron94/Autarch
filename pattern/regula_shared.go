package pattern

import (
	"autarch"
	"memarch"
)

/* RegulaToNFACompiler is an abstraction that provides the API boundary for compiling a RegulaAST into NFAs. */
type RegulaToNFACompiler[TObs any, TOutcome comparable] func(
	alloc memarch.AllocationFn,
	instructions []PatternCompilationInstruction[TObs, TOutcome, RegulaAST[TObs]],
	ctx *SharedCompilationContext[TObs, RegulaAST[TObs]],
	nonTerminalOutcome TOutcome,
) ([]*autarch.NFA[TObs, AnnotatedOutcome[TOutcome]], error)
