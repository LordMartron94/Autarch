package pattern

import (
	"autarch"
	"fmt"
	"foundation/domain"
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
without exposing the concrete collector. Regula collect/bind functions take a symbolFeeder
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
	observationDomain *domain.DiscreteDomain[TObs],
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
		if observationDomain.LessThan(a, b) {
			return -1
		}
		if observationDomain.LessThan(b, a) {
			return 1
		}
		return 0
	})
	points = slices.CompactFunc(points, func(a, b TObs) bool {
		return !observationDomain.LessThan(a, b) && !observationDomain.LessThan(b, a)
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
				if !observationDomain.LessThan(val, cr.lo) && !observationDomain.LessThan(cr.hi, val) {
					pointCover = append(pointCover, r.id)
					break
				}
			}
		}

		addPhysical(
			func(o TObs) bool { return !observationDomain.LessThan(o, val) && !observationDomain.LessThan(val, o) },
			fmt.Sprintf("pt:%v", val),
			nil,
			nil,
			pointCover,
		)

		if i == len(points)-1 {
			continue
		}

		next := points[i+1]

		if s, ok := observationDomain.NextFn(p); ok &&
			!observationDomain.LessThan(s, next) && !observationDomain.LessThan(next, s) {
			continue
		}
		var gapCover []logicalID
		for _, r := range reqs {
			for _, cr := range r.ranges {
				if !observationDomain.LessThan(p, cr.lo) && !observationDomain.LessThan(cr.hi, next) {
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
			func(o TObs) bool { return observationDomain.LessThan(lo, o) && observationDomain.LessThan(o, hi) },
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
