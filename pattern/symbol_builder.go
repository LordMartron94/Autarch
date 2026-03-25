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
	ranges []CharRange[TObs]
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
		ranges: []CharRange[TObs]{{Lo: v, Hi: v}},
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
	Definitions             []autarch.SymbolDefinition[TObs]
	Mapping                 map[logicalID][]physicalID
	DeterministicResolver   autarch.DeterministicSymbolResolver[TObs]
	NondeterministicResolve autarch.NondeterministicSymbolResolver[TObs]
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
			points = append(points, cr.Lo, cr.Hi)
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
		observation *TObs,
		lo, hi *TObs,
		covering []logicalID,
	) {
		id := physicalID(len(defs))

		for _, lid := range covering {
			mapping[lid] = append(mapping[lid], id)
		}

		defs = append(defs, autarch.SymbolDefinition[TObs]{
			ID:          uint64(id),
			Name:        name,
			Match:       match,
			Observation: observation,
			GapLo:       lo,
			GapHi:       hi,
		})
	}

	for i, p := range points {
		val := p

		var pointCover []logicalID
		for _, r := range reqs {
			for _, cr := range r.ranges {
				if !observationDomain.LessThan(val, cr.Lo) && !observationDomain.LessThan(cr.Hi, val) {
					pointCover = append(pointCover, r.id)
					break
				}
			}
		}

		addPhysical(
			func(o TObs) bool { return !observationDomain.LessThan(o, val) && !observationDomain.LessThan(val, o) },
			fmt.Sprintf("pt:%v", val),
			&val,
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
				if !observationDomain.LessThan(p, cr.Lo) && !observationDomain.LessThan(cr.Hi, next) {
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
			nil,
			&lo,
			&hi,
			gapCover,
		)
	}

	return alphabetBuildResult[TObs]{
		Definitions:             defs,
		Mapping:                 mapping,
		DeterministicResolver:   buildDeterministicResolver(defs, observationDomain),
		NondeterministicResolve: buildNondeterministicResolver(defs, observationDomain),
	}
}

type symbolInterval[TObs any] struct {
	lo       TObs
	hi       TObs
	symbolID uint64
}

func buildNondeterministicResolver[TObs any](
	defs []autarch.SymbolDefinition[TObs],
	observationDomain *domain.DiscreteDomain[TObs],
) autarch.NondeterministicSymbolResolver[TObs] {
	deterministic := buildDeterministicResolver(defs, observationDomain)
	return func(observation TObs) []uint64 {
		symbolID, ok := deterministic(observation)
		if !ok {
			return nil
		}
		return []uint64{symbolID}
	}
}

func buildDeterministicResolver[TObs any](
	defs []autarch.SymbolDefinition[TObs],
	observationDomain *domain.DiscreteDomain[TObs],
) autarch.DeterministicSymbolResolver[TObs] {
	intervals := buildSymbolIntervals(defs, observationDomain)
	if dense, ok := buildDenseByteResolver(intervals, observationDomain); ok {
		return dense
	}
	return buildIntervalResolver(intervals, observationDomain)
}

func buildSymbolIntervals[TObs any](
	defs []autarch.SymbolDefinition[TObs],
	observationDomain *domain.DiscreteDomain[TObs],
) []symbolInterval[TObs] {
	intervals := make([]symbolInterval[TObs], 0, len(defs))
	for _, def := range defs {
		if def.Observation != nil {
			intervals = append(intervals, symbolInterval[TObs]{
				lo:       *def.Observation,
				hi:       *def.Observation,
				symbolID: def.ID,
			})
			continue
		}
		if def.GapLo == nil || def.GapHi == nil {
			continue
		}

		loInclusive, loOk := observationDomain.NextFn(*def.GapLo)
		hiInclusive, hiOk := observationDomain.PreviousFn(*def.GapHi)
		if !loOk || !hiOk || observationDomain.GreaterThan(loInclusive, hiInclusive) {
			continue
		}

		intervals = append(intervals, symbolInterval[TObs]{
			lo:       loInclusive,
			hi:       hiInclusive,
			symbolID: def.ID,
		})
	}

	slices.SortFunc(intervals, func(a, b symbolInterval[TObs]) int {
		return observationDomain.OrderingCmp(a.lo, b.lo)
	})
	return intervals
}

func buildIntervalResolver[TObs any](
	intervals []symbolInterval[TObs],
	observationDomain *domain.DiscreteDomain[TObs],
) autarch.DeterministicSymbolResolver[TObs] {
	return func(observation TObs) (uint64, bool) {
		if len(intervals) == 0 {
			return 0, false
		}

		left := 0
		right := len(intervals) - 1
		for left <= right {
			mid := left + (right-left)/2
			entry := intervals[mid]
			if observationDomain.LessThan(observation, entry.lo) {
				right = mid - 1
				continue
			}
			if observationDomain.GreaterThan(observation, entry.hi) {
				left = mid + 1
				continue
			}
			return entry.symbolID, true
		}
		return 0, false
	}
}

func buildDenseByteResolver[TObs any](
	intervals []symbolInterval[TObs],
	observationDomain *domain.DiscreteDomain[TObs],
) (autarch.DeterministicSymbolResolver[TObs], bool) {
	min, minOK := any(observationDomain.Min).(byte)
	max, maxOK := any(observationDomain.Max).(byte)
	if !minOK || !maxOK || min != 0 || max != 255 {
		return nil, false
	}

	table := make([]uint64, 256)
	valid := make([]bool, 256)

	for _, entry := range intervals {
		lo, loOK := any(entry.lo).(byte)
		hi, hiOK := any(entry.hi).(byte)
		if !loOK || !hiOK {
			return nil, false
		}
		for idx := lo; idx <= hi; idx++ {
			table[idx] = entry.symbolID
			valid[idx] = true
			if idx == 255 {
				break
			}
		}
	}

	return func(observation TObs) (uint64, bool) {
		obs, ok := any(observation).(byte)
		if !ok || !valid[obs] {
			return 0, false
		}
		return table[obs], true
	}, true
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
