package executor

import (
	"context"
	"iter"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"
)

func withTestExec(t *testing.T, fn func(e *Exec)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "exec.db")
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	err = db.Update(func(tx *bbolt.Tx) error {
		for _, name := range AllBuckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		fn(NewExec(tx, context.Background(), DefaultConfig()))
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
}

func countKeys(e *Exec, bucket string) int {
	n := 0
	c := e.bucket(bucket).Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		n++
	}
	return n
}

func mustNode(t *testing.T, e *Exec, labels []string, props map[string]Value) *Node {
	t.Helper()
	n, err := e.CreateNode(labels, props)
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	return n
}

func mustEdge(t *testing.T, e *Exec, from, to uint64, typ string) *Edge {
	t.Helper()
	ed, err := e.CreateEdge(from, to, typ, nil)
	if err != nil {
		t.Fatalf("create edge: %v", err)
	}
	return ed
}

func TestValueJSONRoundTrip(t *testing.T) {
	big := int64(1)<<60 + 7
	n := NewNode()
	n.ID = 42
	n.Labels = []string{"Person"}
	n.Props = map[string]Value{
		"big":   IntValue(big),
		"float": FloatValue(1.25),
		"bool":  BoolValue(true),
		"str":   StringValue(`a"b\c`),
	}
	raw, err := n.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Node
	if err := got.Unmarshal(raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Props["big"].AsInt() != big {
		t.Fatalf("big=%v want %d", got.Props["big"], big)
	}
	if got.Props["float"].AsFloat() != 1.25 || got.Props["bool"].AsBool() != true {
		t.Fatalf("props=%v", got.Props)
	}
	if got.Props["str"].AsString() != `a"b\c` {
		t.Fatalf("str=%q", got.Props["str"].AsString())
	}
}

func TestNodeEdgeInvariantsAndCascade(t *testing.T) {
	withTestExec(t, func(e *Exec) {
		a := mustNode(t, e, []string{"Person"}, map[string]Value{"name": StringValue("A")})
		b := mustNode(t, e, []string{"Person"}, map[string]Value{"name": StringValue("B")})
		c := mustNode(t, e, []string{"Person"}, map[string]Value{"name": StringValue("C")})
		e1 := mustEdge(t, e, a.ID, b.ID, "KNOWS")
		mustEdge(t, e, a.ID, c.ID, "KNOWS")
		mustEdge(t, e, b.ID, c.ID, "LIKES")

		if countKeys(e, BucketNodes) != 3 || countKeys(e, BucketEdges) != 3 {
			t.Fatalf("counts nodes=%d edges=%d", countKeys(e, BucketNodes), countKeys(e, BucketEdges))
		}
		if countKeys(e, BucketAdjOut) != 3 || countKeys(e, BucketAdjIn) != 3 {
			t.Fatalf("adj counts")
		}
		if countKeys(e, BucketEdgeType) != 3 {
			t.Fatalf("edge_type counts")
		}
		if err := e.DeleteEdge(e1.ID); err != nil {
			t.Fatalf("delete edge: %v", err)
		}
		if countKeys(e, BucketEdges) != 2 || countKeys(e, BucketAdjOut) != 2 || countKeys(e, BucketAdjIn) != 2 {
			t.Fatalf("edge delete did not clean adjacency: edges=%d out=%d in=%d", countKeys(e, BucketEdges), countKeys(e, BucketAdjOut), countKeys(e, BucketAdjIn))
		}
		// b still has one outgoing edge -> plain delete must fail.
		if err := e.DeleteNode(b.ID, false); err == nil {
			t.Fatalf("expected linked-node delete to fail")
		}
		if err := e.DeleteNode(b.ID, true); err != nil {
			t.Fatalf("detach delete: %v", err)
		}
		if countKeys(e, BucketNodes) != 2 || countKeys(e, BucketEdges) != 1 {
			t.Fatalf("after detach nodes=%d edges=%d", countKeys(e, BucketNodes), countKeys(e, BucketEdges))
		}
		if countKeys(e, BucketNodeLabel) != 2 {
			t.Fatalf("node_label=%d", countKeys(e, BucketNodeLabel))
		}
	})
}

func TestExplicitIDsAndSequence(t *testing.T) {
	withTestExec(t, func(e *Exec) {
		n := NewNode()
		n.ID = 100
		n.Labels = []string{"X"}
		if err := e.PutNodeExplicit(n); err != nil {
			t.Fatalf("explicit: %v", err)
		}
		if err := e.PutNodeExplicit(n); err == nil {
			t.Fatalf("expected duplicate id error")
		}
		zero := NewNode()
		zero.Labels = []string{"Zero"}
		if err := e.PutNodeExplicit(zero); err != nil {
			t.Fatalf("explicit zero: %v", err)
		}
		if !e.NodeExists(0) {
			t.Fatalf("node 0 missing")
		}
		auto, err := e.CreateNode([]string{"Auto"}, nil)
		if err != nil {
			t.Fatalf("auto: %v", err)
		}
		if auto.ID != 101 {
			t.Fatalf("auto id=%d want 101", auto.ID)
		}
	})
}

func TestPropertyIndexMaintenance(t *testing.T) {
	withTestExec(t, func(e *Exec) {
		a := mustNode(t, e, []string{"Person"}, map[string]Value{"age": IntValue(39)})
		b := mustNode(t, e, []string{"Person"}, map[string]Value{"age": IntValue(27)})
		if err := e.CreateIndex("Person", "age"); err != nil {
			t.Fatalf("create index: %v", err)
		}
		got := collectIDs(e.ScanIndexEquals("Person", "age", IntValue(39)))
		if len(got) != 1 || got[0] != a.ID {
			t.Fatalf("index lookup=%v", got)
		}
		if err := e.SetNodeProperties(a.ID, map[string]Value{"age": IntValue(40)}, false); err != nil {
			t.Fatalf("update: %v", err)
		}
		if got := collectIDs(e.ScanIndexEquals("Person", "age", IntValue(39))); len(got) != 0 {
			t.Fatalf("stale index entry: %v", got)
		}
		if got := collectIDs(e.ScanIndexEquals("Person", "age", IntValue(40))); len(got) != 1 || got[0] != a.ID {
			t.Fatalf("new index entry: %v", got)
		}
		if err := e.DeleteNode(b.ID, true); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if got := collectIDs(e.ScanIndexEquals("Person", "age", IntValue(27))); len(got) != 0 {
			t.Fatalf("index entry survived delete: %v", got)
		}
	})
}

func collectIDs(seq iter.Seq[uint64]) []uint64 {
	var out []uint64
	for id := range seq {
		out = append(out, id)
	}
	return out
}

func TestHopBothSelfLoop(t *testing.T) {
	withTestExec(t, func(e *Exec) {
		a := mustNode(t, e, []string{"N"}, nil)
		mustEdge(t, e, a.ID, a.ID, "SELF")
		in := func(yield func(Solution) bool) { yield(Solution{"a": NodeEntity(a.ID)}) }
		var got []Solution
		for sol := range e.Hop(in, "a", BothDir, nil, "", "b") {
			got = append(got, sol)
		}
		if len(got) != 1 {
			t.Fatalf("self-loop both produced %d solutions", len(got))
		}
	})
}

func TestVarLenEdgeUniquenessAndNodeRevisit(t *testing.T) {
	withTestExec(t, func(e *Exec) {
		a := mustNode(t, e, []string{"N"}, nil)
		b := mustNode(t, e, []string{"N"}, nil)
		c := mustNode(t, e, []string{"N"}, nil)
		d := mustNode(t, e, []string{"N"}, nil)
		// diamond a->b->d and a->c->d
		mustEdge(t, e, a.ID, b.ID, "E")
		mustEdge(t, e, a.ID, c.ID, "E")
		mustEdge(t, e, b.ID, d.ID, "E")
		mustEdge(t, e, c.ID, d.ID, "E")
		in := func(yield func(Solution) bool) { yield(Solution{"a": NodeEntity(a.ID)}) }
		var endpoints []uint64
		for sol := range e.VarLen(in, "a", OutDir, nil, 2, 2, "x") {
			endpoints = append(endpoints, sol["x"].Id)
		}
		// two length-2 paths to d, plus a->self->? only self edge can't repeat
		if len(endpoints) != 2 || endpoints[0] != d.ID || endpoints[1] != d.ID {
			t.Fatalf("varlen endpoints=%v", endpoints)
		}
	})
}

func TestGiveLimitStopsUpstream(t *testing.T) {
	withTestExec(t, func(e *Exec) {
		count := 0
		in := func(yield func(Solution) bool) {
			for i := 0; i < 100; i++ {
				count++
				if !yield(Solution{}) {
					return
				}
			}
		}
		proj := []Projection{{Name: "n", Expr: func(Solution) (Value, error) { return IntValue(1), nil }}}
		rows := e.Give(in, proj, GiveOptions{HasLimit: true, Limit: 3})
		n := 0
		for range rows {
			n++
		}
		if n != 3 {
			t.Fatalf("rows=%d want 3", n)
		}
		if count > 20 {
			t.Fatalf("upstream was not stopped early: %d", count)
		}
	})
}
