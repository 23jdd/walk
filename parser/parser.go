package parser

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/23jdd/walk/executor"
)

// Parser is a recursive-descent parser for the supported Cypher subset (F-08).
type Parser struct {
	tk     *Tokenizer
	cur    Token
	ahead  *Token
	source string
	err    error
}

func NewParser(source string) *Parser {
	p := &Parser{tk: TokenizerFromString(source), source: source}
	p.advance()
	return p
}

// Parse parses one statement.
func Parse(source string) (*Query, error) {
	p := NewParser(source)
	q := p.parseQuery()
	if p.err != nil {
		return nil, p.err
	}
	return q, nil
}

func ParseFile(path string) (*Query, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(b))
}

func (p *Parser) advance() {
	if p.err != nil {
		return
	}
	if p.ahead != nil {
		p.cur = *p.ahead
		p.ahead = nil
		return
	}
	tok, err := p.tk.Next()
	if err != nil {
		p.err = err
		return
	}
	p.cur = tok
}

func (p *Parser) peek() Token {
	if p.ahead == nil && p.err == nil {
		tok, err := p.tk.Next()
		if err != nil {
			p.err = err
			return Token{Type: EOF}
		}
		p.ahead = &tok
	}
	if p.ahead == nil {
		return Token{Type: EOF}
	}
	return *p.ahead
}

func (p *Parser) fail(tok Token, msg string) {
	if p.err != nil {
		return
	}
	p.err = &ParseError{
		Span:   tok.Span,
		Msg:    msg,
		Source: p.source,
	}
}

func (p *Parser) expected(what string) {
	p.fail(p.cur, fmt.Sprintf("expected %s, got %s", what, p.describe(p.cur)))
}

func (p *Parser) describe(tok Token) string {
	switch tok.Type {
	case EOF:
		return "end of input"
	case Ident:
		return fmt.Sprintf("identifier %q", tok.Text)
	case Int, Float:
		return fmt.Sprintf("number %q", tok.Text)
	case String:
		return "string literal"
	default:
		return fmt.Sprintf("%q", tok.Text)
	}
}

func (p *Parser) expect(t TokenType) Token {
	if p.err != nil {
		return Token{}
	}
	if p.cur.Type != t {
		p.expected(tokenName(t))
		return Token{}
	}
	tok := p.cur
	p.advance()
	return tok
}

func (p *Parser) accept(t TokenType) (bool, Token) {
	if p.err != nil {
		return false, Token{}
	}
	if p.cur.Type != t {
		return false, Token{}
	}
	tok := p.cur
	p.advance()
	return true, tok
}

func (p *Parser) parseQuery() *Query {
	q := &Query{Span: p.cur.Span}
	if ok, _ := p.accept(Explain); ok {
		q.Explain = true
	}
	clauses := 0
	for p.err == nil && p.cur.Type != EOF {
		switch p.cur.Type {
		case Match:
			clauses++
			p.parseMatch(q)
		case Where:
			clauses++
			p.parseWhere(q)
		case Create:
			clauses++
			p.parseCreate(q)
		case Set:
			clauses++
			p.parseSet(q)
		case Remove:
			clauses++
			p.parseRemove(q)
		case Delete, Detach:
			clauses++
			p.parseDelete(q)
		case Return:
			clauses++
			p.parseReturn(q)
		case Semicolon:
			p.advance()
			if p.cur.Type != EOF {
				p.fail(p.cur, "unexpected token after ';'")
			}
		default:
			p.fail(p.cur, fmt.Sprintf("unexpected %s; expected a clause keyword", p.describe(p.cur)))
		}
	}
	if p.err != nil {
		return nil
	}
	if clauses == 0 {
		p.fail(p.cur, "empty query")
		return nil
	}
	q.Span.End = p.cur.Span.End
	return q
}

func (p *Parser) parseMatch(q *Query) {
	p.expect(Match)
	if p.err != nil {
		return
	}
	pats := p.parsePatternList()
	if p.err != nil {
		return
	}
	q.Match = append(q.Match, pats...)
	if p.cur.Type == Where {
		p.parseWhere(q)
	}
}

