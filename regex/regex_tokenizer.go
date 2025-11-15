package regex

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ------------------------------------------------------------
// TOKEN TYPES
// ------------------------------------------------------------

type regexTokenKind int

const (
	tokLiteral regexTokenKind = iota
	tokLParen
	tokRParen
	tokAlt      // |
	tokStar     // *
	tokPlus     // +
	tokQuestion // ?
	tokCharClass
	tokConcat
	tokEOF
	tokDot      // Wildcard
	tokClassSet // Pre-defined class like \d, \s
	tokBound
)

type regexToken struct {
	kind  regexTokenKind
	value rune   // for literals
	class []rune // for character classes

	min int
	max int

	negated bool
}

// ------------------------------------------------------------
// TOKENIZER
// ------------------------------------------------------------

func tokenizeRegex(input string) ([]regexToken, error) {
	tokens := make([]regexToken, 0, len(input)*2) // safe over-estimate

	i := 0
	for i < len(input) {
		r, width := utf8.DecodeRuneInString(input[i:])
		if r == utf8.RuneError {
			return nil, fmt.Errorf("invalid UTF-8 at byte index %d", i)
		}

		switch r {

		// --------------------------------------------------------
		// Single-character operators
		// --------------------------------------------------------
		case '{':
			// This is NOT a literal '{' if it's a valid bound.
			// Try to parse it as a bound.
			min, max, newI, err := readBound(input, i)
			if err == nil {
				tokens = append(tokens, regexToken{
					kind: tokBound,
					min:  min,
					max:  max,
				})
				i = newI
				continue
			}

		case '(':
			if i+width < len(input) && input[i+width] == '?' && i+width+1 < len(input) && input[i+width+1] == ':' {
				tokens = append(tokens, regexToken{kind: tokLParen})
				i += width + 2
				continue
			}

			tokens = append(tokens, regexToken{kind: tokLParen})
			i += width
			continue

		case ')':
			tokens = append(tokens, regexToken{kind: tokRParen})
			i += width
			continue

		case '|':
			tokens = append(tokens, regexToken{kind: tokAlt})
			i += width
			continue

		case '*':
			tokens = append(tokens, regexToken{kind: tokStar})
			i += width
			continue

		case '+':
			tokens = append(tokens, regexToken{kind: tokPlus})
			i += width
			continue

		case '?':
			tokens = append(tokens, regexToken{kind: tokQuestion})
			i += width
			continue

		case '.':
			tokens = append(tokens, regexToken{kind: tokDot})
			i += width
			continue

		// --------------------------------------------------------
		// Character classes
		// --------------------------------------------------------
		case '[':
			classRunes, negated, newI, err := readCharClass(input, i)
			if err != nil {
				return nil, err
			}

			tokens = append(tokens, regexToken{
				kind:    tokCharClass,
				class:   classRunes,
				negated: negated,
			})
			i = newI
			continue

		// --------------------------------------------------------
		// Escapes
		// --------------------------------------------------------
		case '\\':
			if i+width >= len(input) {
				return nil, fmt.Errorf("dangling escape at end of regex")
			}

			escaped, escW := utf8.DecodeRuneInString(input[i+width:])
			if escaped == utf8.RuneError {
				return nil, fmt.Errorf("invalid escaped UTF-8 at byte %d", i+width)
			}

			if escaped == 'x' {
				r, newI, err := parseHexEscape(input, i+width+escW)
				if err != nil {
					return nil, err
				}
				tokens = append(tokens, regexToken{kind: tokLiteral, value: r})
				i = newI // newI is the index *after* the hex escape
				continue
			}

			// Special escapes
			switch escaped {
			case 'n':
				tokens = append(tokens, regexToken{kind: tokLiteral, value: '\n'})
			case 't':
				tokens = append(tokens, regexToken{kind: tokLiteral, value: '\t'})
			case 'r':
				tokens = append(tokens, regexToken{kind: tokLiteral, value: '\r'})
			case 'd':
				tokens = append(tokens, regexToken{kind: tokClassSet, value: 'd'})
			case 's':
				tokens = append(tokens, regexToken{kind: tokClassSet, value: 's'})
			default:
				tokens = append(tokens, regexToken{kind: tokLiteral, value: escaped})
			}

			i += width + escW
			continue

		default:
			// literal rune
			tokens = append(tokens, regexToken{
				kind:  tokLiteral,
				value: r,
			})
			i += width
			continue
		}
	}

	tokens = append(tokens, regexToken{kind: tokEOF})
	return tokens, nil
}

// ------------------------------------------------------------
// CHARACTER CLASS READER
//
// Reads:
//   [abc]
//   [a-zA-Z0-9_]
//   [^a-z]
// ------------------------------------------------------------

