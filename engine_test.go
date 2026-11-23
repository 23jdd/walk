package walk

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/23jdd/walk/executor"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustQuery(t *testing.T, db *DB, q string) *Result {
	t.Helper()
	res, err := db.Query(context.Background(), q, QueryOptions{})
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return res
}

func scalar(t *testing.T, v any) any {
	t.Helper()
	if val, ok := v.(executor.Value); ok {
		return val.Any()
	}
	return v
}

func TestBasicCreateMatchReturn(t *testing.T) {
	db := newTestDB(t)
	res := mustQuery(t, db, `CREATE (a:Person {name: "Alice", age: 39}), (b:Person {name: "Bob", age: 27}), (a)-[:KNOWS {since: 2020}]->(b)`)
	if res.Summary == nil || res.Summary.NodesCreated != 2 || res.Summary.EdgesCreated != 1 {
		t.Fatalf("unexpected summary: %+v", res.Summary)
	}
	res = mustQuery(t, db, `MATCH (n:Person) RETURN n.name AS name, n.age AS age ORDER BY name`)
	if len(res.Rows) != 2 {
		t.Fatalf("rows=%d want 2: %#v", len(res.Rows), res.Rows)
	}
	if got := scalar(t, res.Rows[0][0]); got != "Alice" {
		t.Fatalf("first name=%v", got)
	}
	if got := scalar(t, res.Rows[0][1]); got != int64(39) {
		t.Fatalf("first age=%v (%T)", got, got)
	}
}

func TestMatchRelationshipReturn(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "Alice"}), (b:Person {name: "Bob"}), (a)-[:KNOWS {since: 2020}]->(b)`)
	res := mustQuery(t, db, `MATCH (a:Person)-[r:KNOWS]->(b) RETURN a.name AS a, b.name AS b, r.since AS since`)
	if len(res.Rows) != 1 {
		t.Fatalf("rows=%d: %#v", len(res.Rows), res.Rows)
	}
	if scalar(t, res.Rows[0][0]) != "Alice" || scalar(t, res.Rows[0][1]) != "Bob" || scalar(t, res.Rows[0][2]) != int64(2020) {
		t.Fatalf("bad row: %#v", res.Rows[0])
	}
}

func TestWhereAndNull(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "Alice", age: 39}), (b:Person {name: "Bob"}), (c:Person {name: "Carol", age: 20})`)
	res := mustQuery(t, db, `MATCH (n:Person) WHERE n.age > 30 RETURN n.name AS name`)
	if len(res.Rows) != 1 || scalar(t, res.Rows[0][0]) != "Alice" {
		t.Fatalf("rows=%#v", res.Rows)
	}
	res = mustQuery(t, db, `MATCH (n:Person) WHERE n.age IS NULL RETURN n.name AS name`)
	if len(res.Rows) != 1 || scalar(t, res.Rows[0][0]) != "Bob" {
		t.Fatalf("null rows=%#v", res.Rows)
	}
}

func TestVarLenAndShortest(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "A"}), (b:Person {name: "B"}), (c:Person {name: "C"}), (a)-[:KNOWS]->(b), (b)-[:KNOWS]->(c)`)
	res := mustQuery(t, db, `MATCH (a:Person {name: "A"})-[:KNOWS*1..2]->(x) RETURN x.name AS name ORDER BY name`)
	if len(res.Rows) != 2 {
		t.Fatalf("varlen rows=%d: %#v", len(res.Rows), res.Rows)
	}
	res = mustQuery(t, db, `MATCH p = shortestPath((a:Person {name: "A"})-[:KNOWS*..3]->(b:Person {name: "C"})) RETURN length(p) AS len`)
	if len(res.Rows) != 1 || scalar(t, res.Rows[0][0]) != int64(2) {
		t.Fatalf("shortest rows=%#v", res.Rows)
	}
}

func TestAggregateOrderDistinct(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "Alice", age: 39}), (b:Person {name: "Bob", age: 27}), (c:Person {name: "Carol", age: 39})`)
	res := mustQuery(t, db, `MATCH (n:Person) RETURN count(*) AS c, avg(n.age) AS avg, min(n.age) AS min, max(n.age) AS max`)
	if len(res.Rows) != 1 {
		t.Fatalf("agg rows=%#v", res.Rows)
	}
	if scalar(t, res.Rows[0][0]) != int64(3) {
		t.Fatalf("count=%v", res.Rows[0][0])
	}
	avg, ok := scalar(t, res.Rows[0][1]).(float64)
	if !ok || avg < 34.9 || avg > 35.1 {
		t.Fatalf("avg=%v (%T)", res.Rows[0][1], res.Rows[0][1])
	}
	res = mustQuery(t, db, `MATCH (n:Person) RETURN DISTINCT n.age AS age ORDER BY age DESC`)
	if len(res.Rows) != 2 || scalar(t, res.Rows[0][0]) != int64(39) {
		t.Fatalf("distinct rows=%#v", res.Rows)
	}
	res = mustQuery(t, db, `MATCH (n:Person) RETURN n.age AS age ORDER BY age DESC SKIP 1 LIMIT 1`)
	if len(res.Rows) != 1 || scalar(t, res.Rows[0][0]) != int64(39) {
		t.Fatalf("skip/limit rows=%#v", res.Rows)
	}
}

