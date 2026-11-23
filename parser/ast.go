package parser

import "github.com/23jdd/walk/executor"

// Position is a 1-based line/column and 0-based rune offset.
type Position struct {
	Offset int `json:"offset"`
	Line   int `json:"line"`
	Column int `json:"column"`
}

type Span struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

func (s Span) Merge(o Span) Span {
	if s.Start.Offset == 0 && s.End.Offset == 0 {
		return o
	}
	if o.Start.Offset < s.Start.Offset {
		s.Start = o.Start
	}
	if o.End.Offset > s.End.Offset {
		s.End = o.End
	}
	return s
}

// ParseError carries a precise source position (F-08 error diagnostics).
type ParseError struct {
	Span    Span
	Msg     string
	Source  string
}

func (e *ParseError) Error() string {
	return formatDiagnostic(e.Source, e.Span, e.Msg)
}

// Query is the parsed form of one Cypher statement.
type Query struct {
	Explain bool
	Match   []*Pattern
	Where   Expr
	Create  []*Pattern
	Set     []*SetItem
	Remove  []*RemoveItem
	Delete  []*DeleteItem
	Detach  bool
	Return  *ReturnClause
	Span    Span
}

// HasWrites reports whether the query needs an Update transaction.
func (q *Query) HasWrites() bool {
	return len(q.Create) > 0 || len(q.Set) > 0 || len(q.Remove) > 0 || len(q.Delete) > 0
}

type Pattern struct {
	PathVar  string
	Parts    []*PathPart
	Shortest bool
	Span     Span
}

type PathPart struct {
	Nodes []*NodePattern
	Rels  []*RelPattern
	Span  Span
}

type Direction uint8

const (
	Both Direction = iota
	Out
	In
)

func (d Direction) String() string {
	switch d {
	case Out:
		return "out"
	case In:
		return "in"
	default:
		return "both"
	}
}

type NodePattern struct {
	Var    string
	Labels []string
	Props  []*Prop
	Span   Span
}

type RelPattern struct {
	Var       string
	Types     []string
	Props     []*Prop
	Direction Direction
	VarLength bool
	Min       int
	Max       int // 0 means "use default/system max"
	Span      Span
}

type Prop struct {
	Key   string
	Value Expr
	Span  Span
}

type SetItem struct {
	Var   string
	Prop  string
	Label string
	Value Expr
	Map   bool
	Add   bool
	Span  Span
}

type RemoveItem struct {
	Var   string
	Prop  string
	Label string
	Span  Span
}

type DeleteItem struct {
	Var  string
	Span Span
}

type ReturnClause struct {
	Distinct bool
	Star     bool
	Items    []*ReturnItem
	OrderBy  []*OrderItem
	Skip     Expr
	Limit    Expr
	Span     Span
}

type ReturnItem struct {
	Expr  Expr
	Alias string
	Star  bool
	Text  string
	Span  Span
}

type OrderItem struct {
	Expr Expr
	Desc bool
	Span Span
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

type Expr interface {
	exprNode()
	GetSpan() Span
}

type LiteralExpr struct {
	Value executor.Value
	Span  Span
}

func (*LiteralExpr) exprNode()     {}
func (e *LiteralExpr) GetSpan() Span { return e.Span }

type VariableExpr struct {
	Name string
	Span Span
}

func (*VariableExpr) exprNode()     {}
func (e *VariableExpr) GetSpan() Span { return e.Span }

type PropertyExpr struct {
	Target Expr
	Prop   string
	Span   Span
}

func (*PropertyExpr) exprNode()     {}
func (e *PropertyExpr) GetSpan() Span { return e.Span }

type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
	Span  Span
}

func (*BinaryExpr) exprNode()     {}
func (e *BinaryExpr) GetSpan() Span { return e.Span }

type UnaryExpr struct {
	Op   string // "-", "+", "NOT"
	Expr Expr
	Span Span
}

func (*UnaryExpr) exprNode()     {}
func (e *UnaryExpr) GetSpan() Span { return e.Span }

type IsNullExpr struct {
	Expr Expr
	Not  bool
	Span Span
}

func (*IsNullExpr) exprNode()     {}
func (e *IsNullExpr) GetSpan() Span { return e.Span }

type FuncExpr struct {
	Name string
	Args []Expr
	Star bool
	Span Span
}

func (*FuncExpr) exprNode()     {}
func (e *FuncExpr) GetSpan() Span { return e.Span }

type MapExpr struct {
	Entries []*Prop
	Span    Span
}

func (*MapExpr) exprNode()     {}
func (e *MapExpr) GetSpan() Span { return e.Span }

// StarExpr only appears inside count(*) and RETURN *.
type StarExpr struct {
	Span Span
}

func (*StarExpr) exprNode()     {}
func (e *StarExpr) GetSpan() Span { return e.Span }

// ExprText returns the canonical source-ish text used as a default column
// name (e.g. "p.name", "count(*)").
func ExprText(e Expr) string {
	switch x := e.(type) {
	case *LiteralExpr:
		b, _ := x.Value.MarshalJSON()
		return string(b)
	case *VariableExpr:
		return x.Name
	case *PropertyExpr:
		return ExprText(x.Target) + "." + x.Prop
	case *BinaryExpr:
		return "(" + ExprText(x.Left) + " " + x.Op + " " + ExprText(x.Right) + ")"
	case *UnaryExpr:
		return x.Op + " " + ExprText(x.Expr)
	case *IsNullExpr:
		if x.Not {
			return ExprText(x.Expr) + " IS NOT NULL"
		}
		return ExprText(x.Expr) + " IS NULL"
	case *FuncExpr:
		if x.Star {
			return x.Name + "(*)"
		}
		parts := make([]string, len(x.Args))
		for i, a := range x.Args {
			parts[i] = ExprText(a)
		}
		return x.Name + "(" + joinComma(parts) + ")"
	case *MapExpr:
		return "{}"
	case *StarExpr:
		return "*"
	}
	return "?"
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
