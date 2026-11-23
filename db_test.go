package walk

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBackupAndOpenRestored(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: 'A'}), (b:Person {name: 'B'}), (a)-[:KNOWS]->(b)`)
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := db.BackupFile(backup); err != nil {
		t.Fatalf("backup: %v", err)
	}
	info, err := os.Stat(backup)
	if err != nil || info.Size() == 0 {
		t.Fatalf("backup file: %v size=%d", err, info.Size())
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer restored.Close()
	res := mustQuery(t, restored, `MATCH (a:Person)-[:KNOWS]->(b) RETURN a.name AS a, b.name AS b`)
	if len(res.Rows) != 1 || scalar(t, res.Rows[0][0]) != "A" {
		t.Fatalf("restored rows=%#v", res.Rows)
	}
	// The source database remains writable after a hot backup.
	mustQuery(t, db, `CREATE (c:Person {name: 'C'})`)
}

func TestCompactPreservesData(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: 'A'}), (b:Person {name: 'B'}), (a)-[:KNOWS]->(b)`)
	before, after, err := db.Compact()
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if before <= 0 || after <= 0 {
		t.Fatalf("sizes before=%d after=%d", before, after)
	}
	res := mustQuery(t, db, `MATCH (n:Person) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(2) {
		t.Fatalf("nodes after compact=%v", res.Rows[0][0])
	}
	res = mustQuery(t, db, `MATCH ()-[r:KNOWS]->() RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(1) {
		t.Fatalf("edges after compact=%v", res.Rows[0][0])
	}
	mustQuery(t, db, `CREATE (c:Person {name: 'C'})`)
}

func TestWriteQueryRollbackOnRuntimeError(t *testing.T) {
	db := newTestDB(t)
	_, err := db.Query(context.Background(), `CREATE (a:Person {name: 'A'}) SET a.x = 1 / 0`, QueryOptions{})
	if err == nil {
		t.Fatalf("expected division by zero error")
	}
	stats, err := db.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Nodes != 0 {
		t.Fatalf("rolled back query left %d nodes", stats.Nodes)
	}
}

func TestDeleteLinkedNodeIsAtomic(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: 'A'}), (b:Person {name: 'B'}), (a)-[:KNOWS]->(b)`)
	if _, err := db.Query(context.Background(), `MATCH (a:Person {name: 'A'}) DELETE a`, QueryOptions{}); err == nil {
		t.Fatalf("expected DELETE to fail for a linked node")
	}
	stats, err := db.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Nodes != 2 || stats.Edges != 1 {
		t.Fatalf("failed delete changed data: %+v", stats)
	}
	if _, err := db.Query(context.Background(), `MATCH (a:Person {name: 'A'}) DETACH DELETE a`, QueryOptions{}); err != nil {
		t.Fatalf("detach delete: %v", err)
	}
	info, err := db.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if info.Nodes != 1 || info.Edges != 0 {
		t.Fatalf("detach delete left nodes=%d edges=%d", info.Nodes, info.Edges)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("invariant warnings: %v", info.Warnings)
	}
}

func TestStatsInvariants(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: 'A'}), (b:Person {name: 'B'}), (a)-[:KNOWS]->(b)`)
	stats, err := db.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Nodes != 2 || stats.Edges != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	if len(stats.Labels) != 1 || stats.Labels[0].Label != "Person" || stats.Labels[0].Count != 2 {
		t.Fatalf("labels=%+v", stats.Labels)
	}
	if len(stats.Types) != 1 || stats.Types[0].Type != "KNOWS" || stats.Types[0].Count != 1 {
		t.Fatalf("types=%+v", stats.Types)
	}
	if len(stats.Warnings) != 0 {
		t.Fatalf("warnings=%v", stats.Warnings)
	}
	if stats.NodeSeq == 0 || stats.EdgeSeq == 0 {
		t.Fatalf("sequences=%d/%d", stats.NodeSeq, stats.EdgeSeq)
	}
}

func TestConcurrentReadersAndSerialWriters(t *testing.T) {
	db := newTestDB(t)
	mustQuery(t, db, `CREATE (a:Person {name: 'A'})`)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				res, err := db.Query(context.Background(), `MATCH (n:Person) RETURN count(*) AS c`, QueryOptions{})
				if err != nil {
					errs <- err
					return
				}
				_ = res
			}
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := db.Query(context.Background(), `CREATE (n:Person {name: 'X'})`, QueryOptions{}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent error: %v", err)
	}
	res := mustQuery(t, db, `MATCH (n:Person) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(6) {
		t.Fatalf("count=%v", res.Rows[0][0])
	}
}
