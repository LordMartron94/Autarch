package pattern

/*
RegulaVisitor defines a configurable contract for external clients to read the AST.
By using a struct of function pointers instead of an interface, clients can provide
partial implementations. Unassigned functions are safely ignored during traversal.
*/
type RegulaVisitor[TObservation any] struct {
	VisitLiteral func(values []TObservation)
	VisitClass   func(ranges []CharRange[TObservation], negatedFrom []CharRange[TObservation])
	VisitConcat  func(left, right *RegulaAST[TObservation])
	VisitUnion   func(left, right *RegulaAST[TObservation])
	VisitRepeat  func(sub *RegulaAST[TObservation], min, max int)
	VisitCapture func(sub *RegulaAST[TObservation])
}

/*
Accept allows an external visitor to process the internal AST node.
It provides a controlled boundary for clients to extract data without
exposing the unexported fields of the RegulaAST struct.
*/
func (r *RegulaAST[TObservation]) Accept(v RegulaVisitor[TObservation]) {
	if r == nil {
		return
	}
	r.routeToVisitor(v)
}

/*
routeToVisitor directs the AST node's data to the appropriate visitor callback.
It guarantees safety by verifying the callback exists before execution.
*/
func (r *RegulaAST[TObservation]) routeToVisitor(v RegulaVisitor[TObservation]) {
	switch r.kind {
	case EXPRESSION_LITERAL:
		if v.VisitLiteral != nil {
			v.VisitLiteral(r.literals)
		}
	case EXPRESSION_CLASS:
		if v.VisitClass != nil {
			v.VisitClass(r.class.ranges, r.class.negatedFrom)
		}
	case EXPRESSION_CONCAT:
		if v.VisitConcat != nil {
			v.VisitConcat(r.left, r.right)
		}
	case EXPRESSION_UNION:
		if v.VisitUnion != nil {
			v.VisitUnion(r.left, r.right)
		}
	case EXPRESSION_REPEAT:
		if v.VisitRepeat != nil {
			v.VisitRepeat(r.sub, r.min, r.max)
		}
	case EXPRESSION_CAPTURE:
		if v.VisitCapture != nil {
			v.VisitCapture(r.sub)
		}
	default:
		panic("unknown expression kind in visitor routing")
	}
}