func (p *Parser) parseWhere(q *Query) {
	p.expect(Where)
	if p.err != nil {
		return
	}
	ex := p.parseExpr()
	if ex == nil {
		return
	}
	if q.Where == nil {
		q.Where = ex
	} else {
		q.Where = &BinaryExpr{Op: "AND", Left: q.Where, Right: ex, Span: q.Where.GetSpan().Merge(ex.GetSpan())}
	}
}

func (p *Parser) parseCreate(q *Query) {
	p.expect(Create)
	if p.err != nil {
		return
	}
	pats := p.parsePatternList()
	if p.err != nil {
		return
	}
	q.Create = append(q.Create, pats...)
}

func (p *Parser) parseSet(q *Query) {
	p.expect(Set)
	if p.err != nil {
		return
	}
	for p.err == nil {
		items := p.parseSetItem()
		if p.err != nil {
			return
		}
		q.Set = append(q.Set, items...)
		if ok, _ := p.accept(Comma); !ok {
			break
		}
	}
}

func (p *Parser) parseSetItem() []*SetItem {
	start := p.cur
	varTok := p.expect(Ident)
	if p.err != nil {
		return nil
	}
	varName := varTok.Text
	if ok, _ := p.accept(Dot); ok {
		propTok := p.expect(Ident)
		p.expect(Eq)
		if p.err != nil {
			return nil
		}
		val := p.parseExpr()
		return []*SetItem{{Var: varName, Prop: propTok.Text, Value: val, Span: Span{Start: start.Span.Start, End: p.cur.Span.End}}}
	}
	if ok, _ := p.accept(Eq); ok {
		val := p.parseExpr()
		_, isMap := val.(*MapExpr)
		return []*SetItem{{Var: varName, Value: val, Map: isMap, Span: Span{Start: start.Span.Start, End: p.cur.Span.End}}}
	}
	if ok, _ := p.accept(Plus); ok {
		p.expect(Eq)
		if p.err != nil {
			return nil
		}
		val := p.parseExpr()
		return []*SetItem{{Var: varName, Value: val, Map: true, Add: true, Span: Span{Start: start.Span.Start, End: p.cur.Span.End}}}
	}
	if ok, _ := p.accept(Colon); ok {
		var items []*SetItem
		for p.err == nil {
			label := p.expect(Ident)
			if p.err != nil {
				return nil
			}
			items = append(items, &SetItem{Var: varName, Label: label.Text, Span: Span{Start: start.Span.Start, End: p.cur.Span.End}})
			if next, _ := p.accept(Colon); !next {
				break
			}
		}
		return items
	}
	p.fail(p.cur, "expected '.property', '=', '+=', or ':Label' in SET")
	return nil
}

func (p *Parser) parseRemove(q *Query) {
	p.expect(Remove)
	if p.err != nil {
		return
	}
	for p.err == nil {
		start := p.cur
		varTok := p.expect(Ident)
		if p.err != nil {
			return
		}
		if ok, _ := p.accept(Dot); ok {
			prop := p.expect(Ident)
			q.Remove = append(q.Remove, &RemoveItem{Var: varTok.Text, Prop: prop.Text, Span: Span{Start: start.Span.Start, End: p.cur.Span.End}})
		} else if ok, _ := p.accept(Colon); ok {
			for p.err == nil {
				label := p.expect(Ident)
				if p.err != nil {
					return
				}
				q.Remove = append(q.Remove, &RemoveItem{Var: varTok.Text, Label: label.Text, Span: Span{Start: start.Span.Start, End: p.cur.Span.End}})
				if next, _ := p.accept(Colon); !next {
					break
				}
			}
		} else {
			p.fail(p.cur, "expected '.property' or ':Label' in REMOVE")
			return
		}
		if ok, _ := p.accept(Comma); !ok {
			break
		}
	}
}

