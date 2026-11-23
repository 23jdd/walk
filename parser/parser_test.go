package parser

import (
	"strings"
	"testing"
)

func TestParsePatternAndClauses(t *testing.T) {
	q, err := Parse(`MATCH (a:Person:Actor {name: "Alice"})-[:KNOWS*1..3]->(b)<-[r:LIKES]-(c)
		WHERE a.age > 30 AND NOT b.name IS NULL
		RETURN DISTINCT a.name AS name, count(*) AS c
		ORDER BY name DESC, c
		SKIP 1 LIMIT 5`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(q.Match) != 1 || len(q.Match[0].Parts) != 1 {
		t.Fatalf("match=%#v", q.Match)
	}
	part := q.Match[0].Parts[0]
	if len(part.Nodes) != 3 || len(part.Rels) != 2 {
		t.Fatalf("part nodes=%d rels=%d", len(part.Nodes), len(part.Rels))
	}
	if part.Nodes[0].Var != "a" || len(part.Nodes[0].Labels) != 2 {
		t.Fatalf("node0=%#v", part.Nodes[0])
	}
	if part.Rels[0].Direction != Out || !part.Rels[0].VarLength || part.Rels[0].Min != 1 || part.Rels[0].Max != 3 {
		t.Fatalf("rel0=%#v", part.Rels[0])
	}
	if part.Rels[1].Direction != In || part.Rels[1].Var != "r" || part.Rels[1].Types[0] != "LIKES" {
		t.Fatalf("rel1=%#v", part.Rels[1])
	}
	if q.Where == nil {
		t.Fatalf("missing where")
	}
	if q.Return == nil || !q.Return.Distinct || len(q.Return.Items) != 2 || len(q.Return.OrderBy) != 2 {
		t.Fatalf("return=%#v", q.Return)
	}
	if q.Return.Items[0].Alias != "name" || q.Return.Items[0].Text != "a.name" {
		t.Fatalf("item0=%#v", q.Return.Items[0])
	}
	if q.Return.Skip == nil || q.Return.Limit == nil {
		t.Fatalf("skip/limit missing")
	}
}

func TestParseUndirectedAndBackticks(t *testing.T) {
	q, err := Parse("MATCH (`weird var`:Label)-->(x) RETURN `weird var`")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	part := q.Match[0].Parts[0]
	if part.Nodes[0].Var != "weird var" || part.Rels[0].Direction != Out {
		t.Fatalf("part=%#v", part)
	}
}

func TestParseShortestPath(t *testing.T) {
	q, err := Parse(`MATCH p = shortestPath((a:Person)-[:KNOWS*..4]->(b:Person)) RETURN length(p)`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pat := q.Match[0]
	if !pat.Shortest || pat.PathVar != "p" {
		t.Fatalf("pattern=%#v", pat)
	}
	if pat.Parts[0].Rels[0].Max != 4 {
		t.Fatalf("max=%d", pat.Parts[0].Rels[0].Max)
	}
}

func TestParseSetRemoveDelete(t *testing.T) {
	q, err := Parse(`MATCH (n:Person) SET n.age = 40, n = {a: 1}, n += {b: 2}, n:Admin REMOVE n.age, n:Old DELETE n`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(q.Set) != 4 {
		t.Fatalf("set items=%d: %#v", len(q.Set), q.Set)
	}
	if len(q.Remove) != 2 || len(q.Delete) != 1 {
		t.Fatalf("remove=%d delete=%d", len(q.Remove), len(q.Delete))
	}
	q, err = Parse(`MATCH (n) DETACH DELETE n`)
	if err != nil || !q.Detach {
		t.Fatalf("detach parse: %v %#v", err, q)
	}
}

func TestParsePrecedence(t *testing.T) {
	q, err := Parse(`MATCH (n) WHERE n.a = 1 OR n.b = 2 AND n.c = 3 RETURN n`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	or, ok := q.Where.(*BinaryExpr)
	if !ok || or.Op != "OR" {
		t.Fatalf("where=%T %#v", q.Where, q.Where)
	}
	if and, ok := or.Right.(*BinaryExpr); !ok || and.Op != "AND" {
		t.Fatalf("right=%T", or.Right)
	}
}

func TestParseErrorPosition(t *testing.T) {
	_, err := Parse("MATCH (n\nRETURN n")
	if err == nil {
		t.Fatalf("expected error")
	}
	pe, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("error type %T: %v", err, err)
	}
	if pe.Span.Start.Line != 2 && pe.Span.Start.Line != 1 {
		t.Fatalf("line=%d", pe.Span.Start.Line)
	}
	msg := err.Error()
	if !strings.Contains(msg, "line ") || !strings.Contains(msg, "^") {
		t.Fatalf("diagnostic=%q", msg)
	}
}

func TestParseEmptyAndTrailing(t *testing.T) {
	if _, err := Parse(""); err == nil {
		t.Fatalf("empty query must fail")
	}
	if _, err := Parse("MATCH (n) RETURN n; garbage"); err == nil {
		t.Fatalf("trailing token must fail")
	}
	if _, err := Parse("MATCH (n) RETURN n;"); err != nil {
		t.Fatalf("trailing semicolon should be accepted: %v", err)
	}
}

func TestTokenizeStringEscapesAndComments(t *testing.T) {
	tk := TokenizerFromString("// comment\nMATCH (n {a: 'x\\n\\u0041', b: \"q\\\"q\"}) /* c */ RETURN n")
	var types []TokenType
	var stringsSeen []string
	for {
		tok, err := tk.Next()
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		types = append(types, tok.Type)
		if tok.Type == String {
			stringsSeen = append(stringsSeen, tok.Text)
		}
		if tok.Type == EOF {
			break
		}
	}
	if len(stringsSeen) != 2 || stringsSeen[0] != "x\nA" || stringsSeen[1] != `q"q` {
		t.Fatalf("strings=%q", stringsSeen)
	}
	if types[0] != Match {
		t.Fatalf("first token=%v", types[0])
	}
}
