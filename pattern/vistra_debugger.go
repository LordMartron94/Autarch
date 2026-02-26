package pattern

import (
	"fmt"
	"strings"
)

// ============================================================
// FORMATTER (Semantic Layer)
// ============================================================

/*
VistraDebugFormatter configures how VistraAST nodes are rendered in debug dumps.

Required: FormatKind and FormatObservation. Optional hooks for literals, class, repeat,
nest call/return, logical/annotation IDs, and optional color wrappers for terminal output.
*/
type VistraDebugFormatter[TObservation any] struct {
	/* REQUIRED */
	FormatKind        func(VistraKind) string
	FormatObservation func(TObservation) string
	FormatRepeat      func(min int, max *int) string

	/* Optional: literals (atom). If nil, falls back to FormatObservation join. */
	FormatLiteral func([]TObservation) string

	/* Optional: one range. If nil, falls back to "lo..hi". */
	FormatClassRange func(lo, hi TObservation) string

	/* Optional: full class. If nil, falls back to formatted ranges. */
	FormatClass func(ranges []charRange[TObservation]) string

	/* Optional: nest call/return symbol lists. If nil, fall back to FormatObservation join. */
	FormatNestCall   func([]TObservation) string
	FormatNestReturn func([]TObservation) string

	/* Optional: binding/metadata */
	FormatLogicalID    func(logicalID) string
	FormatAnnotationID func(AnnotationID) string

	/* Optional: coloring (nil = no color) */
	ColorKind      func(string) string
	ColorPayload   func(string) string
	ColorLogicalID func(string) string
	ColorMeta      func(string) string
}

func (f VistraDebugFormatter[TObservation]) validate() {
	if f.FormatKind == nil {
		panic("VistraDebugFormatter: FormatKind is required")
	}
	if f.FormatObservation == nil {
		panic("VistraDebugFormatter: FormatObservation is required")
	}
	if f.FormatRepeat == nil {
		panic("VistraDebugFormatter: FormatRepeat is required")
	}
}

/*
NewVistraCleanFormatter returns a default formatter for quick scanning of Vistra trees.

Uses short kind names (ATOM, CLS, SEQ, ALT, REP, NEST, EMPTY) and the given observation formatter.
Repeat uses *, +, ?, {n}, {n,m}, {n,∞} style. Annotation IDs render as ann:N.
*/
func NewVistraCleanFormatter[T any](obsFormatter func(T) string) VistraDebugFormatter[T] {
	return VistraDebugFormatter[T]{
		FormatKind: func(k VistraKind) string {
			switch k {
			case VistraAtom:
				return "ATOM"
			case VistraClass:
				return "CLS"
			case VistraConcat:
				return "SEQ"
			case VistraUnion:
				return "ALT"
			case VistraRepeat:
				return "REP"
			case VistraNest:
				return "NEST"
			case VistraEmpty:
				return "EMPTY"
			default:
				return "???"
			}
		},
		FormatObservation: obsFormatter,
		FormatRepeat: func(min int, max *int) string {
			if max == nil {
				if min == 0 {
					return "*"
				}
				if min == 1 {
					return "+"
				}
				return fmt.Sprintf("{%d,∞}", min)
			}
			if min == 0 && *max == 1 {
				return "?"
			}
			if min == *max {
				return fmt.Sprintf("{%d}", min)
			}
			return fmt.Sprintf("{%d,%d}", min, *max)
		},
		FormatAnnotationID: func(id AnnotationID) string {
			return fmt.Sprintf("ann:%d", id)
		},
	}
}

// ============================================================
// ENUMERATION (Structural Layer)
// ============================================================

type vistraDebugEdge[TObservation any] struct {
	label string
	node  *VistraAST[TObservation]
}

/*
VistraEdgeEnumerator returns the child edges of a VistraAST node for tree walking.

Used by VistraDebugger to render the tree structure. NewVistraDebugger uses a default enumerator that follows left/right, sub, and body pointers.
*/
type VistraEdgeEnumerator[TObservation any] interface {
	EdgesOf(node *VistraAST[TObservation]) []vistraDebugEdge[TObservation]
}

type defaultVistraEdgeEnumerator[TObservation any] struct{}