func (p *Parser) parseDelete(q *Query) {
	if ok, _ := p.accept(Detach); ok {
		q.Detach = true
	}
	p.expect(Delete)
	if p.err != nil {
		return
	}
	for p.err == nil {
		start := p.cur
		v := p.expect(Ident)
		q.Delete = append(q.Delete, &DeleteItem{Var: v.Text, Span: Span{Start: start.Span.Start, End: p.cur.Span.End}})
		if ok, _ := p.accept(Comma); !ok {
			break
		}
	}
}

func (p *Parser) parseReturn(q *Query) {
	p.expect(Return)
	if p.err != nil {
		return
	}
	r := &ReturnClause{Span: p.cur.Span}
	if ok, _ := p.accept(Distinct); ok {
		r.Distinct = true
	}
	if ok, _ := p.accept(Star); ok {
		r.Star = true
	} else {
		for p.err == nil {
			start := p.cur
			ex := p.parseExpr()
			if p.err != nil {
				return
			}
			end := p.cur.Span.End
			if p.cur.Type == As {
				end = p.cur.Span.Start
			}
			item := &ReturnItem{Expr: ex, Span: Span{Start: start.Span.Start, End: end}}
			item.Text = p.sourceBetween(start.Span.Start, end)
			if item.Text == "" {
				item.Text = ExprText(ex)
			}
			if ok, _ := p.accept(As); ok {
				alias := p.expect(Ident)
				item.Alias = alias.Text
			}
			r.Items = append(r.Items, item)
			if ok, _ := p.accept(Comma); !ok {
				break
			}
		}
	}
	if p.err != nil {
		return
	}
	if p.cur.Type == Order {
		p.advance()
		p.expect(By)
		for p.err == nil {
			start := p.cur
			ex := p.parseExpr()
			if p.err != nil {
				return
			}
			item := &OrderItem{Expr: ex, Span: Span{Start: start.Span.Start, End: p.cur.Span.End}}
			if ok, _ := p.accept(Desc); ok {
				item.Desc = true
			} else {
				_, _ = p.accept(Asc)
			}
			r.OrderBy = append(r.OrderBy, item)
			if ok, _ := p.accept(Comma); !ok {
				break
			}
		}
	}
	if ok, _ := p.accept(Skip); ok {
		r.Skip = p.parseExpr()
	}
	if ok, _ := p.accept(Limit); ok {
		r.Limit = p.parseExpr()
	}
	if r.Star && len(r.Items) > 0 {
		p.fail(p.cur, "'*' cannot be combined with other RETURN items")
		return
	}
	q.Return = r
}

func (p *Parser) sourceBetween(start, end Position) string {
	runes := []rune(p.source)
	if start.Offset < 0 || end.Offset > len(runes) || start.Offset >= end.Offset {
		return ""
	}
	return strings.TrimSpace(string(runes[start.Offset:end.Offset]))
}

// ---------------------------------------------------------------------------
// Patterns
// ---------------------------------------------------------------------------

func (p *Parser) parsePatternList() []*Pattern {
	var out []*Pattern
	for p.err == nil {
		pat := p.parsePattern()
		if p.err != nil {
			return nil
		}
		out = append(out, pat)
		if ok, _ := p.accept(Comma); !ok {
			break
		}
	}
	return out
}

func (p *Parser) parsePattern() *Pattern {
	start := p.cur.Span.Start
	pat := &Pattern{Span: p.cur.Span}
	if p.cur.Type == Ident && p.peek().Type == Eq {
		pat.PathVar = p.cur.Text
		p.advance()
		p.advance()
	}
	if p.cur.Type == ShortestPath || (p.cur.Type == Ident && strings.EqualFold(p.cur.Text, "shortestPath")) {
		p.advance()
		p.expect(LParen)
		part := p.parsePathPart()
		p.expect(RParen)
		if p.err != nil {
			return nil
		}
		pat.Shortest = true
		pat.Parts = []*PathPart{part}
		pat.Span = Span{Start: start, End: p.cur.Span.End}
		return pat
	}
	part := p.parsePathPart()
	if p.err != nil {
		return nil
	}
	pat.Parts = []*PathPart{part}
	pat.Span = Span{Start: start, End: p.cur.Span.End}
	return pat
}