func TestSetRemoveDelete(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "Alice", age: 39})`)
	res := mustQuery(t, db, `MATCH (a:Person {name: "Alice"}) SET a.age = 40, a:Admin RETURN a.age AS age`)
	if len(res.Rows) != 1 || scalar(t, res.Rows[0][0]) != int64(40) {
		t.Fatalf("set rows=%#v summary=%+v", res.Rows, res.Summary)
	}
	res = mustQuery(t, db, `MATCH (a:Admin) RETURN a.name AS name`)
	if len(res.Rows) != 1 {
		t.Fatalf("label rows=%#v", res.Rows)
	}
	mustQuery(t, db, `MATCH (a:Admin) REMOVE a.age`)
	res = mustQuery(t, db, `MATCH (a:Admin) RETURN a.age AS age`)
	if len(res.Rows) != 1 || !res.Rows[0][0].(executor.Value).IsNull() {
		t.Fatalf("remove rows=%#v", res.Rows)
	}
	res = mustQuery(t, db, `MATCH (a:Admin) DELETE a`)
	if res.Summary == nil || res.Summary.NodesDeleted != 1 {
		t.Fatalf("delete summary=%+v", res.Summary)
	}
	res = mustQuery(t, db, `MATCH (n) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(0) {
		t.Fatalf("count after delete=%v", res.Rows[0][0])
	}
}

func TestExplain(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "Alice"})`)
	res := mustQuery(t, db, `EXPLAIN MATCH (n:Person) WHERE n.name = "Alice" RETURN n`)
	if len(res.Plan) == 0 {
		t.Fatalf("expected plan, got %#v", res)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected plan rows")
	}
}

func TestMaxSolutionsLimit(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person), (b:Person), (c:Person)`)
	_, err := db.Query(context.Background(), `MATCH (n:Person) RETURN n`, QueryOptions{MaxSolutions: 2})
	if err == nil {
		t.Fatalf("expected max-solutions error")
	}
	if !strings.Contains(err.Error(), "maximum solution") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExplainPushdown(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "Alice", age: 39})`)
	res := mustQuery(t, db, `EXPLAIN MATCH (n:Person) WHERE n.age > 30 RETURN n`)
	joined := strings.Join(res.Plan, "\n")
	if !strings.Contains(joined, "pushed WHERE filter") {
		t.Fatalf("expected pushed filter in plan:\n%s", joined)
	}
}

func TestPathValueAndLength(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:P {name: "A"}), (b:P {name: "B"}), (c:P {name: "C"}), (a)-[:T]->(b), (b)-[:T]->(c)`)
	res := mustQuery(t, db, `MATCH p = (a:P {name: "A"})-[:T*2]->(c) RETURN length(p) AS len, p AS p`)
	if len(res.Rows) != 1 {
		t.Fatalf("rows=%#v", res.Rows)
	}
	if scalar(t, res.Rows[0][0]) != int64(2) {
		t.Fatalf("length=%v", res.Rows[0][0])
	}
	ps, ok := res.Rows[0][1].(executor.PathSummary)
	if !ok || ps.Length != 2 || len(ps.Nodes) != 3 || len(ps.Edges) != 2 {
		t.Fatalf("path summary=%#v", res.Rows[0][1])
	}
}

