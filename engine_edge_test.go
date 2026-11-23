package walk

import (
	"testing"

	"github.com/23jdd/walk/executor"
)

func TestAdditionalQueryForms(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:A {name: 'a', n: 1}), (b:B {name: 'b', n: 2}), (c:B {name: 'c'}), (a)-[:T]->(b), (a)-[:U]->(b), (a)-[:T]->(c), (b)-[:T]->(c)`)

	res := mustQuery(t, db, `MATCH (b:B {name: 'b'})<--(a:A) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(2) {
		t.Fatalf("incoming rows=%#v", res.Rows)
	}

	res = mustQuery(t, db, `MATCH (a:A)--(b) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(3) {
		t.Fatalf("undirected count=%v", res.Rows[0][0])
	}

	res = mustQuery(t, db, `MATCH (a:A)-[:T|U]->(b) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(3) {
		t.Fatalf("multi-type count=%v", res.Rows[0][0])
	}

	res = mustQuery(t, db, `MATCH (a:A:B) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(0) {
		t.Fatalf("multi-label AND count=%v", res.Rows[0][0])
	}

	res = mustQuery(t, db, `MATCH (a:A) RETURN *`)
	if len(res.Columns) != 1 || res.Columns[0] != "a" || len(res.Rows) != 1 {
		t.Fatalf("return star columns=%v rows=%#v", res.Columns, res.Rows)
	}

	res = mustQuery(t, db, `MATCH (a:A)-[r:T]->(b) RETURN count(r) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(2) {
		t.Fatalf("count(edge)=%v", res.Rows[0][0])
	}

	res = mustQuery(t, db, `MATCH (a:A) WHERE a.name CONTAINS 'a' RETURN a.name AS name`)
	if len(res.Rows) != 1 || scalar(t, res.Rows[0][0]) != "a" {
		t.Fatalf("contains rows=%#v", res.Rows)
	}

	res = mustQuery(t, db, `MATCH (a:A) SET a = {} RETURN a.name AS name`)
	if len(res.Rows) != 1 || !res.Rows[0][0].(executor.Value).IsNull() {
		t.Fatalf("set map clear rows=%#v", res.Rows)
	}

	res = mustQuery(t, db, `MATCH ()-[r:U]->() DELETE r`)
	if res.Summary == nil || res.Summary.EdgesDeleted != 1 {
		t.Fatalf("delete edge summary=%+v", res.Summary)
	}

	res = mustQuery(t, db, `MATCH (a:A) DETACH DELETE a`)
	if res.Summary == nil || res.Summary.NodesDeleted != 1 {
		t.Fatalf("detach summary=%+v", res.Summary)
	}
	res = mustQuery(t, db, `MATCH (n) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(2) {
		t.Fatalf("after detach count=%v", res.Rows[0][0])
	}
}