func (p *Parser) parsePathPart() *PathPart {
	start := p.cur.Span.Start
	part := &PathPart{}
	node := p.parseNodePattern()
	if p.err != nil {
		return nil
	}
	part.Nodes = append(part.Nodes, node)
	for p.err == nil && (p.cur.Type == Minus || p.cur.Type == Lt) {
		rel := p.parseRelPattern()
		if p.err != nil {
			return nil
		}
		n := p.parseNodePattern()
		if p.err != nil {
			return nil
		}
		part.Rels = append(part.Rels, rel)
		part.Nodes = append(part.Nodes, n)
	}
	part.Span = Span{Start: start, End: p.cur.Span.End}
	return part
}

func (p *Parser) parseNodePattern() *NodePattern {
	start := p.cur.Span.Start
	p.expect(LParen)
	if p.err != nil {
		return nil
	}
	n := &NodePattern{}
	if p.cur.Type == Ident {
		n.Var = p.cur.Text
		p.advance()
	}
	for p.err == nil {
		if ok, _ := p.accept(Colon); !ok {
			break
		}
		label := p.expect(Ident)
		if p.err != nil {
			return nil
		}
		n.Labels = append(n.Labels, label.Text)
	}
	if p.cur.Type == LBrace {
		n.Props = p.parseProps()
	}
	p.expect(RParen)
	if p.err != nil {
		return nil
	}
	n.Span = Span{Start: start, End: p.cur.Span.End}
	return n
}

func (p *Parser) parseRelPattern() *RelPattern {
	start := p.cur.Span.Start
	left, _ := p.accept(Lt)
	p.expect(Minus)
	if p.err != nil {
		return nil
	}
	rel := &RelPattern{Direction: Both, Min: 1}
	if ok, _ := p.accept(LBracket); ok {
		if p.cur.Type == Ident {
			rel.Var = p.cur.Text
			p.advance()
		}
		if ok, _ := p.accept(Colon); ok {
			for p.err == nil {
				t := p.expect(Ident)
				if p.err != nil {
					return nil
				}
				rel.Types = append(rel.Types, t.Text)
				if next, _ := p.accept(Pipe); !next {
					break
				}
			}
		}
		if ok, _ := p.accept(Star); ok {
			rel.VarLength = true
			rel.Min, rel.Max = 1, 0
			if p.cur.Type == Int {
				n, _ := strconv.Atoi(p.cur.Text)
				rel.Min, rel.Max = n, n
				p.advance()
				if p.cur.Type == DotDot {
					p.advance()
					rel.Max = 0
					if p.cur.Type == Int {
						m, _ := strconv.Atoi(p.cur.Text)
						rel.Max = m
						p.advance()
					}
				}
			} else if p.cur.Type == DotDot {
				p.advance()
				if p.cur.Type == Int {
					m, _ := strconv.Atoi(p.cur.Text)
					rel.Max = m
					p.advance()
				}
			}
		}
		if p.cur.Type == LBrace {
			rel.Props = p.parseProps()
		}
		p.expect(RBracket)
		if p.err != nil {
			return nil
		}
	}
	p.expect(Minus)
	if p.err != nil {
		return nil
	}
	right, _ := p.accept(Gt)
	if left && right {
		p.fail(p.cur, "relationship cannot be both incoming and outgoing ('<->')")
		return nil
	}
	if left {
		rel.Direction = In
	} else if right {
		rel.Direction = Out
	} else {
		rel.Direction = Both
	}
	rel.Span = Span{Start: start, End: p.cur.Span.End}
	return rel
}

