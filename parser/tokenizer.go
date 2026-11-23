package parser

import (
	"fmt"
	"os"
	"strings"
)

// Tokenizer is the hand-written Cypher lexer (F-08).
type Tokenizer struct {
	source []rune
	pos    int
	line   int
	col    int
}

func TokenizerFromPath(path string) (*Tokenizer, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return TokenizerFromString(string(content)), nil
}

func TokenizerFromString(content string) *Tokenizer {
	// Strip a UTF-8 BOM if present.
	content = strings.TrimPrefix(content, "\ufeff")
	return &Tokenizer{source: []rune(content), line: 1, col: 1}
}

func (tk *Tokenizer) position() Position {
	return Position{Offset: tk.pos, Line: tk.line, Column: tk.col}
}

func (tk *Tokenizer) peekRune(n int) rune {
	if tk.pos+n < len(tk.source) {
		return tk.source[tk.pos+n]
	}
	return 0
}

func (tk *Tokenizer) advance() rune {
	if tk.pos >= len(tk.source) {
		return 0
	}
	r := tk.source[tk.pos]
	tk.pos++
	if r == '\n' {
		tk.line++
		tk.col = 1
	} else {
		tk.col++
	}
	return r
}

func (tk *Tokenizer) skipSpace() error {
	for tk.pos < len(tk.source) {
		r := tk.peekRune(0)
		switch r {
		case ' ', '\t', '\r', '\n', '\f':
			tk.advance()
		case '/':
			if tk.peekRune(1) == '/' {
				for tk.pos < len(tk.source) && tk.peekRune(0) != '\n' {
					tk.advance()
				}
			} else if tk.peekRune(1) == '*' {
				start := tk.position()
				tk.advance()
				tk.advance()
				closed := false
				for tk.pos < len(tk.source) {
					if tk.peekRune(0) == '*' && tk.peekRune(1) == '/' {
						tk.advance()
						tk.advance()
						closed = true
						break
					}
					tk.advance()
				}
				if !closed {
					return &ParseError{Span: Span{Start: start, End: tk.position()}, Msg: "unterminated block comment", Source: tk.Source()}
				}
			} else {
				return nil
			}
		default:
			return nil
		}
	}
	return nil
}

func (tk *Tokenizer) Source() string { return string(tk.source) }

// Next returns the next token. The tokenizer is not goroutine-safe.
func (tk *Tokenizer) Next() (Token, error) {
	if err := tk.skipSpace(); err != nil {
		return Token{}, err
	}
	start := tk.position()
	if tk.pos >= len(tk.source) {
		return Token{Type: EOF, Text: "", Span: Span{Start: start, End: start}}, nil
	}
	r := tk.peekRune(0)
	mk := func(t TokenType, text string, consume int) Token {
		for i := 0; i < consume; i++ {
			tk.advance()
		}
		return Token{Type: t, Text: text, Span: Span{Start: start, End: tk.position()}}
	}
	switch r {
	case '(':
		return mk(LParen, "(", 1), nil
	case ')':
		return mk(RParen, ")", 1), nil
	case '[':
		return mk(LBracket, "[", 1), nil
	case ']':
		return mk(RBracket, "]", 1), nil
	case '{':
		return mk(LBrace, "{", 1), nil
	case '}':
		return mk(RBrace, "}", 1), nil
	case ',':
		return mk(Comma, ",", 1), nil
	case ':':
		return mk(Colon, ":", 1), nil
	case ';':
		return mk(Semicolon, ";", 1), nil
	case '|':
		return mk(Pipe, "|", 1), nil
	case '+':
		return mk(Plus, "+", 1), nil
	case '-':
		return mk(Minus, "-", 1), nil
	case '*':
		return mk(Star, "*", 1), nil
	case '/':
		return mk(Slash, "/", 1), nil
	case '%':
		return mk(Percent, "%", 1), nil
	case '=':
		if tk.peekRune(1) == '~' {
			return Token{}, tk.err(start, "regex operator '=~' is not supported")
		}
		return mk(Eq, "=", 1), nil
	case '<':
		if tk.peekRune(1) == '=' {
			return mk(Le, "<=", 2), nil
		}
		if tk.peekRune(1) == '>' {
			return mk(Neq, "<>", 2), nil
		}
		return mk(Lt, "<", 1), nil
	case '>':
		if tk.peekRune(1) == '=' {
			return mk(Ge, ">=", 2), nil
		}
		return mk(Gt, ">", 1), nil
	case '!':
		if tk.peekRune(1) == '=' {
			return mk(Neq, "!=", 2), nil
		}
		return Token{}, tk.err(start, "unexpected character '!'")
	case '.':
		if tk.peekRune(1) == '.' {
			return mk(DotDot, "..", 2), nil
		}
		return mk(Dot, ".", 1), nil
	case '\'', '"':
		return tk.scanString(r, start)
	case '`':
		return tk.scanQuotedIdent(start)
	}
	if r >= '0' && r <= '9' {
		return tk.scanNumber(start)
	}
	if isIdentStart(r) {
		return tk.scanIdent(start)
	}
	return Token{}, tk.err(start, fmt.Sprintf("unexpected character %q", r))
}

