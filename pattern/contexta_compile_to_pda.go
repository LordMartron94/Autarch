package pattern

import (
	"autarch"
	"fmt"
	"memarch"
)

/*
ContextaGrammarError wraps lower-level automaton and compiler errors with
grammar-aware metadata.

Use cases:
- Reporting LL(1) violations and predictive set conflicts with rule names
- Exposing stack symbol / rule bindings for tooling

Time complexity: O(1)
Space complexity: O(1)
*/
type ContextaGrammarError struct {
	Engine      string
	Underlying  error
	Automaton   *autarch.AutomatonError
	RuleName    string
	StackSymbol autarch.StackSymbolID
	InputID     uint64
}

// Error implements the error interface.
func (e *ContextaGrammarError) Error() string {
	if e == nil {
		return ""
	}
	if e.Underlying != nil {
		return e.Underlying.Error()
	}
	return "contexta grammar error"
}

func wrapContextaGrammarError(
	engine string,
	src error,
	inputID uint64,
) error {
	if src == nil {
		return nil
	}

	var autoErr *autarch.AutomatonError
	if as, ok := src.(*autarch.AutomatonError); ok {
		autoErr = as
	}

	err := &ContextaGrammarError{
		Engine:     engine,
		Underlying: src,
		Automaton:  autoErr,
		InputID:    inputID,
	}

	return err
}

const (
	StateInit   uint64 = 0
	StateLoop   uint64 = 1
	StateAccept uint64 = 2
)

/*
BottomMarkerID is the stack symbol ID reserved for the bottom-of-stack marker in PDA compilation.

The compiler pushes the start symbol onto this marker and accepts when the stack reduces
back to bottom (epsilon transition from StateLoop with StackTop == BottomMarkerID).

Time complexity: O(1)
Space complexity: O(1)
*/
const BottomMarkerID autarch.StackSymbolID = 0

/*
CompilerMode selects whether the LL compiler produces transitions for an NPDA or a DPDA.

MODE_NPDA: expansion on epsilon only (nondeterministic branching). MODE_DPDA: expansion
triggered by lookahead using FIRST/FOLLOW (predictive set); grammar must be LL(1).

Time complexity: O(1)
Space complexity: O(1)
*/
type CompilerMode uint8

const (
	MODE_NPDA CompilerMode = iota
	MODE_DPDA
)

/*
LLCompiler translates a Context-Free Grammar into a PDA transition table (NPDA or DPDA).

In MODE_DPDA, ComputeAnalysis is used to build predictive sets so each (non-terminal, lookahead)
has at most one production. In MODE_NPDA, expansions are driven by epsilon only. Terminals
in the grammar are of type TObservation; the indexer resolves them to alphabet symbol IDs
at compile time (and at run time when the PDA runs). No client-side TObservation→uint64
mapping is required. Internal state IDs and stack symbol IDs are assigned during Compile.
Use CompileNPDA or CompileDPDA for a full automaton; use CompilerCreate and Compile for
transitions and a debug map only.
*/
type LLCompiler[TObservation comparable, TMeta any] struct {
	grammar                 *Grammar[TObservation, TMeta]
	mode                    CompilerMode
	indexer                 autarch.SymbolIndexer[TObservation]
	deterministicResolver   autarch.DeterministicSymbolResolver[TObservation]
	nondeterministicResolve autarch.NondeterministicSymbolResolver[TObservation]

	// Predictive analysis (populated only in DPDA mode)
	analysis *GrammarAnalysis[TObservation]

	// Registries to assign unique IDs to grammar symbols
	nextStackID       autarch.StackSymbolID
	symbolMap         map[string]autarch.StackSymbolID
	terminalToStackID map[TObservation]autarch.StackSymbolID

	terminalIDs []autarch.StackSymbolID

	transitions []autarch.PDATransition
	nextStateID uint64
}

