package pattern

import "fmt"

/*
SymbolType classifies symbols in a Context-Free Grammar as terminal, non-terminal, or epsilon.

Terminals are tokens produced by the lexer (e.g., from Regula). Non-terminals are
grammar rules that expand to sequences of symbols. Epsilon denotes the empty string
for optional or nullable productions.

Time complexity: O(1)
Space complexity: O(1)
*/
type SymbolType uint8

const (
	SYMBOL_TERMINAL    SymbolType = iota
	SYMBOL_NON_TERMINAL
	SYMBOL_EPSILON
)

/*
Symbol represents one element in a production: either a terminal (Token), a non-terminal (Name), or epsilon.

Type determines which field is meaningful. For SYMBOL_TERMINAL, Token is the lexer token ID.
For SYMBOL_NON_TERMINAL, Name is the rule name. For SYMBOL_EPSILON, no other field is used.

Time complexity: O(1)
Space complexity: O(1)
*/
type Symbol[TTokenID comparable] struct {
	Type  SymbolType
	Name  string
	Token TTokenID
}

/*
Production is a single right-hand side of a rule: an ordered sequence of symbols.

A rule may have multiple productions (alternatives). Empty Symbols or a single
SYMBOL_EPSILON represent an epsilon production.

Time complexity: O(1) for field access
Space complexity: O(k) where k is len(Symbols)
*/
type Production[TTokenID comparable] struct {
	Symbols []Symbol[TTokenID]
}

/*
Rule associates a non-terminal name with one or more productions (alternatives).

The grammar expands this non-terminal by choosing one of the productions based on
lookahead (DPDA/LL(1)) or by exploring all alternatives (NPDA).

Time complexity: O(1) for field access
Space complexity: O(p) where p is total productions and their symbols
*/
type Rule[TTokenID comparable] struct {
	NonTerminal string
	Productions []Production[TTokenID]
}

/*
Grammar is a Context-Free Grammar with a start symbol and a map of rules by non-terminal name.

Rules is keyed by non-terminal name; each entry is a Rule with one or more productions.
StartSymbol must be the name of a rule that exists in Rules. Terminals in productions
are of type TTokenID (the client's observation/token type). When compiling to a PDA,
use pattern.CompilerCreate(grammar, mode, indexer) or pattern.CompileNPDA/CompileDPDA
with the same alphabet and SymbolIndexer so the indexer resolves terminals to symbol IDs.

Time complexity: O(1) for map lookup by name
Space complexity: O(r + s) where r is rules, s is total symbols in all productions
*/
type Grammar[TTokenID comparable] struct {
	StartSymbol string
	Rules       map[string]*Rule[TTokenID]
}

/*
Builder constructs a Contexta Grammar safely with validation on Build.

Use BuilderCreate, then Define rules with NonTerm/Term/Epsilon and Seq/Define.
Build validates that the start symbol is defined and that all referenced non-terminals
exist, then returns the Grammar and nil, or nil and an error.
*/
type Builder[TTokenID comparable] struct {
	grammar *Grammar[TTokenID]
}

/*
BuilderCreate allocates a new Builder with the given start symbol.

The start symbol must be defined via Define before Build is called.

Time complexity: O(1)
Space complexity: O(1)
*/
func BuilderCreate[TTokenID comparable](startSymbol string) *Builder[TTokenID] {
	return &Builder[TTokenID]{
		grammar: &Grammar[TTokenID]{
			StartSymbol: startSymbol,
			Rules:       make(map[string]*Rule[TTokenID]),
		},
	}
}

// ------------------------------------------------------------- SYMBOL REFERENCES

/*
NonTerm creates a reference to a structural rule (non-terminal) by name.

The rule does not need to be defined yet; it will be validated when Build is called.

Time complexity: O(1)
Space complexity: O(1)
*/
func (b *Builder[TTokenID]) NonTerm(name string) Symbol[TTokenID] {
	return Symbol[TTokenID]{
		Type: SYMBOL_NON_TERMINAL,
		Name: name,
	}
}

/*
Term creates a reference to a lexical token produced by the lexer (e.g., Regula).

The token type TTokenID is the same as the observation type used when compiling the
grammar to a PDA. When compiling to NPDA/DPDA, pass the same alphabet and
SymbolIndexer[TObservation]; the indexer resolves terminals to symbol IDs. No
manual TTokenID→uint64 mapping is required.

Time complexity: O(1)
Space complexity: O(1)
*/
func (b *Builder[TTokenID]) Term(token TTokenID) Symbol[TTokenID] {
	return Symbol[TTokenID]{
		Type:  SYMBOL_TERMINAL,
		Token: token,
	}
}

/*
Epsilon creates an empty-string symbol for optional or nullable productions.

Time complexity: O(1)
Space complexity: O(1)
*/
func (b *Builder[TTokenID]) Epsilon() Symbol[TTokenID] {
	return Symbol[TTokenID]{
		Type: SYMBOL_EPSILON,
	}
}

// ------------------------------------------------------------- RULE CONSTRUCTION

/*
Seq creates a single production as a sequence of symbols.

Use with Define to declare one alternative for a non-terminal.

Time complexity: O(k) where k is len(symbols)
Space complexity: O(k)
*/
func Seq[TTokenID comparable](symbols ...Symbol[TTokenID]) Production[TTokenID] {
	return Production[TTokenID]{
		Symbols: symbols,
	}
}

/*
Define binds a non-terminal to one or more productions (alternatives).

Panics if the non-terminal is already defined. Use Seq to build each production.
Validation of references to other non-terminals happens at Build.

Time complexity: O(p) where p is total symbols in productions
Space complexity: O(p)
*/
func (b *Builder[TTokenID]) Define(nonTerminal string, productions ...Production[TTokenID]) {
	if _, exists := b.grammar.Rules[nonTerminal]; exists {
		panic(fmt.Sprintf("Rule '%s' is already defined", nonTerminal))
	}

	b.grammar.Rules[nonTerminal] = &Rule[TTokenID]{
		NonTerminal: nonTerminal,
		Productions: productions,
	}
}

/*
Build validates the grammar and returns it, or an error if invalid.

Checks: start symbol has a rule; every non-terminal referenced in any production
is defined. Returns (*Grammar, nil) on success, (nil, error) on validation failure.

Time complexity: O(r * p * s) where r is rules, p is productions per rule, s is symbols per production
Space complexity: O(1) — grammar already allocated by builder

Edge cases:
- Returns error if start symbol has no rule
- Returns error for undefined non-terminals (e.g., typo in NonTerm name)
*/
func (b *Builder[TTokenID]) Build() (*Grammar[TTokenID], error) {
	// Validation: Check that the start symbol exists
	if _, exists := b.grammar.Rules[b.grammar.StartSymbol]; !exists {
		return nil, fmt.Errorf("start symbol '%s' has no defined rule", b.grammar.StartSymbol)
	}

	// Validation: Check for orphaned non-terminals (used but never defined)
	for _, rule := range b.grammar.Rules {
		for _, prod := range rule.Productions {
			for _, sym := range prod.Symbols {
				if sym.Type == SYMBOL_NON_TERMINAL {
					if _, exists := b.grammar.Rules[sym.Name]; !exists {
						return nil, fmt.Errorf("undefined non-terminal: '%s'", sym.Name)
					}
				}
			}
		}
	}

	return b.grammar, nil
}
