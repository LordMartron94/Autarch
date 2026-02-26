package pattern

import (
	"autarch"
	"fmt"
	"memarch"
	"slices"
)

/*
VistraCompileToDPDA compiles multiple Vistra patterns into DPDAs using a shared alphabet and symbol space.

All patterns are prepared (symbol collection, alphabet build, bind IDs), then each instruction is
normalized, compiled to a control graph, validated for determinism, and turned into a DPDA.
Returns one DPDA per instruction. Stack alphabet is BOS plus one symbol per Nest. Acceptance is
state-based; clients should also require stack depth 1 (only BOS) for full acceptance.
*/
func VistraCompileToDPDA[TObservation, TStackSymbol, TStateOutcome any](
	alloc memarch.AllocationFn,
	instructions []PatternCompilationInstruction[TObservation, TStateOutcome, VistraAST[TObservation]],
	ctx *SharedCompilationContext[TObservation, VistraAST[TObservation]],
	nonTerminalOutcome TStateOutcome,
) ([]*autarch.DPDA[TObservation, TStackSymbol, AnnotatedOutcome[TStateOutcome]], error) {
	if len(instructions) == 0 {
		return nil, fmt.Errorf("vistra dpda: instructions list must be non-empty")
	}

	patterns := make([]*VistraAST[TObservation], len(instructions))
	for i := range instructions {
		patterns[i] = instructions[i].Pattern
	}

	ctx.fullPrepare(
		patterns,
		vistraCollectSymbols[TObservation],
		func(p *VistraAST[TObservation], feeder symbolFeeder[TObservation], bindState interface{}) {
			vistraBindIDs(p, feeder)
		},
		nil,
	)

	inputSize := uint64(len(ctx.alphabet))
	out := make([]*autarch.DPDA[TObservation, TStackSymbol, AnnotatedOutcome[TStateOutcome]], 0, len(instructions))

	for i, instruction := range instructions {
		norm := vistraNormalizeAST(instruction.Pattern)
		comp := vistraDPDACompilerCreate[TObservation, TStateOutcome](ctx.expander, inputSize)
		frag := comp.compile(norm, vistraBOSStackID, nil)
		if comp.err != nil {
			return nil, fmt.Errorf("vistra dpda instruction %d: %w", i, comp.err)
		}
		comp.transitions = frag.edges

		if err := vistraValidateDeterminism(comp.transitions); err != nil {
			return nil, fmt.Errorf("vistra dpda instruction %d: %w", i, err)
		}

		allStates := vistraCollectStateIDs(comp.transitions, frag.entry, frag.exit)
		stateMap := vistraNumberStates(allStates, frag.entry)
		numStates := uint64(len(allStates))

		transitions := vistraEmitDPDATransitions[TObservation, TStackSymbol](comp.transitions, stateMap)
		stackAlphabet := vistraBuildStackAlphabet[TStackSymbol](comp.nextStackSymbolID)

		outcomes := make([]AnnotatedOutcome[TStateOutcome], numStates)
		for s := uint64(0); s < numStates; s++ {
			outcomes[s] = AnnotatedOutcome[TStateOutcome]{Value: nonTerminalOutcome}
		}
		for absState, annID := range comp.annotations {
			if mapped, ok := stateMap[absState]; ok {
				outcomes[mapped] = AnnotatedOutcome[TStateOutcome]{
					Value:      instruction.Outcome,
					Annotation: &annID,
				}
			}
		}
		exitMapped := stateMap[frag.exit]
		if _, hasAnn := comp.annotations[frag.exit]; !hasAnn {
			outcomes[exitMapped] = AnnotatedOutcome[TStateOutcome]{Value: instruction.Outcome}
		}

		dpda, err := autarch.DPDACreate(
			alloc,
			ctx.alphabet,
			stackAlphabet,
			vistraBOSStackID,
			transitions,
			outcomes,
			ctx.indexer,
		)
		if err != nil {
			return nil, fmt.Errorf("vistra dpda instruction %d: %w", i, err)
		}
		out = append(out, dpda)
	}

	return out, nil
}

const vistraBOSStackID = uint64(0)

