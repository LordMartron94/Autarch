package pattern

import (
	"autarch"
	"foundation/domain"
	"testing"
)

func benchmarkRuneResolverInput(resolver autarch.DeterministicSymbolResolver[rune], input []rune, b *testing.B) {
	var sink uint64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, r := range input {
			if id, ok := resolver(r); ok {
				sink ^= id + uint64(r)
			}
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(len(input)), "runes/op")
	_ = sink
}

func benchmarkBuildResolvers() (autarch.DeterministicSymbolResolver[rune], autarch.DeterministicSymbolResolver[rune]) {
	obsDomain := domain.DiscreteDomainRuneCreate()
	defs := []autarch.SymbolDefinition[rune]{
		{ID: 0, Name: "letters", GapLo: runePtr('`'), GapHi: runePtr('{')}, // a-z
		{ID: 1, Name: "digits", GapLo: runePtr('/'), GapHi: runePtr(':')},  // 0-9
		{ID: 2, Name: "latin1", Observation: runePtr(0x00FF)},
		{ID: 3, Name: "greek", GapLo: runePtr(0x03AF), GapHi: runePtr(0x03C0)},
		{ID: 4, Name: "plane1", Observation: runePtr(0x10437)},
	}
	intervals := buildSymbolIntervals(defs, obsDomain)

	twoLevel, ok := buildRuneTwoLevelResolver(intervals, obsDomain)
	if !ok {
		panic("expected rune two-level resolver")
	}
	interval := buildIntervalResolver(intervals, obsDomain)
	return twoLevel, interval
}

func BenchmarkRuneResolverTwoLevelASCIIHeavy(b *testing.B) {
	twoLevel, _ := benchmarkBuildResolvers()
	input := []rune("let alpha = 42 + beta + gamma + delta + epsilon + zeta")
	benchmarkRuneResolverInput(twoLevel, input, b)
}

func BenchmarkRuneResolverTwoLevelMixed(b *testing.B) {
	twoLevel, _ := benchmarkBuildResolvers()
	input := []rune("alpha βeta γamma 123 ζeta 𐐷 omega 0xFF")
	benchmarkRuneResolverInput(twoLevel, input, b)
}

func BenchmarkRuneResolverTwoLevelNonASCIIHeavy(b *testing.B) {
	twoLevel, _ := benchmarkBuildResolvers()
	input := []rune{0x03B1, 0x03B2, 0x03B3, 0x03BF, 0x03C0, 0x00FF, 0x10437, 0x03B4, 0x03B5, 0x03B6}
	benchmarkRuneResolverInput(twoLevel, input, b)
}

func BenchmarkRuneResolverIntervalASCIIHeavy(b *testing.B) {
	_, interval := benchmarkBuildResolvers()
	input := []rune("let alpha = 42 + beta + gamma + delta + epsilon + zeta")
	benchmarkRuneResolverInput(interval, input, b)
}

func BenchmarkRuneResolverIntervalMixed(b *testing.B) {
	_, interval := benchmarkBuildResolvers()
	input := []rune("alpha βeta γamma 123 ζeta 𐐷 omega 0xFF")
	benchmarkRuneResolverInput(interval, input, b)
}

func BenchmarkRuneResolverIntervalNonASCIIHeavy(b *testing.B) {
	_, interval := benchmarkBuildResolvers()
	input := []rune{0x03B1, 0x03B2, 0x03B3, 0x03BF, 0x03C0, 0x00FF, 0x10437, 0x03B4, 0x03B5, 0x03B6}
	benchmarkRuneResolverInput(interval, input, b)
}