func (tk *Tokenizer) scanIdent(start Position) (Token, error) {
	begin := tk.pos
	for tk.pos < len(tk.source) && isIdentPart(tk.peekRune(0)) {
		tk.advance()
	}
	text := string(tk.source[begin:tk.pos])
	span := Span{Start: start, End: tk.position()}
	if tp, ok := keywords[strings.ToUpper(text)]; ok {
		return Token{Type: tp, Text: text, Span: span}, nil
	}
	return Token{Type: Ident, Text: text, Span: span}, nil
}

func (tk *Tokenizer) scanQuotedIdent(start Position) (Token, error) {
	tk.advance() // `
	var sb strings.Builder
	for tk.pos < len(tk.source) {
		r := tk.advance()
		if r == '`' {
			if tk.peekRune(0) == '`' {
				tk.advance()
				sb.WriteRune('`')
				continue
			}
			return Token{Type: Ident, Text: sb.String(), Span: Span{Start: start, End: tk.position()}}, nil
		}
		sb.WriteRune(r)
	}
	return Token{}, tk.err(start, "unterminated backtick identifier")
}

func (tk *Tokenizer) scanNumber(start Position) (Token, error) {
	begin := tk.pos
	isFloat := false
	for tk.pos < len(tk.source) && tk.peekRune(0) >= '0' && tk.peekRune(0) <= '9' {
		tk.advance()
	}
	if tk.peekRune(0) == '.' && tk.peekRune(1) >= '0' && tk.peekRune(1) <= '9' {
		isFloat = true
		tk.advance()
		for tk.pos < len(tk.source) && tk.peekRune(0) >= '0' && tk.peekRune(0) <= '9' {
			tk.advance()
		}
	}
	if tk.peekRune(0) == 'e' || tk.peekRune(0) == 'E' {
		isFloat = true
		tk.advance()
		if tk.peekRune(0) == '+' || tk.peekRune(0) == '-' {
			tk.advance()
		}
		digits := false
		for tk.pos < len(tk.source) && tk.peekRune(0) >= '0' && tk.peekRune(0) <= '9' {
			tk.advance()
			digits = true
		}
		if !digits {
			return Token{}, tk.err(start, "malformed exponent")
		}
	}
	text := string(tk.source[begin:tk.pos])
	span := Span{Start: start, End: tk.position()}
	if isFloat {
		return Token{Type: Float, Text: text, Span: span}, nil
	}
	return Token{Type: Int, Text: text, Span: span}, nil
}

func (tk *Tokenizer) scanString(quote rune, start Position) (Token, error) {
	tk.advance()
	var sb strings.Builder
	for tk.pos < len(tk.source) {
		r := tk.advance()
		if r == quote {
			if tk.peekRune(0) == quote {
				tk.advance()
				sb.WriteRune(quote)
				continue
			}
			return Token{Type: String, Text: sb.String(), Span: Span{Start: start, End: tk.position()}}, nil
		}
		if r == '\\' {
			if tk.pos >= len(tk.source) {
				break
			}
			e := tk.advance()
			switch e {
			case 'n':
				sb.WriteRune('\n')
			case 'r':
				sb.WriteRune('\r')
			case 't':
				sb.WriteRune('\t')
			case '\\':
				sb.WriteRune('\\')
			case '\'':
				sb.WriteRune('\'')
			case '"':
				sb.WriteRune('"')
			case 'u':
				if tk.pos+4 <= len(tk.source) {
					hex := string(tk.source[tk.pos : tk.pos+4])
					var v int
					if _, err := fmt.Sscanf(hex, "%04x", &v); err == nil {
						sb.WriteRune(rune(v))
						for i := 0; i < 4; i++ {
							tk.advance()
						}
						continue
					}
				}
				sb.WriteRune('u')
			default:
				sb.WriteRune(e)
			}
			continue
		}
		sb.WriteRune(r)
	}
	return Token{}, tk.err(start, "unterminated string literal")
}

func (tk *Tokenizer) err(pos Position, msg string) error {
	return &ParseError{Span: Span{Start: pos, End: tk.position()}, Msg: msg, Source: tk.Source()}
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}
