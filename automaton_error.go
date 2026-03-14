package autarch

/*
AutomatonErrorKind classifies structured automaton errors across construction
and execution paths.

The intent is to provide a stable, machine-readable taxonomy for callers
without forcing them to parse human-readable strings. Textual messages are
derived from these kinds and their payloads.

Use cases:
- DPDA/NPDA constructor validation (nondeterminism, epsilon conflicts)
- DPDA/NPDA runtime failures (syntax errors, indexer ambiguity, stack limits)
- DFA/NFA execution errors (invalid symbols)

Time complexity: O(1)
Space complexity: O(1)
*/
type AutomatonErrorKind uint8

const (
	AutomatonErrorUnknown AutomatonErrorKind = iota

	// DPDA construction-time errors
	AutomatonErrorNondeterminismDPDA
	AutomatonErrorEpsilonConflictDPDA

	// DPDA runtime errors
	AutomatonErrorStackUnderflowDPDA
	AutomatonErrorIndexerAmbiguityDPDA
	AutomatonErrorSyntaxDPDA

	// NPDA runtime / limit errors
	AutomatonErrorInvalidSymbolNPDA
	AutomatonErrorBranchLimitNPDA

	// Generic DFA/NFA symbol errors
	AutomatonErrorInvalidSymbolFinite
)

/*
DPDANondeterminismConflict describes one conflicting DPDA transition sharing
the same (state, input, stack) triple.

Use cases:
- Reporting all conflicting keys when checking LL(1) compliance
- Mapping conflicts back to higher-level grammar rules

Time complexity: O(1)
Space complexity: O(1)
*/
type DPDANondeterminismConflict struct {
	StateID  uint64
	InputID  uint64
	StackTop StackSymbolID
}

/*
DPDARuntimeContext captures the local context for a DPDA runtime error.

Position is the index into the input sequence where the failure was detected.
Observation is the offending input value as seen by the DPDA (typically a
token or rune).

Time complexity: O(1)
Space complexity: O(1)
*/
type DPDARuntimeContext struct {
	Position    int
	StateID     uint64
	StackTop    StackSymbolID
	Observation any
}

/*
AutomatonError is a structured error type used by automata in this package.

It implements the error interface while exposing machine-readable fields so
callers can branch on Kind and inspect payloads instead of parsing messages.
Message is always derived from the structured fields and should be treated
as a presentation detail.

Use cases:
- Conveying detailed constructor/runtime failures to higher layers
- Enabling language/tooling integrations to build rich diagnostics

Time complexity: O(1) for construction
Space complexity: O(1)
*/
type AutomatonError struct {
	Kind      AutomatonErrorKind
	Automaton string

	// Human-oriented summary, derived from structured fields.
	Message string

	// Optional DPDA-specific payloads.
	DPDAConflicts []DPDANondeterminismConflict
	DPDAContext   *DPDARuntimeContext

	// Optional NPDA/DFA/NFA symbol context.
	InputID uint64
}

// Error implements the error interface.
func (e *AutomatonError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return "automaton error"
}

