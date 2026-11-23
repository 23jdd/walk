package parser

type TokenType uint8

const (
	Invalid TokenType = iota
	EOF
	Ident
	Int
	Float
	String

	LParen
	RParen
	LBracket
	RBracket
	LBrace
	RBrace
	Comma
	Colon
	Dot
	DotDot
	Semicolon
	Pipe
	Plus
	Minus
	Star
	Slash
	Percent
	Eq
	Neq
	Lt
	Le
	Gt
	Ge

	Match
	Where
	Return
	Create
	Delete
	Detach
	Set
	Remove
	As
	Distinct
	Order
	By
	Asc
	Desc
	Skip
	Limit
	And
	Or
	Not
	Is
	Null
	True
	False
	Starts
	Ends
	Contains
	Explain
	Optional
	With
	Unwind
	ShortestPath
)

var keywords = map[string]TokenType{
	"MATCH":        Match,
	"WHERE":        Where,
	"RETURN":       Return,
	"CREATE":       Create,
	"DELETE":       Delete,
	"DETACH":       Detach,
	"SET":          Set,
	"REMOVE":       Remove,
	"AS":           As,
	"DISTINCT":     Distinct,
	"ORDER":        Order,
	"BY":           By,
	"ASC":          Asc,
	"DESC":         Desc,
	"DESCENDING":   Desc,
	"SKIP":         Skip,
	"LIMIT":        Limit,
	"AND":          And,
	"OR":           Or,
	"NOT":          Not,
	"IS":           Is,
	"NULL":         Null,
	"TRUE":         True,
	"FALSE":        False,
	"STARTS":       Starts,
	"ENDS":         Ends,
	"CONTAINS":     Contains,
	"EXPLAIN":      Explain,
	"OPTIONAL":     Optional,
	"WITH":         With,
	"UNWIND":       Unwind,
	"SHORTESTPATH": ShortestPath,
}

type Token struct {
	Type TokenType
	Text string
	Span Span
}

func (t Token) String() string { return t.Text }

var tokenNames = map[TokenType]string{
	EOF: "end of input", Ident: "identifier", Int: "integer", Float: "float", String: "string",
	LParen: "(", RParen: ")", LBracket: "[", RBracket: "]", LBrace: "{", RBrace: "}",
	Comma: ",", Colon: ":", Dot: ".", DotDot: "..", Semicolon: ";", Pipe: "|",
	Plus: "+", Minus: "-", Star: "*", Slash: "/", Percent: "%",
	Eq: "=", Neq: "<>", Lt: "<", Le: "<=", Gt: ">", Ge: ">=",
	Match: "MATCH", Where: "WHERE", Return: "RETURN", Create: "CREATE", Delete: "DELETE",
	Detach: "DETACH", Set: "SET", Remove: "REMOVE", As: "AS", Distinct: "DISTINCT",
	Order: "ORDER", By: "BY", Asc: "ASC", Desc: "DESC", Skip: "SKIP", Limit: "LIMIT",
	And: "AND", Or: "OR", Not: "NOT", Is: "IS", Null: "NULL", True: "TRUE", False: "FALSE",
	Starts: "STARTS", Ends: "ENDS", Contains: "CONTAINS", Explain: "EXPLAIN",
}

func tokenName(t TokenType) string {
	if s, ok := tokenNames[t]; ok {
		return s
	}
	return "token"
}