func readCharClass(input string, start int) (runes []rune, negated bool, newIndex int, err error) {
	i := start
	_, width := utf8.DecodeRuneInString(input[i:]) // Skip '['
	i += width

	if i >= len(input) {
		return nil, false, 0, fmt.Errorf("unterminated character class")
	}

	r, w := utf8.DecodeRuneInString(input[i:])
	if r == '^' {
		negated = true
		i += w
	}

	classRunes := make(map[rune]struct{})

	var lastRune *rune

	// Helper to parse a single rune or escape
	parseRune := func(idx int) (r rune, newIdx int, err error) {
		ch, w := utf8.DecodeRuneInString(input[idx:])
		if ch == utf8.RuneError {
			return 0, 0, fmt.Errorf("invalid UTF-8 in character class")
		}

		if ch == '\\' {
			if idx+w >= len(input) {
				return 0, 0, fmt.Errorf("dangling escape at end of character class")
			}
			nextCh, nextW := utf8.DecodeRuneInString(input[idx+w:])
			if nextCh == 'x' {
				r, newI, err := parseHexEscape(input, idx+w+nextW)
				return r, newI, err
			}
			// General escape
			return nextCh, idx + w + nextW, nil
		}
		// Normal rune
		return ch, idx + w, nil
	}

	for {
		if i >= len(input) {
			return nil, false, 0, fmt.Errorf("unterminated character class")
		}

		// Check for end of class *first*
		ch, w := utf8.DecodeRuneInString(input[i:])
		if ch == ']' {
			// If we saw a '-', it was a literal (e.g., [a-])
			if lastRune != nil {
				classRunes[*lastRune] = struct{}{}
			}
			i += w
			break // Exit loop
		}

		// Check for range
		if ch == '-' && lastRune != nil {
			// This *might* be a range. 'lastRune' is the start.
			// Peek at the *next* character.
			nextIdx := i + w
			if nextIdx >= len(input) {
				return nil, false, 0, fmt.Errorf("unterminated character class")
			}

			nextCh, _ := utf8.DecodeRuneInString(input[nextIdx:])
			if nextCh == ']' {
				// This is '...-]', so '-' is a literal.
				classRunes[*lastRune] = struct{}{} // Add the rune before '-'
				classRunes['-'] = struct{}{}       // Add the '-'
				lastRune = nil                     // '-' can't start a range
				i += w
				continue
			}

			// It is a range. Parse the end rune.
			endRune, newI, err := parseRune(nextIdx)
			if err != nil {
				return nil, false, 0, err
			}

			// Add all runes in the range
			for r := *lastRune; r <= endRune; r++ {
				classRunes[r] = struct{}{}
			}

			lastRune = nil // Range was consumed
			i = newI
			continue
		}

		// This is a normal character or the start of a range.
		// If we had a pending '-', add it as a literal first.
		if lastRune != nil {
			classRunes[*lastRune] = struct{}{}
		}

		// Parse the current rune
		r, newI, err := parseRune(i)
		if err != nil {
			return nil, false, 0, err
		}

		// Store it in 'lastRune' in case it's the start of a range
		lastRune = &r
		i = newI
	}

	// Convert set to slice
	finalRunes := make([]rune, 0, len(classRunes))
	for r := range classRunes {
		finalRunes = append(finalRunes, r)
	}

	return finalRunes, negated, i, nil
}

func parseHexEscape(input string, start int) (rune, int, error) {
	if start >= len(input) {
		return 0, 0, fmt.Errorf("unexpected end of string after \\x")
	}

	if input[start] == '{' {
		braceStart := start + 1
		braceEnd := strings.IndexByte(input[braceStart:], '}')

		if braceEnd == -1 {
			return 0, 0, fmt.Errorf("unterminated \\x{...} escape at index %d", start-2)
		}

		absBraceEnd := braceStart + braceEnd
		hexStr := input[braceStart:absBraceEnd]

		if len(hexStr) == 0 {
			return 0, 0, fmt.Errorf("empty \\x{} escape at index %d", start-2)
		}

		val, err := strconv.ParseInt(hexStr, 16, 32)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid hex in \\x{...} escape at index %d: %w", start-2, err)
		}

		if !utf8.ValidRune(int32(val)) {
			return 0, 0, fmt.Errorf("hex value %q is not a valid Unicode code point", hexStr)
		}

		return rune(val), absBraceEnd + 1, nil
	}

	if start+1 >= len(input) {
		return 0, 0, fmt.Errorf("malformed \\x escape (requires 2 hex digits) at index %d", start-2)
	}

	hexStr := input[start : start+2]
	val, err := strconv.ParseInt(hexStr, 16, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hex in \\x.. escape at index %d: %w", start-2, err)
	}

	return rune(val), start + 2, nil
}

func readBound(input string, start int) (min, max int, newIndex int, err error) {
	i := start + 1 // Skip the '{'
	if i >= len(input) {
		return 0, 0, 0, fmt.Errorf("not a bound: unterminated {")
	}

	// Find the closing brace
	end := strings.IndexByte(input[i:], '}')
	if end == -1 {
		return 0, 0, 0, fmt.Errorf("not a bound: missing }")
	}

	content := input[i : i+end]
	newIndex = i + end + 1 // Index after the '}'

	parts := strings.Split(content, ",")

	switch len(parts) {
	case 1:
		// {n}
		n, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("not a bound: invalid number %q", parts[0])
		}
		if n < 0 {
			return 0, 0, 0, fmt.Errorf("not a bound: negative repetition %d", n)
		}
		return n, n, newIndex, nil // min=n, max=n

	case 2:
		// {n,} or {n,m}
		minStr, maxStr := parts[0], parts[1]

		min, err := strconv.Atoi(minStr)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("not a bound: invalid min %q", minStr)
		}
		if min < 0 {
			return 0, 0, 0, fmt.Errorf("not a bound: negative repetition %d", min)
		}

		if maxStr == "" {
			// {n,}
			return min, -1, newIndex, nil // max=-1 signifies unbounded
		}

		max, err := strconv.Atoi(maxStr)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("not a bound: invalid max %q", maxStr)
		}
		if max < min {
			return 0, 0, 0, fmt.Errorf("not a bound: max %d less than min %d", max, min)
		}
		return min, max, newIndex, nil

	default:
		return 0, 0, 0, fmt.Errorf("not a bound: invalid content %q", content)
	}
}
