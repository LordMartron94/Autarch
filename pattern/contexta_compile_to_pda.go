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
has at most one production. In MODE_NPDA, expansions are driven by epsilon only. Internal
state IDs (StateInit, StateLoop, StateAccept plus synthetic states) and stack symbol IDs
are assigned during Compile. Use CompileNPDA or CompileDPDA for a full automaton; use
CompilerCreate and Compile for transitions and a debug map only.
*/
type LLCompiler struct {
	grammar *Grammar[uint64]
	mode    CompilerMode

	// Predictive analysis (populated only in DPDA mode)
	analysis *GrammarAnalysis[uint64]

	// Registries to assign unique IDs to grammar symbols
	nextStackID autarch.StackSymbolID
	symbolMap   map[string]autarch.StackSymbolID

	terminalIDs []autarch.StackSymbolID

	transitions []autarch.PDATransition
	nextStateID uint64
}

/*
CompilerCreate allocates an LL compiler for the given grammar and mode.

In MODE_DPDA, ComputeAnalysis is run to populate FIRST/FOLLOW for predictive sets.
Grammar must use uint64 as TTokenID (terminal token IDs) for compatibility with
autarch stack symbol IDs and indexer.

Time complexity: O(1) for MODE_NPDA; O(grammar size) for MODE_DPDA (ComputeAnalysis)
Space complexity: O(grammar + analysis)
*/
func CompilerCreate(grammar *Grammar[uint64], mode CompilerMode) *LLCompiler {
	c := &LLCompiler{
		grammar:     grammar,
		mode:        mode,
		nextStackID: BottomMarkerID + 1,
		symbolMap:   make(map[string]autarch.StackSymbolID),
		transitions: make([]autarch.PDATransition, 0),
		nextStateID: StateAccept + 1,
		terminalIDs: make([]autarch.StackSymbolID, 0),
	}

	if mode == MODE_DPDA {
		// Assuming Contexta's ComputeAnalysis is available to generate FIRST/FOLLOW sets
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
func (c *LLCompiler) Compile() ([]autarch.PDATransition, map[autarch.StackSymbolID]string) {

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
	resolutionMap[BottomMarkerID] = "$BOTTOM"

	return c.transitions, resolutionMap
}

func (c *LLCompiler) compileRules() {
	for nonTerminalName, rule := range c.grammar.Rules {
		ntID := c.getNonTerminalID(nonTerminalName)

		for _, prod := range rule.Productions {
			c.compileProduction(nonTerminalName, ntID, prod)
		}
	}
}

func (c *LLCompiler) compileProduction(ntName string, ntID autarch.StackSymbolID, prod Production[uint64]) {
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

// computePredictiveSet returns FIRST(prod) U FOLLOW(ntName) if prod is nullable.
func (c *LLCompiler) computePredictiveSet(ntName string, prod Production[uint64]) []uint64 {
	firstSet := make(TokenSet[uint64])
	isNullable := true

	for _, sym := range prod.Symbols {
		if sym.Type == SYMBOL_TERMINAL {
			firstSet[sym.Token] = struct{}{}
			isNullable = false
			break
		} else if sym.Type == SYMBOL_NON_TERMINAL {
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

	var tokens []uint64
	for t := range firstSet {
		tokens = append(tokens, t)
	}
	return tokens
}

// resolveSymbolID maps a Contexta symbol to a StackSymbolID.
// It also generates the "Match" consuming transitions for Terminals.
func (c *LLCompiler) resolveSymbolID(sym Symbol[uint64]) autarch.StackSymbolID {
	if sym.Type == SYMBOL_NON_TERMINAL {
		return c.getNonTerminalID(sym.Name)
	}

	tokenName := string(rune(sym.Token))
	termID := c.getTerminalID(tokenName, sym.Token)

	if !c.hasMatchTransition(sym.Token, termID) {
		c.transitions = append(c.transitions, autarch.PDATransition{
			Key: autarch.TransitionKey{
				StateID:  StateLoop,
				InputID:  sym.Token,
				StackTop: termID,
			},
			Result: autarch.TransitionResult{
				NextStateID: StateLoop,
				StackOp:     autarch.STACK_POP,
			},
		})
		c.terminalIDs = append(c.terminalIDs, termID)
	}

	return termID
}

// --- Internal ID Managers ---

func (c *LLCompiler) getNonTerminalID(name string) autarch.StackSymbolID {
	if id, exists := c.symbolMap[name]; exists {
		return id
	}
	id := c.nextStackID
	c.nextStackID++
	c.symbolMap[name] = id
	return id
}

func (c *LLCompiler) getTerminalID(name string, token uint64) autarch.StackSymbolID {
	key := fmt.Sprintf("$TERM_%d", token)
	if id, exists := c.symbolMap[key]; exists {
		return id
	}
	id := c.nextStackID
	c.nextStackID++
	c.symbolMap[key] = id
	return id
}

func (c *LLCompiler) allocateSyntheticState() uint64 {
	id := c.nextStateID
	c.nextStateID++
	return id
}

func (c *LLCompiler) hasMatchTransition(inputID uint64, stackTop autarch.StackSymbolID) bool {
	for _, t := range c.transitions {
		if t.Key.StateID == StateLoop && t.Key.InputID == inputID && t.Key.StackTop == stackTop {
			return true
		}
	}
	return false
}

/*
CompileNPDA compiles a Context-Free Grammar into a Non-Deterministic Pushdown Automaton.

Uses pure epsilon branching for rule expansion (MODE_NPDA). alphabet and indexer must
map observations to symbol IDs consistent with the grammar's terminal token IDs (uint64).
acceptOutcome is stored in the accept state; other states get the zero value of TStateOutcome.
Limits (maxStackNodes, maxStackDepth, maxBranches, maxEpsilonSteps) are passed to NPDACreate.

Use cases:
- Parsing general context-free grammars without LL(1) restriction
- Prototyping or grammars with ambiguity

Time complexity: O(grammar) for compilation; run time depends on NPDA execution
Space complexity: O(grammar + NPDA)

Prerequisites:
- grammar must be valid (Builder.Build or equivalent)
- alphabet and indexer must use uint64 token IDs matching grammar terminals

Edge cases:
- Returns (npda, debugMap, nil); errors from NPDACreate are not currently returned
*/
func CompileNPDA[TObservation, TStateOutcome any](
	grammar *Grammar[uint64],
	allocFn memarch.AllocationFn,
	alphabet []autarch.SymbolDefinition[TObservation],
	indexer autarch.SymbolIndexer[TObservation],
	acceptOutcome TStateOutcome,
	maxStackNodes uint64,
	maxStackDepth uint64,
	maxBranches uint64,
	maxEpsilonSteps uint64,
) (*autarch.NPDA[TObservation, TStateOutcome], map[autarch.StackSymbolID]string, error) {

	compiler := CompilerCreate(grammar, MODE_NPDA)
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

Uses FIRST/FOLLOW predictive sets (MODE_DPDA); the grammar must be LL(1). If any
(state, input, stack) key would have more than one transition, DPDACreate returns
an error and CompileDPDA wraps it as "grammar is not LL(1) compliant". alphabet,
indexer, and acceptOutcome behave as in CompileNPDA. terminalIDs are the stack
symbol IDs that represent terminals (used for lookahead consumption). maxEpsilonSteps
limits consecutive epsilon moves to detect cycles.

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
func CompileDPDA[TObservation, TStateOutcome any](
	grammar *Grammar[uint64],
	allocFn memarch.AllocationFn,
	alphabet []autarch.SymbolDefinition[TObservation],
	indexer autarch.SymbolIndexer[TObservation],
	acceptOutcome TStateOutcome,
	maxEpsilonSteps uint64,
) (*autarch.DPDA[TObservation, TStateOutcome], map[autarch.StackSymbolID]string, error) {

	compiler := CompilerCreate(grammar, MODE_DPDA)
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
