package pattern

import (
	"autarch"
	"foundation/domain"
	"testing"
)

func runePtr(v rune) *rune {
	return &v
}

func TestRuneTwoLevelResolverBoundaryAndAscii(t *testing.T) {
	obsDomain := domain.DiscreteDomainRuneCreate()

	defs := []symbolInterval[rune]{
		{lo: 'a', hi: 'z', symbolID: 1},
		{lo: 0x00FF, hi: 0x00FF, symbolID: 2},
		{lo: 0x0100, hi: 0x0102, symbolID: 3},
		{lo: 0x03B1, hi: 0x03B3, symbolID: 4}, // alpha..gamma
	}

	resolver, ok := buildRuneTwoLevelResolver(defs, obsDomain)
	if !ok {
		t.Fatalf("expected two-level resolver for rune domain")
	}

	cases := []struct {
		in   rune
		id   uint64
		hit  bool
		name string
	}{
		{in: 'a', id: 1, hit: true, name: "ascii_lower"},
		{in: 'z', id: 1, hit: true, name: "ascii_upper"},
		{in: 0x00FF, id: 2, hit: true, name: "page_end_hit"},
		{in: 0x0100, id: 3, hit: true, name: "next_page_start_hit"},
		{in: 0x0102, id: 3, hit: true, name: "next_page_end_hit"},
		{in: 0x0103, id: 0, hit: false, name: "range_miss_after_page_edge"},
		{in: 0x03B2, id: 4, hit: true, name: "greek_mid"},
		{in: '#', id: 0, hit: false, name: "ascii_miss"},
	}

	for _, tc := range cases {
		got, hit := resolver(tc.in)
		if hit != tc.hit {
			t.Fatalf("%s: expected hit=%v got=%v", tc.name, tc.hit, hit)
		}
		if !tc.hit {
			continue
		}
		if got != tc.id {
			t.Fatalf("%s: expected id=%d got=%d", tc.name, tc.id, got)
		}
	}
}

func TestRuneTwoLevelResolverParityWithIntervalResolver(t *testing.T) {
	obsDomain := domain.DiscreteDomainRuneCreate()

	defs := []autarch.SymbolDefinition[rune]{
		{ID: 0, Name: "ascii_word", GapLo: runePtr('`'), GapHi: runePtr('{')},         // a-z
		{ID: 1, Name: "latin1_tail", Observation: runePtr(0x00FF)},                    // boundary point
		{ID: 2, Name: "plane0_range", GapLo: runePtr(0x03AF), GapHi: runePtr(0x03B4)}, // 0x03B0..0x03B3
		{ID: 3, Name: "plane1_point", Observation: runePtr(0x10437)},
	}

	intervals := buildSymbolIntervals(defs, obsDomain)
	twoLevel, ok := buildRuneTwoLevelResolver(intervals, obsDomain)
	if !ok {
		t.Fatalf("expected two-level resolver for rune domain")
	}
	interval := buildIntervalResolver(intervals, obsDomain)

	sample := []rune{
		'a', 'm', 'z',
		'0', '#',
		0x00FE, 0x00FF, 0x0100,
		0x03AF, 0x03B0, 0x03B1, 0x03B3, 0x03B4,
		0x10437, 0x10438,
	}

	for _, r := range sample {
		idA, okA := twoLevel(r)
		idB, okB := interval(r)
		if okA != okB || idA != idB {
			t.Fatalf("resolver mismatch for rune U+%04X: two-level=(%d,%v) interval=(%d,%v)", r, idA, okA, idB, okB)
		}
	}
}
