package walk

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPServer(t *testing.T) {
	db := newTestDB(t)
	srv := httptest.NewServer(NewServer(db, ServerOptions{Timeout: 5 * time.Second}).Handler())
	defer srv.Close()

	post := func(path, body string) *http.Response {
		t.Helper()
		resp, err := http.Post(srv.URL+path, "text/plain", strings.NewReader(body))
		if err != nil {
			t.Fatalf("post %s: %v", path, err)
		}
		return resp
	}

	resp := post("/update", `CREATE (a:Person {name: 'Alice'}), (b:Person {name: 'Bob'}), (a)-[:KNOWS]->(b)`)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("update status=%d body=%s", resp.StatusCode, b)
	}
	var update Result
	if err := json.NewDecoder(resp.Body).Decode(&update); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	resp.Body.Close()
	if update.Summary == nil || update.Summary.NodesCreated != 2 {
		t.Fatalf("summary=%+v", update.Summary)
	}

	resp = post("/query", `MATCH (n:Person) RETURN n.name AS name ORDER BY name`)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("query status=%d body=%s", resp.StatusCode, b)
	}
	var qr Result
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	resp.Body.Close()
	if len(qr.Rows) != 2 || qr.Columns[0] != "name" {
		t.Fatalf("query result=%+v", qr)
	}

	// /query must reject writes.
	resp = post("/query", `CREATE (x:Person)`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for write on /query, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Stats.
	r, err := http.Get(srv.URL + "/stats")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	var stats GraphStats
	if err := json.NewDecoder(r.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	r.Body.Close()
	if stats.Nodes != 2 || stats.Edges != 1 {
		t.Fatalf("stats=%+v", stats)
	}

	// Backup snapshot.
	r, err = http.Get(srv.URL + "/backup")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	data, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || len(data) == 0 {
		t.Fatalf("backup read: %v len=%d", err, len(data))
	}
	if !bytes.HasPrefix(data, []byte("\x00\x00\x00\x00")) && !bytes.Contains(data, []byte("bbolt")) {
		// bbolt files start with page headers; just require a plausible size.
		if len(data) < 4096 {
			t.Fatalf("backup too small: %d", len(data))
		}
	}
}
