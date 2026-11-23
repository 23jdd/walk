package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// AggKind enumerates the supported aggregate functions.
type AggKind uint8

const (
	AggNone AggKind = iota
	AggCountStar
	AggCount
	AggSum
	AggAvg
	AggMin
	AggMax
)

// Projection is one column of a RETURN/GIVE clause.
type Projection struct {
	Name   string
	Expr   EvalFunc
	Entity string // project a whole bound entity/path instead of a scalar
	Agg    AggKind
	AggArg EvalFunc
}

// OrderKey is one ORDER BY key. Either Expr (evaluated on the pre-projection
// solution) or Alias (a projected column name) is used.
type OrderKey struct {
	Expr  EvalFunc
	Alias string
	Desc  bool
}

// GiveOptions carries the post-projection modifiers (F-12).
type GiveOptions struct {
	Distinct bool
	OrderBy  []OrderKey
	Skip     int
	Limit    int
	HasLimit bool
}

// NodeSummary is the JSON projection of a node variable.
type NodeSummary struct {
	ID     uint64           `json:"id"`
	Labels []string         `json:"labels"`
	Props  map[string]Value `json:"props"`
}

func (e *Exec) NodeSummary(id uint64) (NodeSummary, bool) {
	n, err := e.GetNode(id)
	if err != nil {
		return NodeSummary{}, false
	}
	return NodeSummary{ID: n.ID, Labels: append([]string(nil), n.Labels...), Props: cloneProps(n.Props)}, true
}

// EdgeSummary is the JSON projection of an edge variable.
type EdgeSummary struct {
	ID    uint64           `json:"id"`
	From  uint64           `json:"from"`
	To    uint64           `json:"to"`
	Type  string           `json:"type"`
	Props map[string]Value `json:"props"`
}

func (e *Exec) EdgeSummary(id uint64) (EdgeSummary, bool) {
	ed, err := e.GetEdge(id)
	if err != nil {
		return EdgeSummary{}, false
	}
	return EdgeSummary{ID: ed.ID, From: ed.From, To: ed.To, Type: ed.Type, Props: cloneProps(ed.Props)}, true
}

// PathSummary is the JSON projection of a path variable.
type PathSummary struct {
	Nodes  []NodeSummary `json:"nodes"`
	Edges  []EdgeSummary `json:"edges"`
	Length int           `json:"length"`
}