/*
CompilerCreate allocates an LL compiler for the given grammar, mode, and indexer.

In MODE_DPDA, ComputeAnalysis is run to populate FIRST/FOLLOW for predictive sets.
Terminals in the grammar are TObservation; the indexer maps each to symbol IDs for
transition keys and is used again at run time by the PDA. The client supplies
alphabet + indexer (same as for DFA/NFA); no manual TObservation→uint64 mapping.

Time complexity: O(1) for MODE_NPDA; O(grammar size) for MODE_DPDA (ComputeAnalysis)
Space complexity: O(grammar + analysis)
*/
func CompilerCreate[TObservation comparable, TMeta any](
	grammar *Grammar[TObservation, TMeta],
	mode CompilerMode,
	deterministicResolver autarch.DeterministicSymbolResolver[TObservation],
	nondeterministicResolve autarch.NondeterministicSymbolResolver[TObservation],
) *LLCompiler[TObservation, TMeta] {
	c := &LLCompiler[TObservation, TMeta]{
		grammar:                 grammar,
		mode:                    mode,
		indexer:                 nil,
		deterministicResolver:   deterministicResolver,
		nondeterministicResolve: nondeterministicResolve,
		nextStackID:             BottomMarkerID + 1,
		symbolMap:               make(map[string]autarch.StackSymbolID),
		terminalToStackID:       make(map[TObservation]autarch.StackSymbolID),
		transitions:             make([]autarch.PDATransition, 0),
		nextStateID:             StateAccept + 1,
		terminalIDs:             make([]autarch.StackSymbolID, 0),
	}

	if mode == MODE_DPDA {
		c.analysis = ComputeAnalysis[TObservation, TMeta](grammar)
	}

	return c
}

/*
Compile generates the transitions required to build an autarch.NPDA or autarch.DPDA.

Emits initialization (push start symbol, enter loop), acceptance (epsilon on bottom
marker), and rule expansions. In DPDA mode, each production is triggered by its
predictive set (FIRST(prod) ∪ FOLLOW(nt) if prod is nullable). Also returns a
resolution map from StackSymbolID to human-readable name for debugging.

Time complexity: O(r * p * s) over rules, productions, and symbols
Space complexity: O(transitions + symbolMap)

Prerequisites:
- grammar must be valid; in MODE_DPDA every production must have a non-empty predictive set (panics otherwise)

Edge cases:
- In DPDA mode, acceptance transition uses Epsilon on bottom marker when input is fully consumed
*/
func (c *LLCompiler[TObservation, TMeta]) Compile() ([]autarch.PDATransition, map[autarch.StackSymbolID]string, error) {

	startStackID := c.getNonTerminalID(c.grammar.StartSymbol)

	c.transitions = append(c.transitions, autarch.PDATransition{
		Key: autarch.TransitionKey{
			StateID:  StateInit,
			InputID:  autarch.EpsilonSymbolID,
			StackTop: BottomMarkerID,
		},
		Result: autarch.TransitionResult{
			NextStateID:   StateLoop,
			PushSymbolIDs: []autarch.StackSymbolID{startStackID},
		},
	})

	c.transitions = append(c.transitions, autarch.PDATransition{
		Key: autarch.TransitionKey{
			StateID:  StateLoop,
			InputID:  autarch.EpsilonSymbolID,
			StackTop: BottomMarkerID,
		},
		Result: autarch.TransitionResult{
			NextStateID: StateAccept,
			StackOp:     autarch.STACK_POP,
		},
	})

	if err := c.compileRules(); err != nil {
		return nil, nil, err
	}

	resolutionMap := make(map[autarch.StackSymbolID]string)
	for name, id := range c.symbolMap {
		resolutionMap[id] = name
	}
	for obs, id := range c.terminalToStackID {
		resolutionMap[id] = fmt.Sprint(obs)
	}
	resolutionMap[BottomMarkerID] = "$BOTTOM"

	return c.transitions, resolutionMap, nil
}

