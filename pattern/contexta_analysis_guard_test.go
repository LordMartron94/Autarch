package pattern

import "testing"

func TestComputeAnalysisProductionClausesSharedFirstWithGuards(t *testing.T) {
	const term = 1
	g := &Grammar[int, struct{}]{
		StartSymbol: "S",
		Rules: map[string]*Rule[int, struct{}]{
			"S": {
				NonTerminal: "S",
				Productions: []Production[int, struct{}]{
					{
						Symbols: []Symbol[int, struct{}]{{Type: SYMBOL_NON_TERMINAL, Name: "A"}},
						Guard:   []LookaheadConstraint[int]{{Offset: 1, Token: 10}},
					},
					{
						Symbols: []Symbol[int, struct{}]{{Type: SYMBOL_NON_TERMINAL, Name: "B"}},
						Guard:   []LookaheadConstraint[int]{{Offset: 1, Token: 11}},
					},
				},
			},
			"A": {
				NonTerminal: "A",
				Productions: []Production[int, struct{}]{
					{Symbols: []Symbol[int, struct{}]{{Type: SYMBOL_TERMINAL, Token: term}}},
				},
			},
			"B": {
				NonTerminal: "B",
				Productions: []Production[int, struct{}]{
					{Symbols: []Symbol[int, struct{}]{{Type: SYMBOL_TERMINAL, Token: term}}},
				},
			},
		},
	}
	pa := ComputeAnalysis(g)
	cl := pa.ProductionClauses["S"]
	if len(cl) != 2 {
		t.Fatalf("got %d clauses for S", len(cl))
	}
	if _, ok := cl[0].First[term]; !ok {
		t.Fatal("clause 0 should include shared terminal")
	}
	if _, ok := cl[1].First[term]; !ok {
		t.Fatal("clause 1 should include shared terminal")
	}
	if cl[0].ChildRuleName != "A" || cl[1].ChildRuleName != "B" {
		t.Fatalf("ChildRuleName: %q %q", cl[0].ChildRuleName, cl[1].ChildRuleName)
	}
	if len(cl[0].Guard) != 1 || len(cl[1].Guard) != 1 {
		t.Fatal("guards not preserved")
	}
}

func TestCompileDPDARejectsProductionGuards(t *testing.T) {
	g := &Grammar[int, struct{}]{
		StartSymbol: "S",
		Rules: map[string]*Rule[int, struct{}]{
			"S": {
				NonTerminal: "S",
				Productions: []Production[int, struct{}]{
					{
						Symbols: []Symbol[int, struct{}]{{Type: SYMBOL_TERMINAL, Token: 1}},
						Guard:   []LookaheadConstraint[int]{{Offset: 0, Token: 1}},
					},
				},
			},
		},
	}
	c := CompilerCreate(
		g,
		MODE_DPDA,
		func(obs int) (uint64, bool) { return 1, true },
		func(obs int) []uint64 { return []uint64{1} },
	)
	_, _, err := c.Compile()
	if err == nil {
		t.Fatal("expected MODE_DPDA compile to reject guarded productions")
	}
}
