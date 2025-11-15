package regex

import (
	"fmt"
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
)

type regexToken struct {
	kind  regexTokenKind
	value rune   // for literals
	class []rune // for character classes
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
		case '(':
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

		// --------------------------------------------------------
		// Character classes
		// --------------------------------------------------------
		case '[':
			classRunes, newI, err := readCharClass(input, i)
			if err != nil {
				return nil, err
			}

			tokens = append(tokens, regexToken{
				kind:  tokCharClass,
				class: classRunes,
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

			// Special escapes
			switch escaped {
			case 'n':
				tokens = append(tokens, regexToken{kind: tokLiteral, value: '\n'})
			case 't':
				tokens = append(tokens, regexToken{kind: tokLiteral, value: '\t'})
			case 'r':
				tokens = append(tokens, regexToken{kind: tokLiteral, value: '\r'})
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

func readCharClass(input string, start int) ([]rune, int, error) {
	i := start
	_, width := utf8.DecodeRuneInString(input[i:])
	i += width

	if i >= len(input) {
		return nil, 0, fmt.Errorf("unterminated character class")
	}

	var negated bool
	r, w := utf8.DecodeRuneInString(input[i:])
	if r == '^' {
		negated = true
		i += w
	}

	classRunes := make([]rune, 0, 16)

	for {
		if i >= len(input) {
			return nil, 0, fmt.Errorf("unterminated character class")
		}

		ch, w := utf8.DecodeRuneInString(input[i:])
		if ch == utf8.RuneError {
			return nil, 0, fmt.Errorf("invalid UTF-8 in character class")
		}

		if ch == ']' {
			i += w

			if !negated {
				return classRunes, i, nil
			}

			return nil, 0, fmt.Errorf("negated classes [^...] not supported yet")
		}

		// Check for range: a-z
		nextStart := i + w
		if nextStart < len(input) {
			ch2, w2 := utf8.DecodeRuneInString(input[nextStart:])
			if ch2 == '-' {
				afterDash := nextStart + w2
				if afterDash < len(input) {
					ch3, w3 := utf8.DecodeRuneInString(input[afterDash:])
					if ch3 != utf8.RuneError && ch3 != ']' {
						for rr := ch; rr <= ch3; rr++ {
							classRunes = append(classRunes, rr)
						}
						i = afterDash + w3
						continue
					}
				}
			}
		}

		classRunes = append(classRunes, ch)
		i += w
	}
}
