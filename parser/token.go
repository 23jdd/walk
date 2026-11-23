package parser

type Token struct {
	tp     TokenType
	span   Span
	source []rune
}

func newToken(tp TokenType, source []rune) Token {
	return Token{tp: tp, source: source}
}
func InvalidToken() Token {
	return Token{
		tp:     Invalid,
		source: nil,
	}
}
func MatchToken() Token {
	return Token{
		tp:     Match,
		source: []rune("Match"),
	}
}

func ReturnToken() Token {
	return Token{
		tp:     Return,
		source: []rune("Return"),
	}
}

func WhereToken() Token {
	return Token{
		tp:     Where,
		source: []rune("Where"),
	}
}
func IdentifyToken(name []rune) Token {
	return Token{
		tp:     Identify,
		source: name,
	}
}
func (t Token) WithSpan(span Span) Token {
	t.span = span
	return t
}
