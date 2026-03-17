package pattern

/*
TokenSet is a mathematical set of terminal tokens used in FIRST and FOLLOW sets.

Implemented as a map[TTokenID]struct{} for O(1) membership and iteration over
distinct tokens. Keys are the terminal token IDs from the grammar.

Time complexity: O(1) add/lookup, O(k) iteration where k is set size
Space complexity: O(k)
*/
type TokenSet[TTokenID comparable] map[TTokenID]struct{}

/*
GrammarAnalysis holds the predictive parsing data for a Contexta Grammar.

Nullable, First, and Follow are keyed by non-terminal name. Used by the LL(1)
compiler to compute predictive sets and ensure at most one production applies
per (non-terminal, lookahead token). Keys are strictly non-terminal names.

Use cases:
- Building an LL(1) DPDA via CompileDPDA (CompilerCreate with MODE_DPDA)
- Checking grammar compatibility with predictive parsing
- Debugging why a grammar is not LL(1)

Time complexity: O(1) for map access
Space complexity: O(n + f) where n is non-terminals, f is total FIRST/FOLLOW entries
*/
type GrammarAnalysis[TTokenID comparable] struct {
	Nullable map[string]bool
	First    map[string]TokenSet[TTokenID]
	Follow   map[string]TokenSet[TTokenID]
}

/*
ComputeAnalysis generates the FIRST and FOLLOW sets required for LL(1) predictive parsing.

Iterates until fixpoint: Nullable (can the non-terminal derive epsilon), First (tokens
that can start a derivation), Follow (tokens that can appear after the non-terminal in
a derivation). A true LL(1) parser typically injects an explicit EOF into Follow(StartSymbol);
the consumer handles that during compilation if needed.

Use cases:
- Required internally by CompileDPDA (MODE_DPDA)
- Standalone analysis to validate or inspect FIRST/FOLLOW

Time complexity: O(n * p * s) fixpoint over rules, productions, and symbols (typically small constants per symbol)
Space complexity: O(n * (|First| + |Follow|)) for the result

Prerequisites:
- grammar must be valid (start symbol and all referenced non-terminals defined)

Edge cases:
- Start symbol's Follow set does not include EOF unless caller injects it
*/
func ComputeAnalysis[TTokenID comparable, TMeta any](grammar *Grammar[TTokenID, TMeta]) *GrammarAnalysis[TTokenID] {
	analysis := &GrammarAnalysis[TTokenID]{
		Nullable: make(map[string]bool),
		First:    make(map[string]TokenSet[TTokenID]),
		Follow:   make(map[string]TokenSet[TTokenID]),
	}

	// Initialize sets for all Non-Terminals
	for nt := range grammar.Rules {
		analysis.First[nt] = make(TokenSet[TTokenID])
		analysis.Follow[nt] = make(TokenSet[TTokenID])
	}

	computeNullable(grammar, analysis)
	computeFirst(grammar, analysis)
	computeFollow(grammar, analysis)

	return analysis
}

// ------------------------------------------------------------- CORE ALGORITHMS

func computeNullable[TTokenID comparable, TMeta any](grammar *Grammar[TTokenID, TMeta], analysis *GrammarAnalysis[TTokenID]) {
	for changed := true; changed; {
		changed = false

		for nt, rule := range grammar.Rules {
			if analysis.Nullable[nt] {
				continue // Already proven nullable
			}

			for _, prod := range rule.Productions {
				allNullable := true

				for _, sym := range prod.Symbols {
					if sym.Type == SYMBOL_TERMINAL {
						allNullable = false
						break
					}
					if sym.Type == SYMBOL_NON_TERMINAL && !analysis.Nullable[sym.Name] {
						allNullable = false
						break
					}
				}

				if allNullable {
					analysis.Nullable[nt] = true
					changed = true
					break // No need to check other productions for this NT
				}
			}
		}
	}
}

func computeFirst[TTokenID comparable, TMeta any](grammar *Grammar[TTokenID, TMeta], analysis *GrammarAnalysis[TTokenID]) {
	for changed := true; changed; {
		changed = false

		for nt, rule := range grammar.Rules {
			firstSet := analysis.First[nt]

			for _, prod := range rule.Productions {
				for _, sym := range prod.Symbols {
					if sym.Type == SYMBOL_TERMINAL {
						if addToken(firstSet, sym.Token) {
							changed = true
						}
						break // Terminal stops the chain
					}

					if sym.Type == SYMBOL_NON_TERMINAL {
						if mergeSets(firstSet, analysis.First[sym.Name]) {
							changed = true
						}
						// If this non-terminal isn't nullable, it blocks the rest of the symbols
						if !analysis.Nullable[sym.Name] {
							break
						}
					}
					// SYMBOL_EPSILON continues to the next symbol automatically
				}
			}
		}
	}
}

func computeFollow[TTokenID comparable, TMeta any](grammar *Grammar[TTokenID, TMeta], analysis *GrammarAnalysis[TTokenID]) {
	// Note: A true LL(1) parser requires an explicit EOF token injected into Follow(StartSymbol).
	// The consumer should handle injecting EOF during the compilation phase.

	for changed := true; changed; {
		changed = false

		for _, rule := range grammar.Rules {
			for _, prod := range rule.Productions {

				// Evaluate what follows every symbol in this specific production
				for i, sym := range prod.Symbols {
					if sym.Type != SYMBOL_NON_TERMINAL {
						continue
					}

					followSet := analysis.Follow[sym.Name]
					allFollowingNullable := true

					// Look at the symbols directly to the right of 'sym'
					for j := i + 1; j < len(prod.Symbols); j++ {
						nextSym := prod.Symbols[j]

						if nextSym.Type == SYMBOL_TERMINAL {
							if addToken(followSet, nextSym.Token) {
								changed = true
							}
							allFollowingNullable = false
							break
						}

						if nextSym.Type == SYMBOL_NON_TERMINAL {
							if mergeSets(followSet, analysis.First[nextSym.Name]) {
								changed = true
							}
							if !analysis.Nullable[nextSym.Name] {
								allFollowingNullable = false
								break
							}
						}
					}

					// If everything to the right is nullable (or we are at the end of the production),
					// whatever follows the parent Rule also follows this symbol.
					if allFollowingNullable {
						if mergeSets(followSet, analysis.Follow[rule.NonTerminal]) {
							changed = true
						}
					}
				}
			}
		}
	}
}

// ------------------------------------------------------------- HELPERS

func addToken[TTokenID comparable](set TokenSet[TTokenID], token TTokenID) bool {
	if _, exists := set[token]; !exists {
		set[token] = struct{}{}
		return true
	}
	return false
}

func mergeSets[TTokenID comparable](dst, src TokenSet[TTokenID]) bool {
	changed := false
	for token := range src {
		if _, exists := dst[token]; !exists {
			dst[token] = struct{}{}
			changed = true
		}
	}
	return changed
}
