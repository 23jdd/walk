package walk

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/23jdd/walk/executor"
)

var ErrFileExt = errors.New("file ext error")
var ErrDuplicateNodeID = errors.New("duplicate node id")

// LoadOptions configures bulk import (F-06).
type LoadOptions struct {
	Format    string // auto | jsonl | csv
	BatchSize int
	Progress  io.Writer
}

// LoadSummary reports import results and invariant checks.
type LoadSummary struct {
	Nodes    int           `json:"nodes"`
	Edges    int           `json:"edges"`
	Duration time.Duration `json:"duration"`
	Warnings []string      `json:"warnings,omitempty"`
	NodeIDs  []uint64      `json:"-"`
	Batches  int           `json:"batches"`
}

// Load imports a node file followed by an edge file. Nodes must exist before
// edges (F-06 constraint 1).
func (db *DB) Load(nodesPath, edgesPath string, opt LoadOptions) (*LoadSummary, error) {
	start := time.Now()
	summary := &LoadSummary{}
	nodeIDs, err := db.loadNodes(nodesPath, opt, summary)
	if err != nil {
		return summary, err
	}
	summary.NodeIDs = nodeIDs
	if edgesPath != "" {
		if err := db.loadEdges(edgesPath, opt, nodeIDs, summary); err != nil {
			return summary, err
		}
	}
	summary.Duration = time.Since(start)
	if err := db.View(context.Background(), func(e *executor.Exec) error {
		summary.Warnings = e.CheckInvariants()
		return nil
	}); err != nil {
		return summary, err
	}
	return summary, nil
}

// LoadNodes imports only nodes.
func (db *DB) LoadNodes(path string, opt LoadOptions) (int, error) {
	s := &LoadSummary{}
	if _, err := db.loadNodes(path, opt, s); err != nil {
		return 0, err
	}
	return s.Nodes, nil
}

// LoadEdges imports only edges. When the node source used 0-based CSV row ids,
// from_idx/to_idx map to identity ids; otherwise they are direct node ids.
func (db *DB) LoadEdges(path string, opt LoadOptions) (int, error) {
	s := &LoadSummary{}
	if err := db.loadEdges(path, opt, nil, s); err != nil {
		return 0, err
	}
	return s.Edges, nil
}

func (db *DB) loadNodes(path string, opt LoadOptions, summary *LoadSummary) ([]uint64, error) {
	format, err := resolveFormat(path, opt.Format)
	if err != nil {
		return nil, err
	}
	var nodes []*executor.Node
	var ids []uint64
	switch format {
	case "jsonl":
		nodes, ids, err = readJSONLNodes(path)
	case "csv":
		nodes, ids, err = readCSVNodes(path)
	}
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return ids, nil
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	batch := opt.BatchSize
	if batch <= 0 {
		batch = db.cfg.BatchSize
	}
	if batch <= 0 {
		batch = 1000
	}
	start := time.Now()
	for i := 0; i < len(nodes); i += batch {
		end := min(i+batch, len(nodes))
		chunk := nodes[i:end]
		if err := db.Update(context.Background(), func(e *executor.Exec) error {
			for _, n := range chunk {
				if err := e.PutNodeExplicit(n); err != nil {
					return fmt.Errorf("import node id=%d: %w", n.ID, err)
				}
			}
			return nil
		}); err != nil {
			return ids, err
		}
		summary.Nodes += len(chunk)
		summary.Batches++
		reportProgress(opt.Progress, "nodes", summary.Nodes, len(nodes), start)
	}
	return ids, nil
}

