package pattern

/*
vistraNormalizeAST normalizes a Vistra AST for compilation: flattens trivial concats,
removes empty from Concat/Union where possible, and ensures Nest body is present.
Repeat nodes are already in (min, max) form from the factory. Returns the root of the
normalized tree (may be the same node or a new structure). Mutates the tree in place
where possible; call before binding if you need to preserve the original AST.
*/
func vistraNormalizeAST[TObs any](n *VistraAST[TObs]) *VistraAST[TObs] {
	if n == nil {
		return nil
	}

	switch n.kind {
	case VistraAtom, VistraClass, VistraEmpty:
		return n

	case VistraConcat:
		return vistraNormalizeConcat(n)

	case VistraUnion:
		return vistraNormalizeUnion(n)

	case VistraRepeat:
		d := n.data.(vistraRepeatData[TObs])
		sub := vistraNormalizeAST(d.sub)
		if sub == nil {
			return n
		}
		n.data = vistraRepeatData[TObs]{sub: sub, min: d.min, max: d.max}
		return n

	case VistraNest:
		d := n.data.(vistraNestData[TObs])
		if d.body == nil {
			return n
		}
		body := vistraNormalizeAST(d.body)
		n.data = vistraNestData[TObs]{
			rawCallSymbols:   d.rawCallSymbols,
			rawReturnSymbols: d.rawReturnSymbols,
			callSymbolIDs:    d.callSymbolIDs,
			returnSymbolIDs:  d.returnSymbolIDs,
			body:             body,
		}
		return n

	default:
		return n
	}
}

func vistraNormalizeConcat[TObs any](n *VistraAST[TObs]) *VistraAST[TObs] {
	seq := vistraFlattenConcat(n)
	nonEmpty := make([]*VistraAST[TObs], 0, len(seq))
	for _, p := range seq {
		norm := vistraNormalizeAST(p)
		if norm != nil && norm.kind != VistraEmpty {
			nonEmpty = append(nonEmpty, norm)
		}
	}
	if len(nonEmpty) == 0 {
		return &VistraAST[TObs]{kind: VistraEmpty}
	}
	if len(nonEmpty) == 1 {
		return nonEmpty[0]
	}
	return vistraBuildConcatChain(nonEmpty, n.annotationID)
}

func vistraFlattenConcat[TObs any](n *VistraAST[TObs]) []*VistraAST[TObs] {
	if n == nil || n.kind != VistraConcat {
		return []*VistraAST[TObs]{n}
	}
	d := n.data.(vistraBinaryData[TObs])
	var out []*VistraAST[TObs]
	out = append(out, vistraFlattenConcat(d.left)...)
	out = append(out, vistraFlattenConcat(d.right)...)
	return out
}

func vistraBuildConcatChain[TObs any](nodes []*VistraAST[TObs], ann *AnnotationID) *VistraAST[TObs] {
	if len(nodes) == 0 {
		return &VistraAST[TObs]{kind: VistraEmpty}
	}
	if len(nodes) == 1 {
		nodes[0].annotationID = ann
		return nodes[0]
	}
	mid := len(nodes) / 2
	left := vistraBuildConcatChain(nodes[:mid], nil)
	right := vistraBuildConcatChain(nodes[mid:], nil)
	return &VistraAST[TObs]{
		kind:         VistraConcat,
		data:         vistraBinaryData[TObs]{left: left, right: right},
		annotationID: ann,
	}
}

func vistraNormalizeUnion[TObs any](n *VistraAST[TObs]) *VistraAST[TObs] {
	d := n.data.(vistraBinaryData[TObs])
	left := vistraNormalizeAST(d.left)
	right := vistraNormalizeAST(d.right)
	if left != nil && left.kind == VistraEmpty {
		return right
	}
	if right != nil && right.kind == VistraEmpty {
		return left
	}
	if left == nil && right == nil {
		return &VistraAST[TObs]{kind: VistraEmpty}
	}
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	n.data = vistraBinaryData[TObs]{left: left, right: right}
	return n
}