type dpdaFragment struct {
	entry uint64
	exit  uint64
	edges []abstractTransition
}

type abstractTransition struct {
	fromState       uint64
	toState         uint64
	inputSymbolID   uint64
	stackTopID      uint64
	opKind          autarch.StackOperationKind
	pushPopSymbolID uint64
}

type vistraDPDACompiler[TObs any, TOutcome any] struct {
	nextState         uint64
	transitions       []abstractTransition
	expander          symbolExpander
	nextStackSymbolID uint64
	inputSize         uint64
	annotations       map[uint64]AnnotationID
	err               error
}

func vistraDPDACompilerCreate[TObs any, TOutcome any](
	expander symbolExpander,
	inputSize uint64,
) *vistraDPDACompiler[TObs, TOutcome] {
	return &vistraDPDACompiler[TObs, TOutcome]{
		expander:          expander,
		inputSize:         inputSize,
		nextStackSymbolID: 1,
		annotations:       make(map[uint64]AnnotationID),
	}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) newState() uint64 {
	s := c.nextState
	c.nextState++
	return s
}

func (c *vistraDPDACompiler[TObs, TOutcome]) newFrag() dpdaFragment {
	return dpdaFragment{entry: c.newState(), exit: c.newState(), edges: nil}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) emitInto(edges *[]abstractTransition, from, to uint64, inputID, stackTopID uint64, opKind autarch.StackOperationKind, pushPopID uint64) {
	*edges = append(*edges, abstractTransition{
		fromState:       from,
		toState:         to,
		inputSymbolID:   inputID,
		stackTopID:      stackTopID,
		opKind:          opKind,
		pushPopSymbolID: pushPopID,
	})
}

