package pattern

import (
	"sort"
	"strings"
)

// ============================================================
// FORMATTER (Semantic Layer)
// ============================================================

/*
ContextaDebugFormatter supplies string rendering for Contexta grammar debug dumps.

FormatSymbolType and FormatToken are required. FormatRuleName is optional (defaults to identity).
Optional Color* hooks allow terminal or IDE coloring of rule names, tokens, and symbols.
*/
type ContextaDebugFormatter[TTokenID comparable] struct {
	/* REQUIRED */
	FormatSymbolType func(SymbolType) string
	FormatToken      func(TTokenID) string

	/* Optional: rule name display (default: identity) */
	FormatRuleName func(string) string

	/* Optional coloring */
	ColorRuleName  func(string) string
	ColorToken     func(string) string
	ColorNonTerm   func(string) string
	ColorEpsilon   func(string) string
	ColorMeta      func(string) string
}

func (f ContextaDebugFormatter[TTokenID]) validate() {
	if f.FormatSymbolType == nil {
		panic("ContextaDebugFormatter: FormatSymbolType is required")
	}
	if f.FormatToken == nil {
		panic("ContextaDebugFormatter: FormatToken is required")
	}
}

/*
NewContextaCleanFormatter returns a minimal formatter for quick grammar inspection.

Symbol types render as T/NT/ε; tokens use the provided obsFormatter; rule names are unchanged.
*/
func NewContextaCleanFormatter[T comparable](tokenFormatter func(T) string) ContextaDebugFormatter[T] {
	return ContextaDebugFormatter[T]{
		FormatSymbolType: func(st SymbolType) string {
			switch st {
			case SYMBOL_TERMINAL:
				return "T"
			case SYMBOL_NON_TERMINAL:
				return "NT"
			case SYMBOL_EPSILON:
				return "ε"
			default:
				return "?"
			}
		},
		FormatToken:     tokenFormatter,
		FormatRuleName:  func(s string) string { return s },
	}
}

// ============================================================
// RENDERER (Layout + IO)
// ============================================================

/*
ContextaDebugger renders a Contexta Grammar to a human-readable dump.

Similar to RegulaDebugger: formatter drives semantic rendering, glyphs and gutter
control layout. Use DumpString to produce the full dump; optionally attach
GrammarAnalysis for nullable/first/follow in meta (future).
*/
type ContextaDebugger[TTokenID comparable] struct {
	Formatter ContextaDebugFormatter[TTokenID]

	GutterWidth int

	GlyphMid   string
	GlyphLast  string
	GlyphVert  string
	GlyphBlank string
}

/*
NewContextaDebugger creates a ContextaDebugger with the given formatter.

The formatter must have FormatSymbolType and FormatToken set (validate is called).
*/
func NewContextaDebugger[TTokenID comparable](
	formatter ContextaDebugFormatter[TTokenID],
) *ContextaDebugger[TTokenID] {
	formatter.validate()
	return &ContextaDebugger[TTokenID]{
		Formatter:   formatter,
		GutterWidth: 40,
		GlyphMid:    "├─ ",
		GlyphLast:   "└─ ",
		GlyphVert:   "│  ",
		GlyphBlank:  "   ",
	}
}

/*
DumpString produces a human-readable dump of the grammar: start symbol, then each
rule with its productions (each production as a sequence of symbols).
Rule order is deterministic (sorted by rule name).
*/
func (d *ContextaDebugger[TTokenID]) DumpString(grammar *Grammar[TTokenID]) string {
	if grammar == nil {
		return "(nil grammar)\n"
	}
	var b strings.Builder

	// Start symbol
	startStr := grammar.StartSymbol
	if d.Formatter.FormatRuleName != nil {
		startStr = d.Formatter.FormatRuleName(grammar.StartSymbol)
	}
	if d.Formatter.ColorRuleName != nil {
		startStr = d.Formatter.ColorRuleName(startStr)
	}
	b.WriteString("Start: ")
	b.WriteString(startStr)
	b.WriteByte('\n')

	// Rules in sorted order
	names := make([]string, 0, len(grammar.Rules))
	for name := range grammar.Rules {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		rule := grammar.Rules[name]
		if rule == nil {
			continue
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		d.writeRule(&b, name, rule, "", i == len(names)-1)
	}
	return b.String()
}

func (d *ContextaDebugger[TTokenID]) writeRule(
	w *strings.Builder,
	ruleName string,
	rule *Rule[TTokenID],
	prefix string,
	isLast bool,
) {
	f := d.Formatter
	displayName := ruleName
	if f.FormatRuleName != nil {
		displayName = f.FormatRuleName(ruleName)
	}
	if f.ColorRuleName != nil {
		displayName = f.ColorRuleName(displayName)
	}

	if len(prefix) > 0 {
		if isLast {
			w.WriteString(prefix + d.GlyphLast)
		} else {
			w.WriteString(prefix + d.GlyphMid)
		}
	}
	w.WriteString("Rule ")
	w.WriteString(displayName)
	w.WriteByte('\n')

	newPrefix := prefix
	if len(prefix) > 0 {
		if isLast {
			newPrefix += d.GlyphBlank
		} else {
			newPrefix += d.GlyphVert
		}
	}

	for j, prod := range rule.Productions {
		prodLast := j == len(rule.Productions)-1
		if prodLast {
			w.WriteString(newPrefix + d.GlyphLast)
		} else {
			w.WriteString(newPrefix + d.GlyphMid)
		}
		d.writeProduction(w, &prod)
	}
}

func (d *ContextaDebugger[TTokenID]) writeProduction(w *strings.Builder, prod *Production[TTokenID]) {
	f := d.Formatter
	var parts []string
	for _, sym := range prod.Symbols {
		switch sym.Type {
		case SYMBOL_TERMINAL:
			s := f.FormatToken(sym.Token)
			if f.ColorToken != nil {
				s = f.ColorToken(s)
			}
			parts = append(parts, s)
		case SYMBOL_NON_TERMINAL:
			s := sym.Name
			if f.FormatRuleName != nil {
				s = f.FormatRuleName(s)
			}
			if f.ColorNonTerm != nil {
				s = f.ColorNonTerm(s)
			}
			parts = append(parts, s)
		case SYMBOL_EPSILON:
			s := f.FormatSymbolType(SYMBOL_EPSILON)
			if f.ColorEpsilon != nil {
				s = f.ColorEpsilon(s)
			}
			parts = append(parts, s)
		default:
			parts = append(parts, f.FormatSymbolType(sym.Type))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, f.FormatSymbolType(SYMBOL_EPSILON))
	}
	w.WriteString(strings.Join(parts, " "))
	w.WriteByte('\n')
}

/*
DebugDump produces a human-readable dump of the grammar using the given formatter.

Convenience wrapper around NewContextaDebugger and DumpString.
*/
func (g *Grammar[TTokenID]) DebugDump(f ContextaDebugFormatter[TTokenID]) string {
	return NewContextaDebugger(f).DumpString(g)
}
