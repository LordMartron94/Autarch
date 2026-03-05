package pattern

import (
	"fmt"
	"unicode/utf8"
)

/*
RegexToRegula parses a restricted regex string and returns a RegulaAST[rune] that matches the same language.

Supported syntax (RE2-like subset):
  - Literal: a, ., (unescaped non-metacharacters)
  - Escapes: \. \* \+ \? \| \[ \] \( \) \\ \d \s \n \r \t (and \x for literal x when x is not special)
  - . (dot): any character (uses factory domain; requires rune DiscreteDomain)
  - [a-z], [^a-z]: character class and negated class; - denotes range, ] and - as first char are literal
  - * + ?: repetition (zero-or-more, one-or-more, zero-or-one)
  - |: alternation (union)
  - ( ): grouping for precedence
  - Concatenation: ab

Unsupported: \w \D \S, backreferences, anchors ^ $, {m,n}, non-greedy, (?...).
*/
func RegexToRegula(regex string, f *RegulaASTFactory[rune]) (RegulaAST[rune], error) {
	if f == nil {
		return RegulaAST[rune]{}, fmt.Errorf("RegexToRegula: nil factory")
	}
	p := regexParser{s: regex, f: f}
	ast, err := p.parseUnion()
	if err != nil {
		return RegulaAST[rune]{}, err
	}
	if p.pos < len(p.s) {
		return RegulaAST[rune]{}, fmt.Errorf("RegexToRegula: unexpected trailing input at position %d", p.pos)
	}
	return ast, nil
}

func (p *regexParser) atEnd() bool {
	return p.pos >= len(p.s)
}

type regexParser struct {
	s   string
	pos int
	f   *RegulaASTFactory[rune]
}

func (p *regexParser) peek() (rune, int) {
	if p.pos >= len(p.s) {
		return 0, 0
	}
	return utf8.DecodeRuneInString(p.s[p.pos:])
}

func (p *regexParser) advance() rune {
	r, size := p.peek()
	if size > 0 {
		p.pos += size
	}
	return r
}

func (p *regexParser) parseUnion() (RegulaAST[rune], error) {
	first, err := p.parseConcat()
	if err != nil {
		return RegulaAST[rune]{}, err
	}
	var terms []RegulaAST[rune]
	terms = append(terms, first)
	for {
		r, _ := p.peek()
		if r != '|' {
			break
		}
		p.advance()
		next, err := p.parseConcat()
		if err != nil {
			return RegulaAST[rune]{}, err
		}
		terms = append(terms, next)
	}
	return foldUnion(terms), nil
}

func foldUnion(terms []RegulaAST[rune]) RegulaAST[rune] {
	if len(terms) == 1 {
		return terms[0]
	}
	acc := terms[0]
	for i := 1; i < len(terms); i++ {
		acc = acc.Or(terms[i])
	}
	return acc
}