func (c *vistraDPDACompiler[TObs, TOutcome]) emitLogicalInto(edges *[]abstractTransition, from, to uint64, lid logicalID, stackTopID uint64, opKind autarch.StackOperationKind, pushPopID uint64) {
	phys := c.expander.expand(lid)
	for _, pid := range phys {
		c.emitInto(edges, from, to, uint64(pid), stackTopID, opKind, pushPopID)
	}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compile(n *VistraAST[TObs], stackTopID uint64, reuseEntry *uint64) dpdaFragment {
	if n == nil {
		var entry uint64
		if reuseEntry != nil {
			entry = *reuseEntry
		} else {
			entry = c.newState()
		}
		return dpdaFragment{entry: entry, exit: entry, edges: nil}
	}

	switch n.kind {
	case VistraEmpty:
		var entry uint64
		if reuseEntry != nil {
			entry = *reuseEntry
		} else {
			entry = c.newState()
		}
		return dpdaFragment{entry: entry, exit: entry, edges: nil}

	case VistraAtom:
		return c.compileAtom(n, stackTopID, reuseEntry)

	case VistraClass:
		return c.compileClass(n, stackTopID, reuseEntry)

	case VistraConcat:
		return c.compileConcat(n, stackTopID)

	case VistraUnion:
		return c.compileUnion(n, stackTopID)

	case VistraRepeat:
		return c.compileRepeat(n, stackTopID)

	case VistraNest:
		return c.compileNest(n, stackTopID, reuseEntry)

	default:
		panic(fmt.Sprintf("vistra compile: unknown kind %v", n.kind))
	}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileAtom(n *VistraAST[TObs], stackTopID uint64, reuseEntry *uint64) dpdaFragment {
	d := n.data.(vistraAtomData[TObs])
	edges := make([]abstractTransition, 0)
	var start uint64
	if reuseEntry != nil {
		start = *reuseEntry
	} else {
		start = c.newState()
	}
	if len(d.symbolIDs) == 0 {
		if n.annotationID != nil {
			c.annotations[start] = *n.annotationID
		}
		return dpdaFragment{entry: start, exit: start, edges: edges}
	}
	cur := start
	for _, lid := range d.symbolIDs {
		next := c.newState()
		c.emitLogicalInto(&edges, cur, next, lid, stackTopID, autarch.NoOp, 0)
		cur = next
	}
	if n.annotationID != nil {
		c.annotations[cur] = *n.annotationID
	}
	return dpdaFragment{entry: start, exit: cur, edges: edges}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileClass(n *VistraAST[TObs], stackTopID uint64, reuseEntry *uint64) dpdaFragment {
	d := n.data.(vistraClassData[TObs])
	f := c.newFrag()
	f.edges = make([]abstractTransition, 0)
	if reuseEntry != nil {
		f.entry = *reuseEntry
	}
	c.emitLogicalInto(&f.edges, f.entry, f.exit, d.classSymID, stackTopID, autarch.NoOp, 0)
	if n.annotationID != nil {
		c.annotations[f.exit] = *n.annotationID
	}
	return f
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileConcat(n *VistraAST[TObs], stackTopID uint64) dpdaFragment {
	d := n.data.(vistraBinaryData[TObs])
	left := c.compile(d.left, stackTopID, nil)
	right := c.compile(d.right, stackTopID, &left.exit)
	combined := make([]abstractTransition, 0, len(left.edges)+len(right.edges))
	combined = append(combined, left.edges...)
	combined = append(combined, right.edges...)
	if n.annotationID != nil {
		c.annotations[right.exit] = *n.annotationID
	}
	return dpdaFragment{entry: left.entry, exit: right.exit, edges: combined}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileUnion(n *VistraAST[TObs], stackTopID uint64) dpdaFragment {
	d := n.data.(vistraBinaryData[TObs])
	left := c.compile(d.left, stackTopID, nil)
	if c.err != nil {
		return dpdaFragment{}
	}
	right := c.compile(d.right, stackTopID, nil)
	if c.err != nil {
		return dpdaFragment{}
	}

	leftFirst := vistraEdgesFrom(left.edges, left.entry)
	rightFirst := vistraEdgesFrom(right.edges, right.entry)
	if vistraTransitionSetsOverlap(leftFirst, rightFirst) {
		c.err = fmt.Errorf("union branches overlap on (input, stackTop); determinism violated")
		return dpdaFragment{}
	}

	mergeExit := c.newState()
	unionEntry := c.newState()

	combined := make([]abstractTransition, 0, len(left.edges)+len(right.edges)+len(leftFirst)+len(rightFirst))
	for _, t := range left.edges {
		if t.fromState != left.entry {
			combined = append(combined, t)
		}
	}
	for _, t := range right.edges {
		if t.fromState != right.entry {
			combined = append(combined, t)
		}
	}
	for _, t := range leftFirst {
		c.emitInto(&combined, unionEntry, t.toState, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID)
	}
	for _, t := range rightFirst {
		c.emitInto(&combined, unionEntry, t.toState, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID)
	}
	for i := range combined {
		if combined[i].toState == left.exit {
			combined[i].toState = mergeExit
		}
		if combined[i].toState == right.exit {
			combined[i].toState = mergeExit
		}
	}

	if n.annotationID != nil {
		c.annotations[mergeExit] = *n.annotationID
	}
	return dpdaFragment{entry: unionEntry, exit: mergeExit, edges: combined}
}

func vistraEdgesFrom(edges []abstractTransition, from uint64) []abstractTransition {
	var out []abstractTransition
	for _, t := range edges {
		if t.fromState == from {
			out = append(out, t)
		}
	}
	return out
}

func vistraTransitionsFrom(transitions []abstractTransition, from uint64) []abstractTransition {
	var out []abstractTransition
	for _, t := range transitions {
		if t.fromState == from {
			out = append(out, t)
		}
	}
	return out
}

func vistraTransitionSetsOverlap(a, b []abstractTransition) bool {
	type key struct{ input, stack uint64 }
	seen := make(map[key]struct{})
	for _, t := range a {
		seen[key{t.inputSymbolID, t.stackTopID}] = struct{}{}
	}
	for _, t := range b {
		if _, ok := seen[key{t.inputSymbolID, t.stackTopID}]; ok {
			return true
		}
	}
	return false
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileRepeat(n *VistraAST[TObs], stackTopID uint64) dpdaFragment {
	d := n.data.(vistraRepeatData[TObs])
	min, max := d.min, d.max

	if min == 0 && max == -1 {
		return c.compileRepeatStar(d.sub, stackTopID)
	}
	if min == 1 && max == -1 {
		return c.compileRepeatPlus(d.sub, stackTopID)
	}
	if min == 0 && max == 1 {
		return c.compileRepeatOptional(d.sub, stackTopID)
	}

	cur := c.compile(d.sub, stackTopID, nil)
	for i := 1; i < min; i++ {
		next := c.compile(d.sub, stackTopID, &cur.exit)
		cur = dpdaFragment{entry: cur.entry, exit: next.exit, edges: append(append([]abstractTransition(nil), cur.edges...), next.edges...)}
	}
	if max == min {
		if n.annotationID != nil {
			c.annotations[cur.exit] = *n.annotationID
		}
		return cur
	}
	firstSet := vistraEdgesFrom(cur.edges, cur.entry)
	combined := make([]abstractTransition, 0, len(cur.edges)+2*len(firstSet))
	combined = append(combined, cur.edges...)
	if max > min && min >= 2 {
		mid := c.newState()
		for _, t := range firstSet {
			c.emitInto(&combined, cur.exit, mid, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID)
		}
		for _, t := range firstSet {
			c.emitInto(&combined, mid, cur.entry, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID)
		}
		annID := AnnotationID(0)
		if n.annotationID != nil {
			annID = *n.annotationID
		}
		c.annotations[mid] = annID
	} else {
		for _, t := range firstSet {
			c.emitInto(&combined, cur.exit, cur.entry, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID)
		}
	}
	if n.annotationID != nil {
		c.annotations[cur.exit] = *n.annotationID
	}
	return dpdaFragment{entry: cur.entry, exit: cur.exit, edges: combined}
}

func vistraFirstSetContains(firstSet []abstractTransition, inputID, stackID uint64) bool {
	for _, t := range firstSet {
		if t.inputSymbolID == inputID && t.stackTopID == stackID {
			return true
		}
	}
	return false
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileRepeatStar(sub *VistraAST[TObs], stackTopID uint64) dpdaFragment {
	body := c.compile(sub, stackTopID, nil)
	firstSet := vistraEdgesFrom(body.edges, body.entry)
	edges := make([]abstractTransition, 0, len(body.edges)+len(firstSet))
	edges = append(edges, body.edges...)
	for _, t := range firstSet {
		c.emitInto(&edges, body.exit, t.toState, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID)
	}
	annID := AnnotationID(0)
	c.annotations[body.entry] = annID
	return dpdaFragment{entry: body.entry, exit: body.exit, edges: edges}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileRepeatPlus(sub *VistraAST[TObs], stackTopID uint64) dpdaFragment {
	body := c.compile(sub, stackTopID, nil)
	firstSet := vistraEdgesFrom(body.edges, body.entry)
	edges := make([]abstractTransition, 0, len(body.edges)+len(firstSet))
	edges = append(edges, body.edges...)
	for _, t := range firstSet {
		c.emitInto(&edges, body.exit, t.toState, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID)
	}
	return dpdaFragment{entry: body.entry, exit: body.exit, edges: edges}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileRepeatOptional(sub *VistraAST[TObs], stackTopID uint64) dpdaFragment {
	body := c.compile(sub, stackTopID, nil)
	firstSet := vistraEdgesFrom(body.edges, body.entry)
	sink := c.newState()
	edges := make([]abstractTransition, 0, len(body.edges)+2*len(firstSet))
	for _, t := range body.edges {
		to := t.toState
		if t.fromState == body.entry && to == body.exit {
			to = sink
		}
		edges = append(edges, abstractTransition{t.fromState, to, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID})
	}
	for _, t := range firstSet {
		c.emitInto(&edges, body.exit, body.entry, t.inputSymbolID, t.stackTopID, t.opKind, t.pushPopSymbolID)
	}
	annID := AnnotationID(0)
	c.annotations[body.entry] = annID
	return dpdaFragment{entry: body.exit, exit: body.exit, edges: edges}
}

func (c *vistraDPDACompiler[TObs, TOutcome]) compileNest(n *VistraAST[TObs], stackTopID uint64, reuseEntry *uint64) dpdaFragment {
	d := n.data.(vistraNestData[TObs])
	nestStackID := c.nextStackSymbolID
	c.nextStackSymbolID++

	var nestEntry uint64
	if reuseEntry != nil {
		nestEntry = *reuseEntry
	} else {
		nestEntry = c.newState()
	}
	nestExit := c.newState()
	edges := make([]abstractTransition, 0)

	body := c.compile(d.body, nestStackID, nil)
	for _, lid := range d.callSymbolIDs {
		c.emitLogicalInto(&edges, nestEntry, body.entry, lid, stackTopID, autarch.Push, nestStackID)
	}
	for _, lid := range d.returnSymbolIDs {
		c.emitLogicalInto(&edges, body.exit, nestExit, lid, nestStackID, autarch.Pop, 0)
	}
	edges = append(edges, body.edges...)

	if n.annotationID != nil {
		c.annotations[nestExit] = *n.annotationID
	}
	return dpdaFragment{entry: nestEntry, exit: nestExit, edges: edges}
}

func vistraCollectStateIDs(transitions []abstractTransition, entry, exit uint64) []uint64 {
	seen := make(map[uint64]struct{})
	seen[entry] = struct{}{}
	seen[exit] = struct{}{}
	for _, t := range transitions {
		seen[t.fromState] = struct{}{}
		seen[t.toState] = struct{}{}
	}
	out := make([]uint64, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func vistraValidateDeterminism(transitions []abstractTransition) error {
	type key struct {
		from  uint64
		input uint64
		stack uint64
	}
	count := make(map[key]int)
	for _, t := range transitions {
		k := key{t.fromState, t.inputSymbolID, t.stackTopID}
		count[k]++
	}
	for k, n := range count {
		if n > 1 {
			return fmt.Errorf("vistra dpda: determinism violated: state %d input %d stack %d has %d transitions", k.from, k.input, k.stack, n)
		}
	}
	return nil
}

func vistraNumberStates(allStates []uint64, rootEntry uint64) map[uint64]uint64 {
	stateMap := make(map[uint64]uint64)
	stateMap[rootEntry] = 0
	idx := uint64(1)
	for _, s := range allStates {
		if s != rootEntry {
			stateMap[s] = idx
			idx++
		}
	}
	return stateMap
}

func vistraBuildStackAlphabet[TStackSymbol any](numStackSymbols uint64) []autarch.SymbolDefinition[TStackSymbol] {
	defs := make([]autarch.SymbolDefinition[TStackSymbol], 0, numStackSymbols)
	for i := uint64(0); i < numStackSymbols; i++ {
		name := "BOS"
		if i > 0 {
			name = fmt.Sprintf("nest_%d", i-1)
		}
		id := i
		defs = append(defs, autarch.SymbolDefinition[TStackSymbol]{
			ID:          id,
			Name:        name,
			Match:       func(TStackSymbol) bool { return false },
			Observation: nil,
		})
	}
	return defs
}

func vistraEmitDPDATransitions[TObservation, TStackSymbol any](
	transitions []abstractTransition,
	stateMap map[uint64]uint64,
) []autarch.DPDATransition[TObservation, TStackSymbol] {
	out := make([]autarch.DPDATransition[TObservation, TStackSymbol], 0, len(transitions))
	for _, t := range transitions {
		from := stateMap[t.fromState]
		to := stateMap[t.toState]
		inputSym := autarch.SymbolCreate[TObservation]("", t.inputSymbolID)
		stackTopSym := autarch.SymbolCreate[TStackSymbol]("", t.stackTopID)
		op := autarch.StackOperation[TStackSymbol]{Kind: t.opKind}
		if t.opKind == autarch.Push || t.opKind == autarch.Replace {
			op.StackSymbol = autarch.SymbolCreate[TStackSymbol]("", t.pushPopSymbolID)
		}
		out = append(out, autarch.DPDATransition[TObservation, TStackSymbol]{
			CurrentState:    from,
			NextState:       to,
			InputSymbol:     inputSym,
			CurrentStackTop: stackTopSym,
			Operation:       op,
		})
	}
	return out
}
