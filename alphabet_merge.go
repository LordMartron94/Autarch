package autarch

/*
AlphabetMergeResult holds a deduplicated alphabet and per-source symbol ID remaps.

Remaps[i][oldSymbolID] is the new physical symbol ID in Alphabet for source i.
Symbol names are the deduplication key; SymbolDefinition.ID equals its index in Alphabet.
*/
type AlphabetMergeResult[TObservation any] struct {
	Alphabet []SymbolDefinition[TObservation]
	Remaps   []map[uint64]uint64
}

/*
MergeAlphabets unions multiple symbol tables by Name, preserving first-seen definition order
per name and reassigning IDs to match slice indices.
*/
func MergeAlphabets[TObservation any](
	sources ...[]SymbolDefinition[TObservation],
) AlphabetMergeResult[TObservation] {
	nameToDef := make(map[string]SymbolDefinition[TObservation])
	added := make(map[string]bool)
	orderedNames := make([]string, 0)

	for _, src := range sources {
		for _, def := range src {
			if _, ok := nameToDef[def.Name]; !ok {
				nameToDef[def.Name] = def
				orderedNames = append(orderedNames, def.Name)
			}
		}
	}

	merged := make([]SymbolDefinition[TObservation], 0, len(orderedNames))
	nameToFinalID := make(map[string]uint64)
	for _, name := range orderedNames {
		if added[name] {
			continue
		}
		def := nameToDef[name]
		def.ID = uint64(len(merged))
		merged = append(merged, def)
		added[name] = true
		nameToFinalID[name] = def.ID
	}

	remaps := make([]map[uint64]uint64, len(sources))
	for i, src := range sources {
		remaps[i] = make(map[uint64]uint64, len(src))
		for oldID, def := range src {
			remaps[i][uint64(oldID)] = nameToFinalID[def.Name]
		}
	}

	return AlphabetMergeResult[TObservation]{
		Alphabet: merged,
		Remaps:   remaps,
	}
}

/*
MergeTwoAlphabetsNondeterministicResolver builds a resolver over a merged alphabet from two sources.
*/
func MergeTwoAlphabetsNondeterministicResolver[TObservation any](
	merged []SymbolDefinition[TObservation],
	remapA, remapB map[uint64]uint64,
	resolveA, resolveB NondeterministicSymbolResolver[TObservation],
) NondeterministicSymbolResolver[TObservation] {
	seenEpoch := make([]uint32, len(merged))
	currentEpoch := uint32(1)
	outScratch := make([]uint64, 0, len(merged))

	return func(observation TObservation) []uint64 {
		idsA := resolveA(observation)
		idsB := resolveB(observation)
		if len(idsA) == 0 && len(idsB) == 0 {
			return nil
		}
		currentEpoch++
		if currentEpoch == 0 {
			for i := range seenEpoch {
				seenEpoch[i] = 0
			}
			currentEpoch = 1
		}
		outScratch = outScratch[:0]

		for _, id := range idsA {
			mergedID := remapA[id]
			if seenEpoch[mergedID] == currentEpoch {
				continue
			}
			seenEpoch[mergedID] = currentEpoch
			outScratch = append(outScratch, mergedID)
		}
		for _, id := range idsB {
			mergedID := remapB[id]
			if seenEpoch[mergedID] == currentEpoch {
				continue
			}
			seenEpoch[mergedID] = currentEpoch
			outScratch = append(outScratch, mergedID)
		}
		return outScratch
	}
}