func (p *regexParser) parseConcat() (RegulaAST[rune], error) {
	var terms []RegulaAST[rune]
	for {
		next, ok, err := p.parseRepeat()
		if err != nil {
			return RegulaAST[rune]{}, err
		}
		if !ok {
			break
		}
		terms = append(terms, next)
	}
	if len(terms) == 0 {
		return p.f.Literal(), nil
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	acc := terms[0]
	for i := 1; i < len(terms); i++ {
		acc = acc.Then(terms[i])
	}
	return acc, nil
}

func (p *regexParser) parseRepeat() (RegulaAST[rune], bool, error) {
	atom, ok, err := p.parseAtom()
	if err != nil || !ok {
		return RegulaAST[rune]{}, false, err
	}
	r, _ := p.peek()
	switch r {
	case '*':
		p.advance()
		return atom.Star(), true, nil
	case '+':
		p.advance()
		return atom.Plus(), true, nil
	case '?':
		p.advance()
		return atom.Optional(), true, nil
	default:
		return atom, true, nil
	}
}

func (p *regexParser) parseAtom() (RegulaAST[rune], bool, error) {
	if p.atEnd() {
		return RegulaAST[rune]{}, false, nil
	}
	r := p.advance()
	switch r {
	case '|', '*', '+', '?', ')':
		p.pos -= utf8.RuneLen(r)
		return RegulaAST[rune]{}, false, nil
	case ']':
		p.pos -= utf8.RuneLen(r)
		return RegulaAST[rune]{}, false, nil
	case '\\':
		ast, err := p.parseEscape()
		return ast, err == nil, err
	case '.':
		ast, err := p.parseDot()
		return ast, err == nil, err
	case '[':
		ast, err := p.parseClass()
		return ast, err == nil, err
	case '(':
		inner, err := p.parseUnion()
		if err != nil {
			return RegulaAST[rune]{}, false, err
		}
		if p.atEnd() || p.advance() != ')' {
			return RegulaAST[rune]{}, false, fmt.Errorf("RegexToRegula: unclosed ( at position %d", p.pos-1)
		}
		return inner, true, nil
	default:
		return p.f.Literal(r), true, nil
	}
}

func (p *regexParser) parseEscape() (RegulaAST[rune], error) {
	if p.pos >= len(p.s) {
		return RegulaAST[rune]{}, fmt.Errorf("RegexToRegula: unexpected end after \\")
	}
	r := p.advance()
	switch r {
	case 'd':
		return p.f.Class(p.f.Range('0', '9')), nil
	case 's':
		return p.f.Class(
			p.f.Range(' ', ' '),
			p.f.Range('\t', '\t'),
			p.f.Range('\n', '\n'),
			p.f.Range('\r', '\r'),
		), nil
	case 'n':
		return p.f.Literal('\n'), nil
	case 'r':
		return p.f.Literal('\r'), nil
	case 't':
		return p.f.Literal('\t'), nil
	case '.', '*', '+', '?', '|', '[', ']', '(', ')', '\\', '-', '^':
		return p.f.Literal(r), nil
	default:
		return p.f.Literal(r), nil
	}
}

func (p *regexParser) parseDot() (RegulaAST[rune], error) {
	if p.f.observationDomain == nil {
		return RegulaAST[rune]{}, fmt.Errorf("RegexToRegula: . requires factory with DiscreteDomain")
	}
	return p.f.NegatedClass(), nil
}

func (p *regexParser) parseClass() (RegulaAST[rune], error) {
	negated := false
	if p.pos < len(p.s) {
		r, _ := utf8.DecodeRuneInString(p.s[p.pos:])
		if r == '^' {
			negated = true
			p.advance()
		}
	}
	var ranges []CharRange[rune]
	for {
		if p.pos >= len(p.s) {
			return RegulaAST[rune]{}, fmt.Errorf("RegexToRegula: unclosed [")
		}
		r := p.advance()
		if r == ']' && len(ranges) > 0 {
			break
		}
		if r == ']' {
			ranges = append(ranges, p.f.Range(']', ']'))
			continue
		}
		lo := r
		if r == '\\' {
			esc, err := p.parseClassEscape()
			if err != nil {
				return RegulaAST[rune]{}, err
			}
			lo = esc
		}
		if p.pos < len(p.s) {
			next, size := utf8.DecodeRuneInString(p.s[p.pos:])
			if next == '-' && size > 0 {
				p.pos += size
				if p.pos >= len(p.s) {
					ranges = append(ranges, p.f.Range(lo, lo), p.f.Range('-', '-'))
					break
				}
				hiR := p.advance()
				var hi rune
				if hiR == '\\' {
					hi, _ = p.parseClassEscape()
				} else {
					hi = hiR
				}
				if lo > hi {
					return RegulaAST[rune]{}, fmt.Errorf("RegexToRegula: invalid range %q-%q", lo, hi)
				}
				ranges = append(ranges, p.f.Range(lo, hi))
				continue
			}
		}
		ranges = append(ranges, p.f.Range(lo, lo))
	}
	if len(ranges) == 0 {
		return RegulaAST[rune]{}, fmt.Errorf("RegexToRegula: empty class []")
	}
	if negated {
		return p.f.NegatedClass(ranges...), nil
	}
	return p.f.Class(ranges...), nil
}

func (p *regexParser) parseClassEscape() (rune, error) {
	if p.pos >= len(p.s) {
		return 0, fmt.Errorf("RegexToRegula: unexpected end in class escape")
	}
	r := p.advance()
	switch r {
	case 'd':
		return 0, fmt.Errorf("RegexToRegula: \\d in class not supported (use [0-9])")
	case 's':
		return 0, fmt.Errorf("RegexToRegula: \\s in class not supported")
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case '-', ']', '\\', '^':
		return r, nil
	default:
		return r, nil
	}
}
