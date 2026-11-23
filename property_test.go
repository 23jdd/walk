package walk

import (
	"context"
	"math/rand"
	"testing"

	"github.com/23jdd/walk/executor"
)

// TestPropertyInvariantsRandomOps is the F-22 property-style test: a fixed-seed
// random CRUD sequence, checking all storage invariants after every operation.
func TestPropertyInvariantsRandomOps(t *testing.T) {
	db := newTestDB(t)
	rng := rand.New(rand.NewSource(42))
	var nodes []uint64
	var edgeIDs []uint64
	check := func(step int) {
		t.Helper()
		err := db.View(context.Background(), func(e *executor.Exec) error {
			for _, w := range e.CheckInvariants() {
				t.Fatalf("step %d invariant: %s", step, w)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("step %d view: %v", step, err)
		}
	}
	for step := 0; step < 150; step++ {
		switch rng.Intn(6) {
		case 0, 1:
			var id uint64
			err := db.Update(context.Background(), func(e *executor.Exec) error {
				n, err := e.CreateNode([]string{"P", "Q"}, map[string]executor.Value{"age": executor.IntValue(int64(rng.Intn(100)))})
				if err != nil {
					return err
				}
				id = n.ID
				return nil
			})
			if err != nil {
				t.Fatalf("step %d create node: %v", step, err)
			}
			nodes = append(nodes, id)
		case 2:
			if len(nodes) >= 2 {
				a := nodes[rng.Intn(len(nodes))]
				b := nodes[rng.Intn(len(nodes))]
				var id uint64
				err := db.Update(context.Background(), func(e *executor.Exec) error {
					ed, err := e.CreateEdge(a, b, "R", map[string]executor.Value{"w": executor.IntValue(1)})
					if err != nil {
						return err
					}
					id = ed.ID
					return nil
				})
				if err != nil {
					t.Fatalf("step %d create edge: %v", step, err)
				}
				edgeIDs = append(edgeIDs, id)
			}
		case 3:
			if len(nodes) > 0 {
				id := nodes[rng.Intn(len(nodes))]
				err := db.Update(context.Background(), func(e *executor.Exec) error {
					return e.SetNodeProperties(id, map[string]executor.Value{"age": executor.IntValue(int64(rng.Intn(100)))}, false)
				})
				if err != nil {
					t.Fatalf("step %d set: %v", step, err)
				}
			}
		case 4:
			if len(edgeIDs) > 0 {
				idx := rng.Intn(len(edgeIDs))
				id := edgeIDs[idx]
				err := db.Update(context.Background(), func(e *executor.Exec) error {
					_, err := e.DeleteEdgeIfExists(id)
					return err
				})
				if err != nil {
					t.Fatalf("step %d delete edge: %v", step, err)
				}
				edgeIDs = append(edgeIDs[:idx], edgeIDs[idx+1:]...)
			}
		case 5:
			if len(nodes) > 0 {
				idx := rng.Intn(len(nodes))
				id := nodes[idx]
				err := db.Update(context.Background(), func(e *executor.Exec) error {
					return e.DeleteNode(id, true)
				})
				if err != nil {
					t.Fatalf("step %d detach delete: %v", step, err)
				}
				nodes = append(nodes[:idx], nodes[idx+1:]...)
			}
		}
		check(step)
	}
}

func TestLargeIntegerPrecision(t *testing.T) {
	db := newTestDB(t)
	const big = int64(9007199254740993) // 2^53 + 1
	res := mustQuery(t, db, `CREATE (n:P {big: 9007199254740993})`)
	if res.Summary == nil || res.Summary.NodesCreated != 1 {
		t.Fatalf("summary=%+v", res.Summary)
	}
	res = mustQuery(t, db, `MATCH (n:P) RETURN n.big AS big`)
	if len(res.Rows) != 1 {
		t.Fatalf("rows=%#v", res.Rows)
	}
	if scalar(t, res.Rows[0][0]) != big {
		t.Fatalf("big=%v (%T) want %d", res.Rows[0][0], res.Rows[0][0], big)
	}
}
