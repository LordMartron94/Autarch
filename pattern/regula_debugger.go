package pattern

import (
	"fmt"
	"strings"
)

// ============================================================
// FORMATTER (Semantic Layer)
// ============================================================

type RegulaDebugFormatter[TObservation any] struct {
	/* REQUIRED */
	FormatKind func(ExpressionKind) string

	/* Observation render hooks */
	FormatObservation func(TObservation) string
	FormatRepeat      func(min int, max *int) string

	// FormatLiteral renders g.literals. If nil, falls back to FormatObservation join.
	FormatLiteral func([]TObservation) string

	// FormatClassRange renders one range. If nil, falls back to "lo..hi".
	FormatClassRange func(lo, hi TObservation) string

	// FormatClass renders the full class.
	FormatClass func(ranges []CharRange[TObservation]) string

	/* Binding metadata hooks (optional) */
	FormatLogicalID  func(logicalID) string
	FormatPositionID func(positionID) string

	// FormatAnnotationID allows custom rendering of the annotation (e.g., "ann:RuleName")
	FormatAnnotationID func(AnnotationID) string

	/* Coloring layer (nil = no color) */
	ColorKind       func(string) string
	ColorPayload    func(string) string
	ColorLogicalID  func(string) string
	ColorPositionID func(string) string
	ColorMeta       func(string) string
}

func (f RegulaDebugFormatter[TObservation]) validate() {
	if f.FormatKind == nil {
		panic("RegulaDebugFormatter: FormatKind is required")
	}
}

// NewCleanFormatter provides a sensible default for scanning patterns quickly.
func NewCleanFormatter[T any](obsFormatter func(T) string) RegulaDebugFormatter[T] {
	return RegulaDebugFormatter[T]{
		FormatKind: func(k ExpressionKind) string {
			switch k {
			case EXPRESSION_LITERAL:
				return "LIT"
			case EXPRESSION_CLASS:
				return "CLS"
			case EXPRESSION_CONCAT:
				return "SEQ"
			case EXPRESSION_UNION:
				return "ALT"
			case EXPRESSION_REPEAT:
				return "REP"
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

type regulaDebugEdge[TObservation any] struct {
	label string
	node  *RegulaAST[TObservation]
}

type RegulaEdgeEnumerator[TObservation any] interface {
	EdgesOf(node *RegulaAST[TObservation]) []regulaDebugEdge[TObservation]
}

type defaultRegulaEdgeEnumerator[TObservation any] struct{}

func (e defaultRegulaEdgeEnumerator[TObservation]) EdgesOf(
	g *RegulaAST[TObservation],
) []regulaDebugEdge[TObservation] {
	if g == nil {
		return nil
	}
	switch g.kind {
	case EXPRESSION_CONCAT, EXPRESSION_UNION:
		var out []regulaDebugEdge[TObservation]
		if g.left != nil {
			out = append(out, regulaDebugEdge[TObservation]{label: "l", node: g.left})
		}
		if g.right != nil {
			out = append(out, regulaDebugEdge[TObservation]{label: "r", node: g.right})
		}
		return out
	case EXPRESSION_REPEAT:
		if g.sub == nil {
			return nil
		}
		return []regulaDebugEdge[TObservation]{
			{label: "sub", node: g.sub},
		}
	default:
		return nil
	}
}

// ============================================================
// RENDERER (Layout + IO)
// ============================================================

type RegulaDebugger[TObservation any] struct {
	Formatter  RegulaDebugFormatter[TObservation]
	Enumerator RegulaEdgeEnumerator[TObservation]

	GutterWidth int

	GlyphMid   string
	GlyphLast  string
	GlyphVert  string
	GlyphBlank string
}

func NewRegulaDebugger[TObservation any](
	formatter RegulaDebugFormatter[TObservation],
) *RegulaDebugger[TObservation] {
	formatter.validate()
	return &RegulaDebugger[TObservation]{
		Formatter:   formatter,
		Enumerator:  defaultRegulaEdgeEnumerator[TObservation]{},
		GutterWidth: 50,
		GlyphMid:    "├─ ",
		GlyphLast:   "└─ ",
		GlyphVert:   "│  ",
		GlyphBlank:  "   ",
	}
}

func (d *RegulaDebugger[TObservation]) DumpString(root *RegulaAST[TObservation]) string {
	var b strings.Builder
	_ = d.walk(root, &b, "", true, 0)
	return b.String()
}

func (d *RegulaDebugger[TObservation]) walk(
	n *RegulaAST[TObservation],
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

func (d *RegulaDebugger[TObservation]) writeNodeLine(w *strings.Builder, g *RegulaAST[TObservation], depth int) {
	f := d.Formatter
	startPos := w.Len()

	kindStr := f.FormatKind(g.kind)
	if f.ColorKind != nil {
		kindStr = f.ColorKind(kindStr)
	}
	w.WriteString(kindStr)
	w.WriteByte(' ')

	payload := d.getPayload(g)
	if payload != "" {
		if f.ColorPayload != nil {
			payload = f.ColorPayload(payload)
		}
		w.WriteString(payload)
	}

	meta := d.getMeta(g)
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

func (d *RegulaDebugger[TObservation]) getPayload(g *RegulaAST[TObservation]) string {
	f := d.Formatter
	switch g.kind {
	case EXPRESSION_LITERAL:
		if f.FormatLiteral != nil {
			return f.FormatLiteral(g.literals)
		}
		if len(g.literals) == 0 {
			return "ε"
		}
		var res []string
		for _, v := range g.literals {
			res = append(res, f.FormatObservation(v))
		}
		return "(" + strings.Join(res, " ") + ")"
	case EXPRESSION_CLASS:
		if f.FormatClass != nil {
			return f.FormatClass(g.class.ranges)
		}
		var res []string
		for _, r := range g.class.ranges {
			res = append(res, fmt.Sprintf("%s..%s", f.FormatObservation(r.Lo), f.FormatObservation(r.Hi)))
		}
		return "[" + strings.Join(res, ", ") + "]"
	case EXPRESSION_REPEAT:
		maxVal := g.max
		var maxPtr *int
		if maxVal != -1 {
			maxPtr = &maxVal
		}
		return f.FormatRepeat(g.min, maxPtr)
	}
	return ""
}

func (d *RegulaDebugger[TObservation]) getMeta(g *RegulaAST[TObservation]) string {
	f := d.Formatter
	var parts []string

	// Position IDs
	if f.FormatPositionID != nil {
		if g.kind == EXPRESSION_CLASS && g.classPosID != 0 {
			parts = append(parts, "p:"+f.FormatPositionID(g.classPosID))
		} else if len(g.literalPosIDs) > 0 {
			var ids []string
			for _, pid := range g.literalPosIDs {
				ids = append(ids, f.FormatPositionID(pid))
			}
			parts = append(parts, "p:["+strings.Join(ids, ",")+"]")
		}
	}

	// Binding status
	if g.literalBound || g.classBound {
		parts = append(parts, "bound")
	}

	// Annotation handling (Dereference the pointer)
	if g.annotationID != nil {
		val := *g.annotationID
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

func (d *RegulaDebugger[TObservation]) visibleLen(s string) int {
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

func (g *RegulaAST[TObservation]) DebugDump(f RegulaDebugFormatter[TObservation]) string {
	return NewRegulaDebugger(f).DumpString(g)
}
