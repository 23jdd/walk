package parser

type Position struct {
	Offset int
	Line   int
	Column int
}
type Span struct {
	Start, End Position
}