func (p *Parser) parseProps() []*Prop {
	p.expect(LBrace)
	if p.err != nil {
		return nil
	}
	var props []*Prop
	for p.err == nil && p.cur.Type != RBrace {
		start := p.cur.Span.Start
		keyTok := p.cur
		if keyTok.Type == Ident || keyTok.Type == String {
			p.advance()
		} else {
			p.fail(p.cur, "expected property name")
			return nil
		}
		p.expect(Colon)
		if p.err != nil {
			return nil
		}
		val := p.parseExpr()
		if p.err != nil {
			return nil
		}
		props = append(props, &Prop{Key: keyTok.Text, Value: val, Span: Span{Start: start, End: p.cur.Span.End}})
		if ok, _ := p.accept(Comma); !ok {
			break
		}
	}
	p.expect(RBrace)
	if p.err != nil {
		return nil
	}
	return props
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

func (p *Parser) parseExpr() Expr {
	return p.parseOr()
}

func (p *Parser) parseOr() Expr {
	left := p.parseAnd()
	for p.err == nil && p.cur.Type == Or {
		op := p.cur
		p.advance()
		right := p.parseAnd()
		if p.err != nil {
			return nil
		}
		left = &BinaryExpr{Op: "OR", Left: left, Right: right, Span: Span{Start: op.Span.Start, End: right.GetSpan().End}}
	}
	return left
}

func (p *Parser) parseAnd() Expr {
	left := p.parseNot()
	for p.err == nil && p.cur.Type == And {
		op := p.cur
		p.advance()
		right := p.parseNot()
		if p.err != nil {
			return nil
		}
		left = &BinaryExpr{Op: "AND", Left: left, Right: right, Span: Span{Start: op.Span.Start, End: right.GetSpan().End}}
	}
	return left
}

func (p *Parser) parseNot() Expr {
	if p.cur.Type == Not {
		op := p.cur
		p.advance()
		ex := p.parseNot()
		if p.err != nil {
			return nil
		}
		return &UnaryExpr{Op: "NOT", Expr: ex, Span: Span{Start: op.Span.Start, End: ex.GetSpan().End}}
	}
	return p.parseComparison()
}

func (p *Parser) parseComparison() Expr {
	left := p.parseAdditive()
	for p.err == nil {
		var op string
		switch p.cur.Type {
		case Eq:
			op = "="
		case Neq:
			op = "<>"
		case Lt:
			op = "<"
		case Le:
			op = "<="
		case Gt:
			op = ">"
		case Ge:
			op = ">="
		case Starts:
			p.advance()
			p.expect(With)
			op = "STARTS WITH"
		case Ends:
			p.advance()
			p.expect(With)
			op = "ENDS WITH"
		case Contains:
			op = "CONTAINS"
		case Is:
			p.advance()
			not := false
			if ok, _ := p.accept(Not); ok {
				not = true
			}
			p.expect(Null)
			if p.err != nil {
				return nil
			}
			left = &IsNullExpr{Expr: left, Not: not, Span: Span{Start: left.GetSpan().Start, End: p.cur.Span.End}}
			continue
		default:
			return left
		}
		p.advance()
		right := p.parseAdditive()
		if p.err != nil {
			return nil
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right, Span: Span{Start: left.GetSpan().Start, End: right.GetSpan().End}}
	}
	return left
}

func (p *Parser) parseAdditive() Expr {
	left := p.parseMultiplicative()
	for p.err == nil && (p.cur.Type == Plus || p.cur.Type == Minus) {
		op := p.cur.Text
		p.advance()
		right := p.parseMultiplicative()
		if p.err != nil {
			return nil
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right, Span: Span{Start: left.GetSpan().Start, End: right.GetSpan().End}}
	}
	return left
}

func (p *Parser) parseMultiplicative() Expr {
	left := p.parseUnary()
	for p.err == nil && (p.cur.Type == Star || p.cur.Type == Slash || p.cur.Type == Percent) {
		op := p.cur.Text
		p.advance()
		right := p.parseUnary()
		if p.err != nil {
			return nil
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right, Span: Span{Start: left.GetSpan().Start, End: right.GetSpan().End}}
	}
	return left
}

func (p *Parser) parseUnary() Expr {
	if p.cur.Type == Minus || p.cur.Type == Plus {
		op := p.cur
		p.advance()
		ex := p.parseUnary()
		if p.err != nil {
			return nil
		}
		return &UnaryExpr{Op: op.Text, Expr: ex, Span: Span{Start: op.Span.Start, End: ex.GetSpan().End}}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() Expr {
	ex := p.parsePrimary()
	for p.err == nil && p.cur.Type == Dot {
		p.advance()
		prop := p.expect(Ident)
		if p.err != nil {
			return nil
		}
		span := Span{Start: ex.GetSpan().Start, End: prop.Span.End}
		ex = &PropertyExpr{Target: ex, Prop: prop.Text, Span: span}
	}
	return ex
}

func (p *Parser) parsePrimary() Expr {
	switch p.cur.Type {
	case Int:
		v, err := strconv.ParseInt(p.cur.Text, 10, 64)
		if err != nil {
			p.fail(p.cur, "integer literal out of range")
			return nil
		}
		ex := &LiteralExpr{Value: executor.IntValue(v), Span: p.cur.Span}
		p.advance()
		return ex
	case Float:
		v, err := strconv.ParseFloat(p.cur.Text, 64)
		if err != nil {
			p.fail(p.cur, "invalid float literal")
			return nil
		}
		ex := &LiteralExpr{Value: executor.FloatValue(v), Span: p.cur.Span}
		p.advance()
		return ex
	case String:
		ex := &LiteralExpr{Value: executor.StringValue(p.cur.Text), Span: p.cur.Span}
		p.advance()
		return ex
	case Null:
		ex := &LiteralExpr{Value: executor.NullValue(), Span: p.cur.Span}
		p.advance()
		return ex
	case True:
		ex := &LiteralExpr{Value: executor.BoolValue(true), Span: p.cur.Span}
		p.advance()
		return ex
	case False:
		ex := &LiteralExpr{Value: executor.BoolValue(false), Span: p.cur.Span}
		p.advance()
		return ex
	case Star:
		ex := &StarExpr{Span: p.cur.Span}
		p.advance()
		return ex
	case LParen:
		p.advance()
		ex := p.parseExpr()
		if p.err != nil {
			return nil
		}
		p.expect(RParen)
		if p.err != nil {
			return nil
		}
		return ex
	case LBrace:
		return p.parseMapExpr()
	case Ident, ShortestPath:
		name := p.cur.Text
		span := p.cur.Span
		p.advance()
		if p.cur.Type == LParen {
			return p.parseFunction(name, span)
		}
		if p.cur.Type == Dot {
			// handled by postfix loop
		}
		return &VariableExpr{Name: name, Span: span}
	default:
		p.fail(p.cur, fmt.Sprintf("expected an expression, got %s", p.describe(p.cur)))
		return nil
	}
}

func (p *Parser) parseFunction(name string, nameSpan Span) Expr {
	p.expect(LParen)
	if p.err != nil {
		return nil
	}
	fn := &FuncExpr{Name: name, Span: Span{Start: nameSpan.Start, End: p.cur.Span.End}}
	if ok, _ := p.accept(Star); ok {
		fn.Star = true
		p.expect(RParen)
		return fn
	}
	if ok, _ := p.accept(Distinct); ok {
		_ = ok // DISTINCT inside aggregates is accepted but not distinguished
	}
	for p.err == nil && p.cur.Type != RParen {
		arg := p.parseExpr()
		if p.err != nil {
			return nil
		}
		fn.Args = append(fn.Args, arg)
		if ok, _ := p.accept(Comma); !ok {
			break
		}
	}
	p.expect(RParen)
	if p.err != nil {
		return nil
	}
	if fn.Star && strings.ToUpper(fn.Name) != "COUNT" {
		p.fail(Token{Span: nameSpan}, "only count(*) supports '*'")
		return nil
	}
	return fn
}

func (p *Parser) parseMapExpr() Expr {
	start := p.cur.Span.Start
	props := p.parseProps()
	if p.err != nil {
		return nil
	}
	return &MapExpr{Entries: props, Span: Span{Start: start, End: p.cur.Span.End}}
}