func (db *DB) loadEdges(path string, opt LoadOptions, nodeIDs []uint64, summary *LoadSummary) error {
	format, err := resolveFormat(path, opt.Format)
	if err != nil {
		return err
	}
	var edges []*executor.Edge
	switch format {
	case "jsonl":
		edges, err = readJSONLEdges(path)
	case "csv":
		edges, err = readCSVEdges(path, nodeIDs)
	}
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		return nil
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	batch := opt.BatchSize
	if batch <= 0 {
		batch = db.cfg.BatchSize
	}
	if batch > 3000 {
		batch = 3000
	}
	if batch <= 0 {
		batch = 1000
	}
	start := time.Now()
	for i := 0; i < len(edges); i += batch {
		end := min(i+batch, len(edges))
		chunk := edges[i:end]
		if err := db.Update(context.Background(), func(e *executor.Exec) error {
			for _, ed := range chunk {
				if err := e.PutEdge(ed); err != nil {
					return fmt.Errorf("import edge %d->%d: %w", ed.From, ed.To, err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		summary.Edges += len(chunk)
		summary.Batches++
		reportProgress(opt.Progress, "edges", summary.Edges, len(edges), start)
	}
	return nil
}

func reportProgress(w io.Writer, stage string, done, total int, start time.Time) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "%s: %d/%d (%s)\n", stage, done, total, time.Since(start).Round(time.Millisecond))
}

func resolveFormat(path, format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jsonl", "json", "ndjson":
		return "jsonl", nil
	case "csv":
		return "csv", nil
	case "", "auto":
		switch strings.ToLower(filepath.Ext(path)) {
		case ".jsonl", ".ndjson", ".json":
			return "jsonl", nil
		case ".csv":
			return "csv", nil
		default:
			return "", fmt.Errorf("%w: %s", ErrFileExt, path)
		}
	}
	return "", fmt.Errorf("unsupported format %q", format)
}

// ---------------------------------------------------------------------------
// JSONL
// ---------------------------------------------------------------------------

func parseUintRaw(raw json.RawMessage) (uint64, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false, nil
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		v, err := strconv.ParseUint(num.String(), 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("invalid id %q", num.String())
		}
		return v, true, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("invalid id %q", s)
		}
		return v, true, nil
	}
	return 0, false, fmt.Errorf("invalid id %s", string(raw))
}