func (c *LLCompiler[TObservation, TMeta]) compileRules() error {
	for nonTerminalName, rule := range c.grammar.Rules {
		ntID := c.getNonTerminalID(nonTerminalName)

		for _, prod := range rule.Productions {
			if err := c.compileProduction(nonTerminalName, ntID, prod); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *LLCompiler[TObservation, TMeta]) compileProduction(ntName string, ntID autarch.StackSymbolID, prod Production[TObservation, TMeta]) error {
	symbols := prod.Symbols
	k := len(symbols)

	var pushIDs []autarch.StackSymbolID
	if k == 0 || (k == 1 && symbols[0].Type == SYMBOL_EPSILON) {
		// Pop only; no push
	} else {
		for _, sym := range symbols {
			pushIDs = append(pushIDs, c.resolveSymbolID(sym))
		}
	}

	var triggers []uint64

	if c.mode == MODE_DPDA {
		triggers = c.computePredictiveSet(ntName, prod)
		if len(triggers) == 0 {
			return fmt.Errorf("production for '%s' can never be reached (empty predictive set)", ntName)
		}
	} else {
		triggers = []uint64{autarch.EpsilonSymbolID}
	}

	for _, trigger := range triggers {
		res := autarch.TransitionResult{NextStateID: StateLoop}
		if len(pushIDs) > 0 {
			res.PushSymbolIDs = pushIDs
		} else {
			res.StackOp = autarch.STACK_POP
		}
		c.transitions = append(c.transitions, autarch.PDATransition{
			Key: autarch.TransitionKey{
				StateID:  StateLoop,
				InputID:  trigger,
				StackTop: ntID,
			},
			Result: res,
		})
	}

	return nil
}

// computePredictiveSet returns input symbol IDs (uint64) for transition keys:
// FIRST(prod) ∪ FOLLOW(ntName) if prod is nullable, resolved via the indexer.
func (c *LLCompiler[TObservation, TMeta]) computePredictiveSet(ntName string, prod Production[TObservation, TMeta]) []uint64 {
	firstSet := make(TokenSet[TObservation])
	isNullable := true

	for _, sym := range prod.Symbols {
		if sym.Type == SYMBOL_TERMINAL {
			firstSet[sym.Token] = struct{}{}
			isNullable = false
			break
		}
		if sym.Type == SYMBOL_NON_TERMINAL {
			for t := range c.analysis.First[sym.Name] {
				firstSet[t] = struct{}{}
			}
			if !c.analysis.Nullable[sym.Name] {
				isNullable = false
				break
			}
		}
	}

	if isNullable {
		for t := range c.analysis.Follow[ntName] {
			firstSet[t] = struct{}{}
		}
	}

	// Resolve each terminal in the set to alphabet symbol ID(s) via indexer
	seen := make(map[uint64]struct{})
	for obs := range firstSet {
		if c.mode == MODE_DPDA {
			id, ok := c.deterministicResolver(obs)
			if !ok {
				panic(fmt.Sprintf("grammar terminal %v not in alphabet (resolver returned no symbol)", obs))
			}
			seen[id] = struct{}{}
			continue
		}

		ids := c.nondeterministicResolve(obs)
		if len(ids) == 0 {
			panic(fmt.Sprintf("grammar terminal %v not in alphabet (indexer returned no symbol)", obs))
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	out := make([]uint64, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// resolveSymbolID maps a Contexta symbol to a StackSymbolID and emits match transitions for terminals.
func (c *LLCompiler[TObservation, TMeta]) resolveSymbolID(sym Symbol[TObservation, TMeta]) autarch.StackSymbolID {
	if sym.Type == SYMBOL_NON_TERMINAL {
		return c.getNonTerminalID(sym.Name)
	}
	if sym.Type == SYMBOL_EPSILON {
		panic("resolveSymbolID: epsilon has no stack symbol ID")
	}

	obs := sym.Token
	stackID := c.getTerminalStackID(obs)

	if c.mode == MODE_DPDA {
		inputID, ok := c.deterministicResolver(obs)
		if !ok {
			panic(fmt.Sprintf("grammar terminal %v not in alphabet (resolver returned no symbol)", obs))
		}
		if !c.hasMatchTransition(inputID, stackID) {
			c.transitions = append(c.transitions, autarch.PDATransition{
				Key: autarch.TransitionKey{
					StateID:  StateLoop,
					InputID:  inputID,
					StackTop: stackID,
				},
				Result: autarch.TransitionResult{
					NextStateID: StateLoop,
					StackOp:     autarch.STACK_POP,
				},
			})
		}
		c.terminalIDs = append(c.terminalIDs, stackID)
		return stackID
	}

	inputIDs := c.nondeterministicResolve(obs)
	if len(inputIDs) == 0 {
		panic(fmt.Sprintf("grammar terminal %v not in alphabet (resolver returned no symbol)", obs))
	}
	for _, inputID := range inputIDs {
		if c.hasMatchTransition(inputID, stackID) {
			continue
		}
		c.transitions = append(c.transitions, autarch.PDATransition{
			Key: autarch.TransitionKey{
				StateID:  StateLoop,
				InputID:  inputID,
				StackTop: stackID,
			},
			Result: autarch.TransitionResult{
				NextStateID: StateLoop,
				StackOp:     autarch.STACK_POP,
			},
		})
	}
	c.terminalIDs = append(c.terminalIDs, stackID)

	return stackID
}

func (c *LLCompiler[TObservation, TMeta]) getTerminalStackID(obs TObservation) autarch.StackSymbolID {
	if id, exists := c.terminalToStackID[obs]; exists {
		return id
	}
	id := c.nextStackID
	c.nextStackID++
	c.terminalToStackID[obs] = id
	return id
}

func (c *LLCompiler[TObservation, TMeta]) getNonTerminalID(name string) autarch.StackSymbolID {
	if id, exists := c.symbolMap[name]; exists {
		return id
	}
	id := c.nextStackID
	c.nextStackID++
	c.symbolMap[name] = id
	return id
}

func (c *LLCompiler[TObservation, TMeta]) hasMatchTransition(inputID uint64, stackTop autarch.StackSymbolID) bool {
	for _, t := range c.transitions {
		if t.Key.StateID == StateLoop && t.Key.InputID == inputID && t.Key.StackTop == stackTop {
			return true
		}
	}
	return false
}

/*
CompileNPDA compiles a Context-Free Grammar into a Non-Deterministic Pushdown Automaton.

Uses pure epsilon branching for rule expansion (MODE_NPDA). Terminals in the grammar
are TObservation; the client supplies alphabet and indexer (same as for DFA/NFA). The
indexer resolves observations to symbol IDs at compile time and at run time. No
TToken→uint64 mapping is required. acceptOutcome is stored in the accept state.
Limits (maxStackNodes, maxStackDepth, maxBranches, maxEpsilonSteps) are passed to NPDACreate.

Use cases:
- Parsing general context-free grammars without LL(1) restriction
- Prototyping or grammars with ambiguity

Time complexity: O(grammar) for compilation; run time depends on NPDA execution
Space complexity: O(grammar + NPDA)

Prerequisites:
- grammar must be valid (Builder.Build or equivalent)
- alphabet and indexer must cover all terminals that appear in the grammar

Edge cases:
- Returns (npda, debugMap, nil); errors from NPDACreate are not currently returned
*/
func CompileNPDA[TObservation comparable, TMeta any, TOutcome any](
	grammar *Grammar[TObservation, TMeta],
	allocFn memarch.AllocationFn,
	alphabet []autarch.SymbolDefinition[TObservation],
	deterministicResolver autarch.DeterministicSymbolResolver[TObservation],
	nondeterministicResolve autarch.NondeterministicSymbolResolver[TObservation],
	acceptOutcome TOutcome,
	maxStackNodes uint64,
	maxStackDepth uint64,
	maxBranches uint64,
	maxEpsilonSteps uint64,
) (*autarch.NPDA[TObservation, AnnotatedOutcome[TOutcome]], []autarch.PDATransition, map[autarch.StackSymbolID]string, error) {
	compiler := CompilerCreate(
		grammar,
		MODE_NPDA,
		deterministicResolver,
		nondeterministicResolve,
	)
	transitions, debugMap, err := compiler.Compile()
	if err != nil {
		return nil, transitions, debugMap, wrapContextaGrammarError("NPDA", err, 0)
	}

	numStates := compiler.nextStateID
	outcomes := make([]AnnotatedOutcome[TOutcome], numStates)
	for i := range outcomes {
		outcomes[i] = AnnotatedOutcome[TOutcome]{}
	}
	startRule := grammar.Rules[grammar.StartSymbol]
	var acceptAnn *AnnotationID
	if startRule != nil && startRule.AnnotationID != nil {
		acceptAnn = startRule.AnnotationID
	}
	outcomes[StateAccept] = AnnotatedOutcome[TOutcome]{Value: acceptOutcome, Annotation: acceptAnn}

	npda := autarch.NPDACreate(
		allocFn,
		alphabet,
		[]uint64{StateInit},
		transitions,
		outcomes,
		nondeterministicResolve,
		maxStackNodes,
		maxStackDepth,
		maxBranches,
		maxEpsilonSteps,
	)

	return npda, transitions, debugMap, nil
}

/*
CompileDPDA compiles a Context-Free Grammar into an LL(1) Deterministic Pushdown Automaton.

Uses FIRST/FOLLOW predictive sets (MODE_DPDA); the grammar must be LL(1). Terminals
in the grammar are TObservation; the client supplies alphabet and indexer (same as
for DFA/NFA). The indexer must return exactly one symbol per observation for
determinism. No TToken→uint64 mapping is required. If any (state, input, stack) key
would have more than one transition, DPDACreate returns an error and CompileDPDA
wraps it as "grammar is not LL(1) compliant".

Use cases:
- Parsing LL(1) grammars in O(n) time with a single execution path
- Expression parsers, config formats, and syntax highlighters with unambiguous grammars

Time complexity: O(grammar) for compilation; O(n) for DPDARun where n is input length
Space complexity: O(grammar + DPDA)

Prerequisites:
- grammar must be valid and LL(1) (no predictive set overlaps)
- indexer must return exactly one symbol per observation for determinism

Edge cases:
- Returns error if DPDACreate fails (nondeterminism or epsilon/consuming conflict)
*/
func CompileDPDA[TObservation comparable, TMeta any, TOutcome any](
	grammar *Grammar[TObservation, TMeta],
	allocFn memarch.AllocationFn,
	alphabet []autarch.SymbolDefinition[TObservation],
	deterministicResolver autarch.DeterministicSymbolResolver[TObservation],
	acceptOutcome TOutcome,
	maxEpsilonSteps uint64,
) (*autarch.DPDA[TObservation, AnnotatedOutcome[TOutcome]], []autarch.PDATransition, map[autarch.StackSymbolID]string, error) {
	compiler := CompilerCreate(
		grammar,
		MODE_DPDA,
		deterministicResolver,
		func(observation TObservation) []uint64 {
			id, ok := deterministicResolver(observation)
			if !ok {
				return nil
			}
			return []uint64{id}
		},
	)
	transitions, debugMap, err := compiler.Compile()
	if err != nil {
		return nil, transitions, debugMap, wrapContextaGrammarError("DPDA", err, 0)
	}

	numStates := compiler.nextStateID
	outcomes := make([]AnnotatedOutcome[TOutcome], numStates)
	for i := range outcomes {
		outcomes[i] = AnnotatedOutcome[TOutcome]{}
	}
	startRule := grammar.Rules[grammar.StartSymbol]
	var acceptAnn *AnnotationID
	if startRule != nil && startRule.AnnotationID != nil {
		acceptAnn = startRule.AnnotationID
	}
	outcomes[StateAccept] = AnnotatedOutcome[TOutcome]{Value: acceptOutcome, Annotation: acceptAnn}

	dpda, err := autarch.DPDACreate(
		allocFn,
		alphabet,
		StateInit,
		transitions,
		outcomes,
		deterministicResolver,
		compiler.terminalIDs,
		maxEpsilonSteps,
	)

	if err != nil {
		return nil, transitions, debugMap, wrapContextaGrammarError("DPDA", err, 0)
	}

	return dpda, transitions, debugMap, nil
}
