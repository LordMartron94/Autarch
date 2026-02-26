package pattern

import (
	"autarch"
	"fmt"
	"slices"
)

// ------------------------------------------------------- TYPES

type ObservationFormatter[TObservation any] struct {
	ToBytes func(observations []TObservation) []byte
}

func (o *ObservationFormatter[TObservation]) toBytesSingle(observation TObservation) []byte {
	return o.ToBytes([]TObservation{observation})
}

// ------------------------------------------------------- LOGICAL

type logicalID uint64

type symbolRequest[TObs any] struct {
	id     logicalID
	ranges []charRange[TObs]
}

type symbolCollector[TObs any] struct {
	nextID uint64
	keys   map[autarch.SymbolKey]logicalID
	reqs   []symbolRequest[TObs]

	formatter ObservationFormatter[TObs]
}

func newSymbolCollector[TObs any](
	formatter ObservationFormatter[TObs],
) *symbolCollector[TObs] {
	if formatter.ToBytes == nil {
		panic("formatter.ToBytes is required")
	}

	return &symbolCollector[TObs]{
		keys:      make(map[autarch.SymbolKey]logicalID),
		formatter: formatter,
	}
}

/*
symbolCollectorAddLiteral registers a single literal observation and returns its logical symbol ID.
Duplicate literals receive the same ID. Call this when walking an AST to feed symbol requests.
*/
func symbolCollectorAddLiteral[TObs any](c *symbolCollector[TObs], v TObs) logicalID {
	h := hashValue(v, c.formatter.toBytesSingle)
	key := autarch.SymbolKey{Kind: autarch.SymbolKindLiteral, Hash: h}

	if id, ok := c.keys[key]; ok {
		return id
	}

	id := logicalID(c.nextID)
	c.nextID++
	c.keys[key] = id

	c.reqs = append(c.reqs, symbolRequest[TObs]{
		id:     id,
		ranges: []charRange[TObs]{{lo: v, hi: v}},
	})

	return id
}

/*
symbolCollectorAddClass registers a character class and returns its logical symbol ID.
Duplicate classes (by range set) receive the same ID. Call this when walking an AST to feed symbol requests.
*/
func symbolCollectorAddClass[TObs any](c *symbolCollector[TObs], cls charClass[TObs]) logicalID {
	h := hashRanges(cls.ranges, c.formatter.ToBytes)
	key := autarch.SymbolKey{Kind: autarch.SymbolKindClass, Hash: h}

	if id, ok := c.keys[key]; ok {
		return id
	}

	id := logicalID(c.nextID)
	c.nextID++
	c.keys[key] = id

	c.reqs = append(c.reqs, symbolRequest[TObs]{
		id:     id,
		ranges: cls.ranges,
	})

	return id
}

/*
symbolFeeder is the interface used to feed literals and classes into the shared compilation context
without exposing the concrete collector. Regula and Vistra collect/bind functions take a symbolFeeder
so the same context can be used for either AST type.
*/
type symbolFeeder[TObs any] interface {
	addLiteral(v TObs) logicalID
	addClass(cls charClass[TObs]) logicalID
}

func (c *symbolCollector[TObs]) addLiteral(v TObs) logicalID {
	return symbolCollectorAddLiteral(c, v)
}

func (c *symbolCollector[TObs]) addClass(cls charClass[TObs]) logicalID {
	return symbolCollectorAddClass(c, cls)
}

// ------------------------------------------------------- ALPHABET PARTITIONER

type physicalID uint64

type alphabetBuildResult[TObs any] struct {
	Definitions []autarch.SymbolDefinition[TObs]
	Mapping     map[logicalID][]physicalID
}

func buildAlphabet[TObs any](
	reqs []symbolRequest[TObs],
	isLess func(a, b TObs) bool,
	successor SuccessorFn[TObs],
) alphabetBuildResult[TObs] {
	mapping := make(map[logicalID][]physicalID)

	if len(reqs) == 0 {
		return alphabetBuildResult[TObs]{Mapping: mapping}
	}

	var points []TObs
	for _, r := range reqs {
		for _, cr := range r.ranges {
			points = append(points, cr.lo, cr.hi)
		}
	}

	slices.SortFunc(points, func(a, b TObs) int {
		if isLess(a, b) {
			return -1
		}
		if isLess(b, a) {
			return 1
		}
		return 0
	})
	points = slices.CompactFunc(points, func(a, b TObs) bool {
		return !isLess(a, b) && !isLess(b, a)
	})

	var defs []autarch.SymbolDefinition[TObs]

	addPhysical := func(
		match func(TObs) bool,
		name string,
		lo, hi *TObs,
		covering []logicalID,
	) {
		id := physicalID(len(defs))

		for _, lid := range covering {
			mapping[lid] = append(mapping[lid], id)
		}

		defs = append(defs, autarch.SymbolDefinition[TObs]{
			ID:    uint64(id),
			Name:  name,
			Match: match,
			GapLo: lo,
			GapHi: hi,
		})
	}

	for i, p := range points {
		val := p

		var pointCover []logicalID
		for _, r := range reqs {
			for _, cr := range r.ranges {
				if !isLess(val, cr.lo) && !isLess(cr.hi, val) {
					pointCover = append(pointCover, r.id)
					break
				}
			}
		}

		addPhysical(
			func(o TObs) bool { return !isLess(o, val) && !isLess(val, o) },
			fmt.Sprintf("pt:%v", val),
			nil,
			nil,
			pointCover,
		)

		if i == len(points)-1 {
			continue
		}

		next := points[i+1]

		if successor != nil {
			if s, ok := successor(p); ok &&
				!isLess(s, next) && !isLess(next, s) {
				continue
			}
		}

		var gapCover []logicalID
		for _, r := range reqs {
			for _, cr := range r.ranges {
				if !isLess(p, cr.lo) && !isLess(cr.hi, next) {
					gapCover = append(gapCover, r.id)
					break
				}
			}
		}

		if len(gapCover) == 0 {
			continue
		}

		lo, hi := p, next
		addPhysical(
			func(o TObs) bool { return isLess(lo, o) && isLess(o, hi) },
			fmt.Sprintf("gap:(%v,%v)", lo, hi),
			&lo,
			&hi,
			gapCover,
		)
	}

	return alphabetBuildResult[TObs]{
		Definitions: defs,
		Mapping:     mapping,
	}
}

// ------------------------------------------------------- SYMBOL EXPANDER

type symbolExpander struct {
	mapping map[logicalID][]physicalID
}

func newSymbolExpander(
	m map[logicalID][]physicalID,
) symbolExpander {
	return symbolExpander{mapping: m}
}

func (e symbolExpander) expand(id logicalID) []physicalID {
	return e.mapping[id]
}
