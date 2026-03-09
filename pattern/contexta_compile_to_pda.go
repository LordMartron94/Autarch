package pattern

import (
	"autarch"
	"fmt"
	"memarch"
)

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
type LLCompiler[TObservation comparable] struct {
	grammar *Grammar[TObservation]
	mode    CompilerMode
	indexer autarch.SymbolIndexer[TObservation]

	// Predictive analysis (populated only in DPDA mode)
	analysis *GrammarAnalysis[TObservation]

	// Registries to assign unique IDs to grammar symbols
	nextStackID          autarch.StackSymbolID
	symbolMap            map[string]autarch.StackSymbolID
	terminalToStackID   map[TObservation]autarch.StackSymbolID

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
func CompilerCreate[TObservation comparable](
	grammar *Grammar[TObservation],
	mode CompilerMode,
	indexer autarch.SymbolIndexer[TObservation],
) *LLCompiler[TObservation] {
	c := &LLCompiler[TObservation]{
		grammar:             grammar,
		mode:                mode,
		indexer:             indexer,
		nextStackID:         BottomMarkerID + 1,
		symbolMap:           make(map[string]autarch.StackSymbolID),
		terminalToStackID:   make(map[TObservation]autarch.StackSymbolID),
		transitions:         make([]autarch.PDATransition, 0),
		nextStateID:         StateAccept + 1,
		terminalIDs:         make([]autarch.StackSymbolID, 0),
	}

	if mode == MODE_DPDA {
		c.analysis = ComputeAnalysis(grammar)
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
func (c *LLCompiler[TObservation]) Compile() ([]autarch.PDATransition, map[autarch.StackSymbolID]string) {

	startStackID := c.getNonTerminalID(c.grammar.StartSymbol)

	// 1. Initialization Phase: Push the Start Symbol and enter the loop
	c.transitions = append(c.transitions, autarch.PDATransition{
		Key: autarch.TransitionKey{
			StateID:  StateInit,
			InputID:  autarch.EpsilonSymbolID,
			StackTop: BottomMarkerID,
		},
		Result: autarch.TransitionResult{
			NextStateID:  StateLoop,
			StackOp:      autarch.STACK_PUSH,
			PushSymbolID: startStackID,
		},
	})

	// 2. Acceptance Phase: If we see the bottom marker in the loop, accept.
	// In DPDA mode, this transition is taken when input is fully consumed (Epsilon).
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

	// 3. Compile the Rules
	c.compileRules()

	resolutionMap := make(map[autarch.StackSymbolID]string)
	for name, id := range c.symbolMap {
		resolutionMap[id] = name
	}
	for obs, id := range c.terminalToStackID {
		resolutionMap[id] = fmt.Sprint(obs)
	}
	resolutionMap[BottomMarkerID] = "$BOTTOM"

	return c.transitions, resolutionMap
}

func (c *LLCompiler[TObservation]) compileRules() {
	for nonTerminalName, rule := range c.grammar.Rules {
		ntID := c.getNonTerminalID(nonTerminalName)

		for _, prod := range rule.Productions {
			c.compileProduction(nonTerminalName, ntID, prod)
		}
	}
}

func (c *LLCompiler[TObservation]) compileProduction(ntName string, ntID autarch.StackSymbolID, prod Production[TObservation]) {
	symbols := prod.Symbols
	k := len(symbols)

	var firstOp autarch.StackOperation
	var firstPushID autarch.StackSymbolID
	var entryNextState uint64

	// Determine the mechanical stack sequence for the right-hand side
	if k == 0 || (k == 1 && symbols[0].Type == SYMBOL_EPSILON) {
		firstOp = autarch.STACK_POP
		firstPushID = 0
		entryNextState = StateLoop
	} else {
		// Resolve IDs for all symbols in the production
		var pushIDs []autarch.StackSymbolID
		for _, sym := range symbols {
			pushIDs = append(pushIDs, c.resolveSymbolID(sym))
		}

		firstOp = autarch.STACK_REPLACE
		firstPushID = pushIDs[k-1]
		entryNextState = StateLoop

		// If we have more than one symbol, we need a synthetic epsilon chain to push the rest
		if k > 1 {
			nextState := StateLoop

			// Build backwards from X_1 up to X_{k-1}
			for i := 0; i < k-1; i++ {
				currentState := c.allocateSyntheticState()
				expectedStackTop := pushIDs[i+1] // What we just pushed in the prior step

				c.transitions = append(c.transitions, autarch.PDATransition{
					Key: autarch.TransitionKey{
						StateID:  currentState,
						InputID:  autarch.EpsilonSymbolID,
						StackTop: expectedStackTop,
					},
					Result: autarch.TransitionResult{
						NextStateID:  nextState,
						StackOp:      autarch.STACK_PUSH,
						PushSymbolID: pushIDs[i],
					},
				})
				nextState = currentState
			}
			entryNextState = nextState
		}
	}

	// Determine what input triggers this rule expansion
	var triggers []uint64

	if c.mode == MODE_DPDA {
		// LL(1) Predictive Parsing: Expand only when looking at a token in the Predictive Set
		triggers = c.computePredictiveSet(ntName, prod)
		if len(triggers) == 0 {
			panic(fmt.Sprintf("Grammar Error: Production for '%s' can never be reached (empty predictive set)", ntName))
		}
	} else {
		// Pure NPDA: Blindly branch on Epsilon
		triggers = []uint64{autarch.EpsilonSymbolID}
	}

	// Bind the entry transitions
	for _, trigger := range triggers {
		c.transitions = append(c.transitions, autarch.PDATransition{
			Key: autarch.TransitionKey{
				StateID:  StateLoop,
				InputID:  trigger,
				StackTop: ntID,
			},
			Result: autarch.TransitionResult{
				NextStateID:  entryNextState,
				StackOp:      firstOp,
				PushSymbolID: firstPushID,
			},
		})
	}
}

// computePredictiveSet returns input symbol IDs (uint64) for transition keys:
// FIRST(prod) ∪ FOLLOW(ntName) if prod is nullable, resolved via the indexer.
func (c *LLCompiler[TObservation]) computePredictiveSet(ntName string, prod Production[TObservation]) []uint64 {
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
		syms := c.indexer(obs)
		if len(syms) == 0 {
			panic(fmt.Sprintf("grammar terminal %v not in alphabet (indexer returned no symbol)", obs))
		}
		for _, s := range syms {
			seen[s.SymbolID] = struct{}{}
		}
	}
	out := make([]uint64, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// resolveSymbolID maps a Contexta symbol to a StackSymbolID and emits match transitions for terminals.
func (c *LLCompiler[TObservation]) resolveSymbolID(sym Symbol[TObservation]) autarch.StackSymbolID {
	if sym.Type == SYMBOL_NON_TERMINAL {
		return c.getNonTerminalID(sym.Name)
	}
	if sym.Type == SYMBOL_EPSILON {
		panic("resolveSymbolID: epsilon has no stack symbol ID")
	}

	obs := sym.Token
	syms := c.indexer(obs)
	if len(syms) == 0 {
		panic(fmt.Sprintf("grammar terminal %v not in alphabet (indexer returned no symbol)", obs))
	}
	if c.mode == MODE_DPDA && len(syms) != 1 {
		panic(fmt.Sprintf("DPDA requires exactly one symbol per observation; indexer returned %d for %v", len(syms), obs))
	}
	inputID := syms[0].SymbolID
	stackID := c.getTerminalStackID(obs)

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
		c.terminalIDs = append(c.terminalIDs, stackID)
	}

	return stackID
}

func (c *LLCompiler[TObservation]) getTerminalStackID(obs TObservation) autarch.StackSymbolID {
	if id, exists := c.terminalToStackID[obs]; exists {
		return id
	}
	id := c.nextStackID
	c.nextStackID++
	c.terminalToStackID[obs] = id
	return id
}

func (c *LLCompiler[TObservation]) getNonTerminalID(name string) autarch.StackSymbolID {
	if id, exists := c.symbolMap[name]; exists {
		return id
	}
	id := c.nextStackID
	c.nextStackID++
	c.symbolMap[name] = id
	return id
}

func (c *LLCompiler[TObservation]) allocateSyntheticState() uint64 {
	id := c.nextStateID
	c.nextStateID++
	return id
}

func (c *LLCompiler[TObservation]) hasMatchTransition(inputID uint64, stackTop autarch.StackSymbolID) bool {
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
func CompileNPDA[TObservation comparable, TStateOutcome any](
	grammar *Grammar[TObservation],
	allocFn memarch.AllocationFn,
	alphabet []autarch.SymbolDefinition[TObservation],
	indexer autarch.SymbolIndexer[TObservation],
	acceptOutcome TStateOutcome,
	maxStackNodes uint64,
	maxStackDepth uint64,
	maxBranches uint64,
	maxEpsilonSteps uint64,
) (*autarch.NPDA[TObservation, TStateOutcome], map[autarch.StackSymbolID]string, error) {

	compiler := CompilerCreate(grammar, MODE_NPDA, indexer)
	transitions, debugMap := compiler.Compile()

	// Build the outcomes array.
	// The length must cover the highest synthetic state ID generated by the compiler.
	numStates := compiler.nextStateID
	outcomes := make([]TStateOutcome, numStates)
	outcomes[StateAccept] = acceptOutcome

	npda := autarch.NPDACreate(
		allocFn,
		alphabet,
		[]uint64{StateInit}, // Starts at the compiler's init state
		transitions,
		outcomes,
		indexer,
		maxStackNodes,
		maxStackDepth,
		maxBranches,
		maxEpsilonSteps,
	)

	return npda, debugMap, nil
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
func CompileDPDA[TObservation comparable, TStateOutcome any](
	grammar *Grammar[TObservation],
	allocFn memarch.AllocationFn,
	alphabet []autarch.SymbolDefinition[TObservation],
	indexer autarch.SymbolIndexer[TObservation],
	acceptOutcome TStateOutcome,
	maxEpsilonSteps uint64,
) (*autarch.DPDA[TObservation, TStateOutcome], map[autarch.StackSymbolID]string, error) {

	compiler := CompilerCreate(grammar, MODE_DPDA, indexer)
	transitions, debugMap := compiler.Compile()

	numStates := compiler.nextStateID
	outcomes := make([]TStateOutcome, numStates)
	outcomes[StateAccept] = acceptOutcome

	dpda, err := autarch.DPDACreate(
		allocFn,
		alphabet,
		StateInit,
		transitions,
		outcomes,
		indexer,
		compiler.terminalIDs, // Required for lookahead consumption logic
		maxEpsilonSteps,
	)

	if err != nil {
		return nil, nil, fmt.Errorf("grammar is not LL(1) compliant: %w", err)
	}

	return dpda, debugMap, nil
}