func (e defaultVistraEdgeEnumerator[TObservation]) EdgesOf(
	v *VistraAST[TObservation],
) []vistraDebugEdge[TObservation] {
	if v == nil {
		return nil
	}
	switch v.kind {
	case VistraConcat, VistraUnion:
		d, _ := v.data.(vistraBinaryData[TObservation])
		var out []vistraDebugEdge[TObservation]
		if d.left != nil {
			out = append(out, vistraDebugEdge[TObservation]{label: "l", node: d.left})
		}
		if d.right != nil {
			out = append(out, vistraDebugEdge[TObservation]{label: "r", node: d.right})
		}
		return out
	case VistraRepeat:
		d, _ := v.data.(vistraRepeatData[TObservation])
		if d.sub == nil {
			return nil
		}
		return []vistraDebugEdge[TObservation]{
			{label: "sub", node: d.sub},
		}
	case VistraNest:
		d, _ := v.data.(vistraNestData[TObservation])
		if d.body == nil {
			return nil
		}
		return []vistraDebugEdge[TObservation]{
			{label: "body", node: d.body},
		}
	default:
		return nil
	}
}

// ============================================================
// RENDERER (Layout + IO)
// ============================================================

/*
VistraDebugger dumps a VistraAST tree to a human-readable string with optional coloring and alignment.

Use NewVistraDebugger to construct. DumpString(root) produces a tree with kind, payload, and metadata per node.
*/
type VistraDebugger[TObservation any] struct {
	Formatter   VistraDebugFormatter[TObservation]
	Enumerator  VistraEdgeEnumerator[TObservation]
	GutterWidth int

	GlyphMid   string
	GlyphLast  string
	GlyphVert  string
	GlyphBlank string
}

/*
NewVistraDebugger constructs a Vistra debugger with the given formatter.

Formatter is validated (FormatKind, FormatObservation, FormatRepeat required). A default enumerator is used for tree structure. Gutter and glyphs match Regula debugger defaults.
*/
func NewVistraDebugger[TObservation any](
	formatter VistraDebugFormatter[TObservation],
) *VistraDebugger[TObservation] {
	formatter.validate()
	return &VistraDebugger[TObservation]{
		Formatter:   formatter,
		Enumerator:  defaultVistraEdgeEnumerator[TObservation]{},
		GutterWidth: 50,
		GlyphMid:    "├─ ",
		GlyphLast:   "└─ ",
		GlyphVert:   "│  ",
		GlyphBlank:  "   ",
	}
}

/*
DumpString renders the tree rooted at root as a single string.

Each node appears on one line with optional metadata aligned in the gutter. Children are indented with tree glyphs.
*/
func (d *VistraDebugger[TObservation]) DumpString(root *VistraAST[TObservation]) string {
	var b strings.Builder
	_ = d.walk(root, &b, "", true, 0)
	return b.String()
}

func (d *VistraDebugger[TObservation]) walk(
	n *VistraAST[TObservation],
	w *strings.Builder,
	prefix string,
	isLast bool,
	depth int,
) error {
	if n == nil {
		return nil
	}
	if depth > 0 {
		if isLast {
			w.WriteString(prefix + d.GlyphLast)
		} else {
			w.WriteString(prefix + d.GlyphMid)
		}
	}

	d.writeNodeLine(w, n, depth)

	newPrefix := prefix
	if depth > 0 {
		if isLast {
			newPrefix += d.GlyphBlank
		} else {
			newPrefix += d.GlyphVert
		}
	}

	edges := d.Enumerator.EdgesOf(n)
	for i, e := range edges {
		d.walk(e.node, w, newPrefix, i == len(edges)-1, depth+1)
	}
	return nil
}

func (d *VistraDebugger[TObservation]) writeNodeLine(w *strings.Builder, v *VistraAST[TObservation], depth int) {
	f := d.Formatter
	startPos := w.Len()

	kindStr := f.FormatKind(v.kind)
	if f.ColorKind != nil {
		kindStr = f.ColorKind(kindStr)
	}
	w.WriteString(kindStr)
	w.WriteByte(' ')

	payload := d.getPayload(v)
	if payload != "" {
		if f.ColorPayload != nil {
			payload = f.ColorPayload(payload)
		}
		w.WriteString(payload)
	}

	meta := d.getMeta(v)
	if meta != "" {
		currentLineLen := d.visibleLen(w.String()[startPos:])
		totalOffset := currentLineLen + (depth * 3)

		padding := d.GutterWidth - totalOffset
		if padding < 2 {
			padding = 2
		}
		w.WriteString(strings.Repeat(" ", padding))

		if f.ColorMeta != nil {
			meta = f.ColorMeta(meta)
		}
		w.WriteString(meta)
	}
	w.WriteByte('\n')
}

