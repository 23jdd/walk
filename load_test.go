package walk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/23jdd/walk/executor"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestLoadJSONL(t *testing.T) {
	dir := t.TempDir()
	nodes := writeFile(t, dir, "nodes.jsonl", `
{"id": 1, "labels": ["Person"], "props": {"name": "Alice", "age": 39}}
{"id": 2, "labels": ["Person"], "props": {"name": "Bob", "age": 27, "score": 1.5, "active": true}}
{"labels": ["Person"], "props": {"name": "Carol"}}
`)
	edges := writeFile(t, dir, "edges.jsonl", `
{"from": 1, "to": 2, "type": "KNOWS", "props": {"since": 2020}}
{"from": 2, "to": 3, "type": "KNOWS"}
`)
	db := newTestDB(t)
	var progress strings.Builder
	summary, err := db.Load(nodes, edges, LoadOptions{Format: "auto", BatchSize: 1, Progress: &progress})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if summary.Nodes != 3 || summary.Edges != 2 {
		t.Fatalf("summary=%+v", summary)
	}
	if len(summary.Warnings) != 0 {
		t.Fatalf("warnings=%v", summary.Warnings)
	}
	if !strings.Contains(progress.String(), "nodes: 3/3") || !strings.Contains(progress.String(), "edges: 2/2") {
		t.Fatalf("progress=%q", progress.String())
	}
	res := mustQuery(t, db, `MATCH (n:Person) RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(3) {
		t.Fatalf("count=%v", res.Rows[0][0])
	}
	res = mustQuery(t, db, `MATCH (a:Person {name: "Alice"})-[:KNOWS]->(b) RETURN b.name AS name, b.score AS score, b.active AS active`)
	if len(res.Rows) != 1 {
		t.Fatalf("rows=%#v", res.Rows)
	}
	if scalar(t, res.Rows[0][1]) != 1.5 || scalar(t, res.Rows[0][2]) != true {
		t.Fatalf("typed props=%#v", res.Rows[0])
	}
}

func TestLoadCSV(t *testing.T) {
	dir := t.TempDir()
	nodes := writeFile(t, dir, "nodes.csv", "id,labels,name,age,active,score\n10,Person,Alice,39,true,1.5\n11,Person, Bob ,27,false,2\n")
	edges := writeFile(t, dir, "edges.csv", "from_idx,to_idx,type,since\n0,1,KNOWS,2020\n")
	db := newTestDB(t)
	summary, err := db.Load(nodes, edges, LoadOptions{Format: "csv", BatchSize: 10})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if summary.Nodes != 2 || summary.Edges != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	res := mustQuery(t, db, `MATCH (a:Person {name: "Alice"})-[:KNOWS {since: 2020}]->(b) RETURN b.name AS name, b.age AS age, b.active AS active`)
	if len(res.Rows) != 1 {
		t.Fatalf("rows=%#v", res.Rows)
	}
	if scalar(t, res.Rows[0][1]) != int64(27) || scalar(t, res.Rows[0][2]) != false {
		t.Fatalf("row=%#v", res.Rows[0])
	}
}

func TestLoadRejectsDuplicateAndDangling(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t)
	dupes := writeFile(t, dir, "dupes.jsonl", `{"id": 1, "labels": ["P"]}`+"\n"+`{"id": 1, "labels": ["P"]}`+"\n")
	if _, err := db.Load(dupes, "", LoadOptions{}); err == nil {
		t.Fatalf("expected duplicate id error")
	}
	nodes := writeFile(t, dir, "nodes.jsonl", `{"id": 1, "labels": ["P"]}`+"\n")
	dangling := writeFile(t, dir, "dangling.jsonl", `{"from": 1, "to": 99, "type": "T"}`+"\n")
	if _, err := db.Load(nodes, dangling, LoadOptions{}); err == nil {
		t.Fatalf("expected dangling edge error")
	}
	// The failed edge import must not leave a partial edge behind.
	res := mustQuery(t, db, `MATCH ()-[r]->() RETURN count(*) AS c`)
	if scalar(t, res.Rows[0][0]) != int64(0) {
		t.Fatalf("partial edge committed: %v", res.Rows[0][0])
	}
}

func TestCSVTypeInference(t *testing.T) {
	cases := map[string]executor.Value{
		"42":    executor.IntValue(42),
		"-7":    executor.IntValue(-7),
		"1.25":  executor.FloatValue(1.25),
		"true":  executor.BoolValue(true),
		"FALSE": executor.BoolValue(false),
		"hello": executor.StringValue("hello"),
	}
	for raw, want := range cases {
		got := inferValue(raw)
		if got.Type() != want.Type() {
			t.Fatalf("infer %q type=%s want %s", raw, got.Type(), want.Type())
		}
		if !got.Equal(want) {
			t.Fatalf("infer %q=%v want %v", raw, got, want)
		}
	}
}