func (e *Exec) PathSummary(p *Path) PathSummary {
	out := PathSummary{Length: p.Length()}
	for _, id := range p.Nodes {
		if n, ok := e.NodeSummary(id); ok {
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, id := range p.Edges {
		if ed, ok := e.EdgeSummary(id); ok {
			out.Edges = append(out.Edges, ed)
		}
	}
	return out
}

func cloneProps(p map[string]Value) map[string]Value {
	out := make(map[string]Value, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func (e *Exec) projectEntity(sol Solution, name string) any {
	ent, ok := sol[name]
	if !ok {
		return NullValue()
	}
	switch ent.Kind {
	case NodeKind:
		if n, ok := e.NodeSummary(ent.Id); ok {
			return n
		}
	case EdgeKind:
		if ed, ok := e.EdgeSummary(ent.Id); ok {
			return ed
		}
	case PathKind:
		if ent.Path != nil {
			return e.PathSummary(ent.Path)
		}
	}
	return NullValue()
}

func (e *Exec) projectOne(sol Solution, p Projection) (any, error) {
	if p.Entity != "" {
		return e.projectEntity(sol, p.Entity), nil
	}
	if p.Expr == nil {
		return NullValue(), nil
	}
	v, err := p.Expr(sol)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func hasAggregate(items []Projection) bool {
	for _, it := range items {
		if it.Agg != AggNone {
			return true
		}
	}
	return false
}

// Give projects solutions into rows and applies DISTINCT/ORDER/SKIP/LIMIT.
func (e *Exec) Give(in Stream, items []Projection, opt GiveOptions) RowStream {
	return func(yield func(Row) bool) {
		if len(items) == 0 {
			return
		}
		if hasAggregate(items) {
			rows := e.aggregate(in, items)
			if e.Err() != nil {
				return
			}
			e.emitRows(rows, items, opt, yield)
			return
		}
		// Streaming projection. ORDER BY forces materialisation.
		if len(opt.OrderBy) > 0 {
			var rows []projectedRow
			for sol := range in {
				if err := e.Check(); err != nil {
					return
				}
				row, err := e.projectRow(sol, items)
				if err != nil {
					e.Fail(err)
					return
				}
				rows = append(rows, projectedRow{row: row, sol: sol})
			}
			e.emitProjected(rows, items, opt, yield)
			return
		}
		seen := make(map[string]struct{})
		skipped := 0
		emitted := 0
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			row, err := e.projectRow(sol, items)
			if err != nil {
				e.Fail(err)
				return
			}
			if opt.Distinct {
				key := rowKey(row)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
			}
			if skipped < opt.Skip {
				skipped++
				continue
			}
			if opt.HasLimit && emitted >= opt.Limit {
				return
			}
			emitted++
			if !yield(row) {
				return
			}
		}
	}
}

type projectedRow struct {
	row Row
	sol Solution
}

func (e *Exec) projectRow(sol Solution, items []Projection) (Row, error) {
	row := make(Row, len(items))
	for i, it := range items {
		v, err := e.projectOne(sol, it)
		if err != nil {
			return nil, err
		}
		row[i] = v
	}
	return row, nil
}

func (e *Exec) emitRows(rows []Row, items []Projection, opt GiveOptions, yield func(Row) bool) {
	prs := make([]projectedRow, len(rows))
	for i, r := range rows {
		prs[i] = projectedRow{row: r}
	}
	e.emitProjected(prs, items, opt, yield)
}

func (e *Exec) emitProjected(rows []projectedRow, items []Projection, opt GiveOptions, yield func(Row) bool) {
	if opt.Distinct {
		seen := make(map[string]struct{}, len(rows))
		out := rows[:0]
		for _, pr := range rows {
			key := rowKey(pr.row)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, pr)
		}
		rows = out
	}
	if len(opt.OrderBy) > 0 {
		sort.SliceStable(rows, func(i, j int) bool {
			for _, k := range opt.OrderBy {
				a, b := e.orderValue(rows[i], k, items), e.orderValue(rows[j], k, items)
				cmp := compareAny(a, b)
				if cmp == 0 {
					continue
				}
				if k.Desc {
					return cmp > 0
				}
				return cmp < 0
			}
			return false
		})
	}
	skipped := 0
	emitted := 0
	for _, pr := range rows {
		if skipped < opt.Skip {
			skipped++
			continue
		}
		if opt.HasLimit && emitted >= opt.Limit {
			return
		}
		emitted++
		if !yield(pr.row) {
			return
		}
	}
}

func (e *Exec) orderValue(pr projectedRow, k OrderKey, items []Projection) any {
	if k.Alias != "" {
		for i, it := range items {
			if it.Name == k.Alias && i < len(pr.row) {
				return pr.row[i]
			}
		}
		return NullValue()
	}
	if k.Expr == nil || pr.sol == nil {
		return nil
	}
	v, err := k.Expr(pr.sol)
	if err != nil {
		e.Fail(err)
		return nil
	}
	return v
}

func compareAny(a, b any) int {
	av, aok := a.(Value)
	bv, bok := b.(Value)
	if aok && bok {
		if av.IsNull() && bv.IsNull() {
			return 0
		}
		if av.IsNull() {
			return -1
		}
		if bv.IsNull() {
			return 1
		}
		if c, ok := av.Compare(bv); ok {
			return c
		}
	}
	// Non-scalar (node/edge/path/nil) values compare by their JSON encoding.
	ab, bb := mustJSON(a), mustJSON(b)
	return bytes.Compare(ab, bb)
}

func mustJSON(v any) []byte {
	b, err := jsonMarshal(v)
	if err != nil {
		return nil
	}
	return b
}

func rowKey(row Row) string {
	var b bytes.Buffer
	for _, v := range row {
		switch x := v.(type) {
		case Value:
			b.Write(canonicalKey(x))
		case nil:
			b.WriteByte('N')
		default:
			b.WriteByte('J')
			b.Write(mustJSON(x))
		}
		b.WriteByte(0)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Aggregation (operator spec §5.2)
// ---------------------------------------------------------------------------

type aggState struct {
	count   int64
	sumInt  int64
	sumF    float64
	useF    bool
	seen    bool
	min     Value
	max     Value
	hasNum  bool
}

func (a *aggState) add(v Value, kind AggKind) error {
	switch kind {
	case AggCountStar:
		a.count++
		return nil
	case AggCount:
		if !v.IsNull() {
			a.count++
		}
		return nil
	case AggSum, AggAvg:
		if v.IsNull() {
			return nil
		}
		switch v.Type() {
		case IntType:
			a.sumF += float64(v.AsInt())
			a.sumInt += v.AsInt()
			a.count++
			a.seen = true
		case FloatType:
			a.sumF += v.AsFloat()
			a.useF = true
			a.count++
			a.seen = true
		default:
			return fmt.Errorf("%w: aggregate requires numeric argument, got %s", ErrTypeMismatch, v.Type())
		}
		return nil
	case AggMin, AggMax:
		if v.IsNull() {
			return nil
		}
		if !a.seen {
			a.min, a.max, a.seen = v, v, true
			return nil
		}
		cmp, ok := v.Compare(a.min)
		if ok && cmp < 0 {
			a.min = v
		}
		cmp, ok = v.Compare(a.max)
		if ok && cmp > 0 {
			a.max = v
		}
		return nil
	}
	return nil
}

func (a *aggState) final(kind AggKind) Value {
	switch kind {
	case AggCountStar, AggCount:
		return IntValue(a.count)
	case AggSum:
		if !a.seen {
			return NullValue()
		}
		if a.useF {
			return FloatValue(a.sumF)
		}
		return IntValue(a.sumInt)
	case AggAvg:
		if !a.seen || a.count == 0 {
			return NullValue()
		}
		return FloatValue(a.sumF / float64(a.count))
	case AggMin:
		if !a.seen {
			return NullValue()
		}
		return a.min
	case AggMax:
		if !a.seen {
			return NullValue()
		}
		return a.max
	}
	return NullValue()
}

// aggregate materialises all input solutions and groups them by the
// non-aggregate projection values.
func (e *Exec) aggregate(in Stream, items []Projection) []Row {
	type group struct {
		keys []any
		aggs []*aggState
	}
	groups := map[string]*group{}
	var order []string

	for sol := range in {
		if err := e.Check(); err != nil {
			return nil
		}
		keys := make([]any, len(items))
		keyBuf := make([]byte, 0, 64)
		for i, it := range items {
			if it.Agg != AggNone {
				continue
			}
			v, err := e.projectOne(sol, it)
			if err != nil {
				e.Fail(err)
				return nil
			}
			keys[i] = v
			switch x := v.(type) {
			case Value:
				keyBuf = append(keyBuf, canonicalKey(x)...)
			default:
				keyBuf = append(keyBuf, mustJSON(x)...)
			}
			keyBuf = append(keyBuf, 0)
		}
		gk := string(keyBuf)
		g, ok := groups[gk]
		if !ok {
			g = &group{keys: keys, aggs: make([]*aggState, len(items))}
			for i := range items {
				g.aggs[i] = &aggState{}
			}
			groups[gk] = g
			order = append(order, gk)
		}
		for i, it := range items {
			if it.Agg == AggNone {
				continue
			}
			if it.Agg == AggCountStar {
				g.aggs[i].add(NullValue(), AggCountStar)
				continue
			}
			v, err := it.AggArg(sol)
			if err != nil {
				e.Fail(err)
				return nil
			}
			if err := g.aggs[i].add(v, it.Agg); err != nil {
				e.Fail(err)
				return nil
			}
		}
	}
	// Empty input with no grouping keys produces one row (SQL semantics).
	if len(groups) == 0 {
		allAgg := true
		for _, it := range items {
			if it.Agg == AggNone {
				allAgg = false
			}
		}
		if allAgg {
			row := make(Row, len(items))
			for i, it := range items {
				row[i] = (&aggState{}).final(it.Agg)
			}
			return []Row{row}
		}
		return nil
	}
	out := make([]Row, 0, len(order))
	for _, gk := range order {
		g := groups[gk]
		row := make(Row, len(items))
		for i, it := range items {
			if it.Agg == AggNone {
				row[i] = g.keys[i]
				continue
			}
			row[i] = g.aggs[i].final(it.Agg)
		}
		out = append(out, row)
	}
	return out
}

// Distinct is the Row-stream de-duplication operator.
func (e *Exec) Distinct(in RowStream) RowStream {
	return func(yield func(Row) bool) {
		seen := map[string]struct{}{}
		for row := range in {
			k := rowKey(row)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			if !yield(row) {
				return
			}
		}
	}
}

// Order sorts a row stream stably.
func Order(in RowStream, less func(a, b Row) bool) RowStream {
	return func(yield func(Row) bool) {
		var rows []Row
		for row := range in {
			rows = append(rows, row)
		}
		sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
		for _, row := range rows {
			if !yield(row) {
				return
			}
		}
	}
}

// SkipAll discards the first n rows.
func SkipAll(in RowStream, n int) RowStream {
	return func(yield func(Row) bool) {
		if n < 0 {
			n = 0
		}
		skipped := 0
		for row := range in {
			if skipped < n {
				skipped++
				continue
			}
			if !yield(row) {
				return
			}
		}
	}
}

// LimitAll forwards at most n rows and then stops the upstream stream.
func LimitAll(in RowStream, n int) RowStream {
	return func(yield func(Row) bool) {
		if n <= 0 {
			return
		}
		emitted := 0
		for row := range in {
			if !yield(row) {
				return
			}
			emitted++
			if emitted >= n {
				return
			}
		}
	}
}

// AvgFloat is a tiny helper documenting AVG's float64 result type.
func AvgFloat(sum float64, n int64) Value {
	if n == 0 {
		return NullValue()
	}
	return FloatValue(sum / float64(n))
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
