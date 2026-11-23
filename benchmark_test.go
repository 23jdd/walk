package walk

import (
	"context"
	"path/filepath"
	"testing"
)

func benchDB(b *testing.B) *DB {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.db")
	db, err := Open(path)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return db
}

func benchExec(b *testing.B, db *DB, q string) {
	b.Helper()
	if _, err := db.Query(context.Background(), q, QueryOptions{}); err != nil {
		b.Fatalf("setup %q: %v", q, err)
	}
}

func BenchmarkCreateNode(b *testing.B) {
	db := benchDB(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Query(context.Background(), `CREATE (n:Bench {i: 1})`, QueryOptions{}); err != nil {
			b.Fatalf("create: %v", err)
		}
	}
}

func BenchmarkOneHop(b *testing.B) {
	db := benchDB(b)
	benchExec(b, db, `CREATE (root:N {name: 'root'})`)
	// 1,000 outgoing edges.
	for i := 0; i < 100; i++ {
		benchExec(b, db, `MATCH (r:N {name: 'root'}) CREATE (r)-[:E]->(x:N {name: 'x'})`)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Query(context.Background(), `MATCH (r:N {name: 'root'})-[:E]->(x) RETURN count(*) AS c`, QueryOptions{}); err != nil {
			b.Fatalf("hop: %v", err)
		}
	}
}

func BenchmarkVarLen2(b *testing.B) {
	db := benchDB(b)
	// Chain root -> n1 ... -> n20, two hops from root.
	benchExec(b, db, `CREATE (a:N {name: '0'}), (b:N {name: '1'}), (c:N {name: '2'}), (a)-[:E]->(b), (b)-[:E]->(c)`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Query(context.Background(), `MATCH (a:N {name: '0'})-[:E*1..2]->(x) RETURN count(*) AS c`, QueryOptions{}); err != nil {
			b.Fatalf("varlen: %v", err)
		}
	}
}

func BenchmarkOrderLimit(b *testing.B) {
	db := benchDB(b)
	for i := 0; i < 200; i++ {
		benchExec(b, db, `CREATE (n:Bench {v: 1})`)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Query(context.Background(), `MATCH (n:Bench) RETURN n.v AS v ORDER BY v DESC LIMIT 10`, QueryOptions{}); err != nil {
			b.Fatalf("order: %v", err)
		}
	}
}