func TestSetMapMergeRemove(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {x: 1, y: 2})`)
	mustQuery(t, db, `MATCH (a:Person) SET a = {x: 10, z: 3}`)
	res := mustQuery(t, db, `MATCH (a:Person) RETURN a.x AS x, a.y AS y, a.z AS z`)
	if scalar(t, res.Rows[0][0]) != int64(10) || !res.Rows[0][1].(executor.Value).IsNull() || scalar(t, res.Rows[0][2]) != int64(3) {
		t.Fatalf("replace row=%#v", res.Rows[0])
	}
	mustQuery(t, db, `MATCH (a:Person) SET a += {y: 20}`)
	res = mustQuery(t, db, `MATCH (a:Person) RETURN a.y AS y`)
	if scalar(t, res.Rows[0][0]) != int64(20) {
		t.Fatalf("merge row=%#v", res.Rows[0])
	}
	mustQuery(t, db, `MATCH (a:Person) SET a:Admin REMOVE a.z, a:Person`)
	res = mustQuery(t, db, `MATCH (a:Admin) RETURN a.z AS z`)
	if len(res.Rows) != 1 || !res.Rows[0][0].(executor.Value).IsNull() {
		t.Fatalf("remove row=%#v", res.Rows)
	}
	res = mustQuery(t, db, `MATCH (a:Person) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(0) {
		t.Fatalf("label remove failed: %v", res.Rows[0][0])
	}
}

func TestGroupBy(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {age: 39}), (b:Person {age: 39}), (c:Person {age: 27})`)
	res := mustQuery(t, db, `MATCH (n:Person) RETURN n.age AS age, count(*) AS c ORDER BY age`)
	if len(res.Rows) != 2 {
		t.Fatalf("rows=%#v", res.Rows)
	}
	if scalar(t, res.Rows[0][0]) != int64(27) || scalar(t, res.Rows[0][1]) != int64(1) {
		t.Fatalf("row0=%#v", res.Rows[0])
	}
	if scalar(t, res.Rows[1][0]) != int64(39) || scalar(t, res.Rows[1][1]) != int64(2) {
		t.Fatalf("row1=%#v", res.Rows[1])
	}
}

func TestNullOrdering(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "A", age: 10}), (b:Person {name: "B"}), (c:Person {name: "C", age: 20})`)
	res := mustQuery(t, db, `MATCH (n:Person) RETURN n.name AS name ORDER BY n.age`)
	if scalar(t, res.Rows[0][0]) != "B" {
		t.Fatalf("asc null first: %#v", res.Rows)
	}
	res = mustQuery(t, db, `MATCH (n:Person) RETURN n.name AS name ORDER BY n.age DESC`)
	if scalar(t, res.Rows[2][0]) != "B" {
		t.Fatalf("desc null last: %#v", res.Rows)
	}
}

func TestCreateAfterMatch(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: "A"}), (b:Person {name: "B"})`)
	mustQuery(t, db, `MATCH (a:Person) CREATE (a)-[:TAGGED]->(t:Tag {name: "x"})`)
	res := mustQuery(t, db, `MATCH (t:Tag) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(2) {
		t.Fatalf("tags=%v", res.Rows[0][0])
	}
	res = mustQuery(t, db, `MATCH ()-[r:TAGGED]->() RETURN count(r) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(2) {
		t.Fatalf("edges=%v", res.Rows[0][0])
	}
}

func TestParallelEdgesAndSelfLoopUndirected(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:N {name: "A"}), (b:N {name: "B"}), (a)-[:E]->(b), (a)-[:E]->(b), (a)-[:E]->(a)`)
	res := mustQuery(t, db, `MATCH (a:N {name: "A"})-[:E]-(b) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(3) {
		t.Fatalf("undirected count=%v", res.Rows[0][0])
	}
	res = mustQuery(t, db, `MATCH ()-[r:E]->() RETURN count(r) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(3) {
		t.Fatalf("edge count=%v", res.Rows[0][0])
	}
}

func TestEmptyAggregate(t *testing.T) {
	db := newTestDB(t)
	res := mustQuery(t, db, `MATCH (n:Missing) RETURN count(*) AS c, sum(n.x) AS s, avg(n.x) AS a`)
	if len(res.Rows) != 1 {
		t.Fatalf("rows=%#v", res.Rows)
	}
	if scalar(t, res.Rows[0][0]) != int64(0) || !res.Rows[0][1].(executor.Value).IsNull() || !res.Rows[0][2].(executor.Value).IsNull() {
		t.Fatalf("empty agg row=%#v", res.Rows[0])
	}
}