func readJSONLNodes(path string) ([]*executor.Node, []uint64, error) {
	type nodeLine struct {
		ID         json.RawMessage           `json:"id"`
		Labels     []string                  `json:"labels"`
		Props      map[string]executor.Value `json:"props"`
		Properties map[string]executor.Value `json:"properties"`
	}
	var nodes []*executor.Node
	var ids []uint64
	seen := map[uint64]struct{}{}
	var maxID uint64
	var auto []*executor.Node
	err := scanJSONL(path, func(line []byte, lineNo int) error {
		var raw nodeLine
		if err := json.Unmarshal(line, &raw); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		n := executor.NewNode()
		n.Labels = raw.Labels
		if raw.Props != nil {
			n.Props = raw.Props
		} else if raw.Properties != nil {
			n.Props = raw.Properties
		}
		id, has, err := parseUintRaw(raw.ID)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		if has {
			if _, ok := seen[id]; ok {
				return fmt.Errorf("%w: id=%d at line %d", ErrDuplicateNodeID, id, lineNo)
			}
			seen[id] = struct{}{}
			n.ID = id
			if id > maxID {
				maxID = id
			}
			nodes = append(nodes, n)
			ids = append(ids, id)
		} else {
			auto = append(auto, n)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	// Assign auto ids after the maximum explicit id to avoid collisions.
	next := maxID + 1
	for _, n := range auto {
		n.ID = next
		next++
		nodes = append(nodes, n)
		ids = append(ids, n.ID)
	}
	return nodes, ids, nil
}

func readJSONLEdges(path string) ([]*executor.Edge, error) {
	type edgeLine struct {
		ID         json.RawMessage           `json:"id"`
		From       json.RawMessage           `json:"from"`
		To         json.RawMessage           `json:"to"`
		Type       string                    `json:"type"`
		Relation   string                    `json:"relation"`
		Props      map[string]executor.Value `json:"props"`
		Properties map[string]executor.Value `json:"properties"`
	}
	var edges []*executor.Edge
	err := scanJSONL(path, func(line []byte, lineNo int) error {
		var raw edgeLine
		if err := json.Unmarshal(line, &raw); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		from, ok, err := parseUintRaw(raw.From)
		if err != nil || !ok {
			return fmt.Errorf("%s:%d: invalid from: %v", path, lineNo, err)
		}
		to, ok, err := parseUintRaw(raw.To)
		if err != nil || !ok {
			return fmt.Errorf("%s:%d: invalid to: %v", path, lineNo, err)
		}
		typ := raw.Type
		if typ == "" {
			typ = raw.Relation
		}
		if typ == "" {
			return fmt.Errorf("%s:%d: missing relationship type", path, lineNo)
		}
		ed := executor.NewEdgeWithLink(from, to, typ)
		if raw.Props != nil {
			ed.Props = raw.Props
		} else if raw.Properties != nil {
			ed.Props = raw.Properties
		}
		if id, has, err := parseUintRaw(raw.ID); err == nil && has {
			ed.ID = id
		}
		edges = append(edges, ed)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return edges, nil
}

func scanJSONL(path string, fn func(line []byte, lineNo int) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := fn([]byte(line), lineNo); err != nil {
			return err
		}
	}
	return sc.Err()
}

// ---------------------------------------------------------------------------
// CSV
// ---------------------------------------------------------------------------

func readCSVNodes(path string) ([]*executor.Node, []uint64, error) {
	rows, err := readCSV(path)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("%s: empty csv", path)
	}
	col := indexColumns(rows[0])
	idCol, hasID := col["id"]
	labelCol, hasLabels := col["labels"]
	if !hasLabels {
		labelCol, hasLabels = col["label"]
	}
	var nodes []*executor.Node
	var ids []uint64
	for i, row := range rows[1:] {
		n := executor.NewNode()
		n.ID = uint64(i)
		if hasID && idCol < len(row) && strings.TrimSpace(row[idCol]) != "" {
			id, err := strconv.ParseUint(strings.TrimSpace(row[idCol]), 10, 64)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: row %d: invalid id", path, i+2)
			}
			n.ID = id
		}
		if hasLabels && labelCol < len(row) {
			n.Labels = splitLabels(row[labelCol])
		}
		for name, idx := range col {
			if name == "id" || name == "labels" || name == "label" {
				continue
			}
			if idx < len(row) {
				n.Props[name] = inferValue(row[idx])
			}
		}
		nodes = append(nodes, n)
		ids = append(ids, n.ID)
	}
	seen := map[uint64]struct{}{}
	for _, n := range nodes {
		if _, ok := seen[n.ID]; ok {
			return nil, nil, fmt.Errorf("%w: id=%d", ErrDuplicateNodeID, n.ID)
		}
		seen[n.ID] = struct{}{}
	}
	return nodes, ids, nil
}

func readCSVEdges(path string, nodeIDs []uint64) ([]*executor.Edge, error) {
	rows, err := readCSV(path)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s: empty csv", path)
	}
	col := indexColumns(rows[0])
	fromCol, ok1 := col["from_idx"]
	toCol, ok2 := col["to_idx"]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("%s: edge csv requires from_idx and to_idx headers", path)
	}
	typeCol := -1
	for _, name := range []string{"type", "relation", "rel_type"} {
		if idx, ok := col[name]; ok {
			typeCol = idx
			break
		}
	}
	var edges []*executor.Edge
	for i, row := range rows[1:] {
		fromIdx, err := strconv.ParseUint(strings.TrimSpace(at(row, fromCol)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: row %d: invalid from_idx", path, i+2)
		}
		toIdx, err := strconv.ParseUint(strings.TrimSpace(at(row, toCol)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: row %d: invalid to_idx", path, i+2)
		}
		from, to := fromIdx, toIdx
		if nodeIDs != nil {
			if fromIdx >= uint64(len(nodeIDs)) || toIdx >= uint64(len(nodeIDs)) {
				return nil, fmt.Errorf("%s: row %d: node index out of range", path, i+2)
			}
			from, to = nodeIDs[fromIdx], nodeIDs[toIdx]
		}
		typ := "RELATED"
		if typeCol >= 0 && typeCol < len(row) && strings.TrimSpace(row[typeCol]) != "" {
			typ = strings.TrimSpace(row[typeCol])
		}
		ed := executor.NewEdgeWithLink(from, to, typ)
		for name, idx := range col {
			switch name {
			case "from_idx", "to_idx", "type", "relation", "rel_type", "id":
				continue
			}
			if idx < len(row) {
				ed.Props[name] = inferValue(row[idx])
			}
		}
		edges = append(edges, ed)
	}
	return edges, nil
}

func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && len(rows[0]) > 0 {
		rows[0][0] = strings.TrimPrefix(rows[0][0], "\ufeff")
	}
	return rows, nil
}

func indexColumns(header []string) map[string]int {
	out := make(map[string]int, len(header))
	for i, h := range header {
		out[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return out
}

func splitLabels(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if v := strings.TrimSpace(f); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// inferValue implements the documented CSV type inference (F-06).
func inferValue(s string) executor.Value {
	t := strings.TrimSpace(s)
	if i, err := strconv.ParseInt(t, 10, 64); err == nil {
		return executor.IntValue(i)
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return executor.FloatValue(f)
	}
	switch strings.ToLower(t) {
	case "true":
		return executor.BoolValue(true)
	case "false":
		return executor.BoolValue(false)
	}
	return executor.StringValue(s)
}

func at(row []string, i int) string {
	if i >= 0 && i < len(row) {
		return row[i]
	}
	return ""
}

// Legacy importer entry points.
func (db *DB) LoadNodeFromJsonL(path string) error {
	if filepath.Ext(path) != ".jsonl" {
		return ErrFileExt
	}
	_, err := db.LoadNodes(path, LoadOptions{})
	return err
}

func (db *DB) LoadEdgeFromJsonL(path string) error {
	if filepath.Ext(path) != ".jsonl" {
		return ErrFileExt
	}
	_, err := db.LoadEdges(path, LoadOptions{})
	return err
}