func (d *VistraDebugger[TObservation]) getPayload(v *VistraAST[TObservation]) string {
	f := d.Formatter
	switch v.kind {
	case VistraAtom:
		d, _ := v.data.(vistraAtomData[TObservation])
		if f.FormatLiteral != nil {
			return f.FormatLiteral(d.rawSymbols)
		}
		if len(d.rawSymbols) == 0 {
			return "ε"
		}
		var res []string
		for _, o := range d.rawSymbols {
			res = append(res, f.FormatObservation(o))
		}
		return "(" + strings.Join(res, " ") + ")"
	case VistraClass:
		d, _ := v.data.(vistraClassData[TObservation])
		if f.FormatClass != nil {
			return f.FormatClass(d.rawClass.ranges)
		}
		var res []string
		for _, r := range d.rawClass.ranges {
			if f.FormatClassRange != nil {
				res = append(res, f.FormatClassRange(r.lo, r.hi))
			} else {
				res = append(res, fmt.Sprintf("%v..%v", f.FormatObservation(r.lo), f.FormatObservation(r.hi)))
			}
		}
		return "[" + strings.Join(res, ", ") + "]"
	case VistraRepeat:
		d, _ := v.data.(vistraRepeatData[TObservation])
		var maxPtr *int
		if d.max >= 0 {
			maxPtr = &d.max
		}
		return f.FormatRepeat(d.min, maxPtr)
	case VistraNest:
		d, _ := v.data.(vistraNestData[TObservation])
		callStr := formatNestSymbols(d.rawCallSymbols, f.FormatNestCall, f.FormatObservation)
		retStr := formatNestSymbols(d.rawReturnSymbols, f.FormatNestReturn, f.FormatObservation)
		return "call:" + callStr + " return:" + retStr
	case VistraEmpty:
		return "ε"
	}
	return ""
}

func formatNestSymbols[TObservation any](
	symbols []TObservation,
	hook func([]TObservation) string,
	formatObs func(TObservation) string,
) string {
	if hook != nil {
		return hook(symbols)
	}
	if len(symbols) == 0 {
		return "[]"
	}
	var res []string
	for _, o := range symbols {
		res = append(res, formatObs(o))
	}
	return "[" + strings.Join(res, " ") + "]"
}

func (d *VistraDebugger[TObservation]) getMeta(v *VistraAST[TObservation]) string {
	f := d.Formatter
	var parts []string

	if f.FormatLogicalID != nil {
		switch v.kind {
		case VistraAtom:
			if data, ok := v.data.(vistraAtomData[TObservation]); ok && len(data.symbolIDs) > 0 {
				var ids []string
				for _, id := range data.symbolIDs {
					ids = append(ids, f.FormatLogicalID(id))
				}
				parts = append(parts, "sym:["+strings.Join(ids, ",")+"]")
			}
		case VistraClass:
			if data, ok := v.data.(vistraClassData[TObservation]); ok && data.classSymID != 0 {
				parts = append(parts, "sym:"+f.FormatLogicalID(data.classSymID))
			}
		case VistraNest:
			if data, ok := v.data.(vistraNestData[TObservation]); ok {
				if len(data.callSymbolIDs) > 0 {
					var ids []string
					for _, id := range data.callSymbolIDs {
						ids = append(ids, f.FormatLogicalID(id))
					}
					parts = append(parts, "callSym:["+strings.Join(ids, ",")+"]")
				}
				if len(data.returnSymbolIDs) > 0 {
					var ids []string
					for _, id := range data.returnSymbolIDs {
						ids = append(ids, f.FormatLogicalID(id))
					}
					parts = append(parts, "retSym:["+strings.Join(ids, ",")+"]")
				}
			}
		}
	}

	if v.annotationID != nil {
		val := *v.annotationID
		if f.FormatAnnotationID != nil {
			parts = append(parts, f.FormatAnnotationID(val))
		} else {
			parts = append(parts, fmt.Sprintf("ann:%d", val))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return "«" + strings.Join(parts, " ") + "»"
}

func (d *VistraDebugger[TObservation]) visibleLen(s string) int {
	count := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		count++
	}
	return count
}

/*
DebugDump renders the VistraAST tree with the given formatter.

Convenience for one-off dumps: NewVistraDebugger(f).DumpString(v).
*/
func (v *VistraAST[TObservation]) DebugDump(f VistraDebugFormatter[TObservation]) string {
	return NewVistraDebugger(f).DumpString(v)
}
