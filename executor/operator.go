package executor

import (
	"fmt"
	"iter"
	"sort"
)

// EvalFunc evaluates an expression against one solution mapping.
type EvalFunc func(Solution) (Value, error)

// PropPred is an inline equality constraint (e.g. {name: "Alice"}).
type PropPred struct {
	Name  string
	Value Value
}

// Find anchors a pattern on nodes satisfying all labels and inline props.
// F-09: never full-scans when a label or property index is available.
func (e *Exec) Find(labels []string, props []PropPred, as string) Stream {
	return func(yield func(Solution) bool) {
		if err := e.Check(); err != nil {
			return
		}
		if len(labels) == 0 {
			e.Warn("find without label: full nodes scan")
		}
		ids := e.findCandidateIDs(labels, props)
		for id := range ids {
			n, err := e.GetNode(id)
			if err != nil {
				e.Fail(err)
				return
			}
			ok := true
			for _, l := range labels {
				if !n.HasLabel(l) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			for _, p := range props {
				v, exists := n.Props[p.Name]
				if !exists || !v.Equal(p.Value) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			if err := e.Emit(); err != nil {
				return
			}
			sol := Solution{}
			if as != "" {
				sol[as] = NodeEntity(id)
			}
			if !yield(sol) {
				return
			}
		}
	}
}

// findCandidateIDs chooses the cheapest anchor: property index > smallest
// label posting list > full scan.
func (e *Exec) findCandidateIDs(labels []string, props []PropPred) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		if len(labels) > 0 {
			for _, p := range props {
				if p.Value.IsNull() || !e.HasIndex(labels[0], p.Name) {
					continue
				}
				for id := range e.ScanIndexEquals(labels[0], p.Name, p.Value) {
					if !yield(id) {
						return
					}
				}
				return
			}
			best := labels[0]
			bestCount := -1
			for _, l := range labels {
				c := e.CountLabelsNode(l)
				if c == 0 {
					return
				}
				if bestCount < 0 || c < bestCount {
					best, bestCount = l, c
				}
			}
			for id := range e.ScanNodesByLabel(best) {
				if !yield(id) {
					return
				}
			}
			return
		}
		for id := range e.AllNodeIDs() {
			if !yield(id) {
				return
			}
		}
	}
}

// NodeByID returns a one-element stream binding as to an existing node.
func (e *Exec) NodeByID(id uint64, as string) Stream {
	return func(yield func(Solution) bool) {
		if !e.NodeExists(id) {
			return
		}
		sol := Solution{}
		if as != "" {
			sol[as] = NodeEntity(id)
		}
		_ = e.Emit()
		yield(sol)
	}
}

// Hop expands one adjacency step from a bound node (F-09).
// A solution with no matching neighbour dies; a bare Hop is therefore an
// existence filter (operator spec §3.2).
func (e *Exec) Hop(in Stream, src string, dir Dir, typeIDs []uint64, via, as string) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			ent, ok := sol[src]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: hop source %q is not a node", ErrTypeMismatch, src))
				return
			}
			for adj := range e.Adjacent(ent.Id, dir, typeIDs) {
				if err := e.Emit(); err != nil {
					return
				}
				next := CloneSolution(sol)
				if as != "" {
					next[as] = NodeEntity(adj.Endpoint(ent.Id))
				}
				if via != "" {
					next[via] = EdgeEntity(adj.EdgeID)
				}
				if !yield(next) {
					return
				}
			}
		}
	}
}

// HopWithPath extends an existing path binding (or starts one) while hopping.
func (e *Exec) HopWithPath(in Stream, src string, dir Dir, typeIDs []uint64, via, as, pathAs string) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			ent, ok := sol[src]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: hop source %q is not a node", ErrTypeMismatch, src))
				return
			}
			var base *Path
			if pathAs != "" {
				if p, ok := sol[pathAs]; ok && p.IsPath() {
					base = p.Path
				}
			}
			for adj := range e.Adjacent(ent.Id, dir, typeIDs) {
				if err := e.Emit(); err != nil {
					return
				}
				next := CloneSolution(sol)
				endpoint := adj.Endpoint(ent.Id)
				if as != "" {
					next[as] = NodeEntity(endpoint)
				}
				if via != "" {
					next[via] = EdgeEntity(adj.EdgeID)
				}
				if pathAs != "" {
					var p *Path
					if base != nil {
						p = base.Clone()
						p.Nodes = append(p.Nodes, endpoint)
						p.Edges = append(p.Edges, adj.EdgeID)
					} else {
						p = &Path{Nodes: []uint64{ent.Id, endpoint}, Edges: []uint64{adj.EdgeID}}
					}
					next[pathAs] = PathEntity(p)
				}
				if !yield(next) {
					return
				}
			}
		}
	}
}

// StartPath binds a path containing only the anchor node.
func (e *Exec) StartPath(in Stream, nodeVar, pathAs string) Stream {
	if pathAs == "" {
		return in
	}
	return func(yield func(Solution) bool) {
		for sol := range in {
			ent, ok := sol[nodeVar]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: path anchor %q is not a node", ErrTypeMismatch, nodeVar))
				return
			}
			next := CloneSolution(sol)
			next[pathAs] = PathEntity(&Path{Nodes: []uint64{ent.Id}})
			if !yield(next) {
				return
			}
		}
	}
}

// CrossJoin pairs every left solution with every right solution. The right
// stream is re-evaluated for each left row, which keeps it streaming.
func CrossJoin(left, right Stream) Stream {
	return func(yield func(Solution) bool) {
		for l := range left {
			for r := range right {
				next := CloneSolution(l)
				conflict := false
				for k, v := range r {
					if old, ok := next[k]; ok && (old.Kind != v.Kind || old.Id != v.Id || old.Path != v.Path) {
						conflict = true
						break
					}
					next[k] = v
				}
				if conflict {
					continue
				}
				if !yield(next) {
					return
				}
			}
		}
	}
}

// VarLen enumerates simple-edge paths of length lo..hi (F-10). Relationship
// uniqueness is enforced per path; nodes may repeat.
func (e *Exec) VarLen(in Stream, src string, dir Dir, typeIDs []uint64, lo, hi int, as string) Stream {
	return e.VarLenWithPath(in, src, dir, typeIDs, lo, hi, as, "")
}

// VarLenWithPath additionally binds a path value when pathAs is non-empty.
func (e *Exec) VarLenWithPath(in Stream, src string, dir Dir, typeIDs []uint64, lo, hi int, as, pathAs string) Stream {
	if hi <= 0 || hi > e.cfg.MaxDepth {
		hi = e.cfg.MaxDepth
	}
	if lo < 1 {
		lo = 1
	}
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			ent, ok := sol[src]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: varlen source %q is not a node", ErrTypeMismatch, src))
				return
			}
			used := make(map[uint64]bool)
			nodes := []uint64{ent.Id}
			edges := make([]uint64, 0, hi)
			aborted := false
			var dfs func(cur uint64, depth int)
			dfs = func(cur uint64, depth int) {
				if aborted || e.Err() != nil {
					return
				}
				if depth >= lo {
					if err := e.Emit(); err != nil {
						aborted = true
						return
					}
					next := CloneSolution(sol)
					if as != "" {
						next[as] = NodeEntity(cur)
					}
					if pathAs != "" {
						next[pathAs] = PathEntity(&Path{
							Nodes: append([]uint64(nil), nodes...),
							Edges: append([]uint64(nil), edges...),
						})
					}
					if !yield(next) {
						aborted = true
						return
					}
				}
				if depth >= hi {
					return
				}
				for adj := range e.Adjacent(cur, dir, typeIDs) {
					if used[adj.EdgeID] {
						continue
					}
					used[adj.EdgeID] = true
					nodes = append(nodes, adj.Endpoint(cur))
					edges = append(edges, adj.EdgeID)
					dfs(adj.Endpoint(cur), depth+1)
					nodes = nodes[:len(nodes)-1]
					edges = edges[:len(edges)-1]
					delete(used, adj.EdgeID)
					if aborted || e.Err() != nil {
						return
					}
				}
			}
			dfs(ent.Id, 0)
			if aborted {
				return
			}
		}
	}
}

// Shortest returns, per input solution, the first BFS path from src to dst
// using the requested direction and types (F-13). Unreachable pairs are
// silently dropped.
func (e *Exec) Shortest(in Stream, src, dst string, dir Dir, typeIDs []uint64, maxDepth int, as, pathAs string) Stream {
	if maxDepth <= 0 || maxDepth > e.cfg.MaxDepth {
		maxDepth = e.cfg.MaxDepth
	}
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			s, ok1 := sol[src]
			t, ok2 := sol[dst]
			if !ok1 || !ok2 || !s.IsNode() || !t.IsNode() {
				e.Fail(fmt.Errorf("%w: shortest endpoints must be nodes", ErrTypeMismatch))
				return
			}
			if s.Id == t.Id {
				next := CloneSolution(sol)
				if as != "" {
					next[as] = NodeEntity(t.Id)
				}
				if pathAs != "" {
					next[pathAs] = PathEntity(&Path{Nodes: []uint64{s.Id}})
				}
				if !yield(next) {
					return
				}
				continue
			}
			type step struct {
				parent uint64
				edge   uint64
			}
			visited := map[uint64]step{s.Id: {parent: s.Id}}
			depth := map[uint64]int{s.Id: 0}
			queue := []uint64{s.Id}
			var found *Path
			for len(queue) > 0 && found == nil {
				cur := queue[0]
				queue = queue[1:]
				if depth[cur] >= maxDepth {
					continue
				}
				for adj := range e.Adjacent(cur, dir, typeIDs) {
					nextNode := adj.Endpoint(cur)
					if _, seen := visited[nextNode]; seen {
						continue
					}
					visited[nextNode] = step{parent: cur, edge: adj.EdgeID}
					depth[nextNode] = depth[cur] + 1
					if nextNode == t.Id {
						p := &Path{}
						at := nextNode
						for at != s.Id {
							st := visited[at]
							p.Nodes = append(p.Nodes, at)
							p.Edges = append(p.Edges, st.edge)
							at = st.parent
						}
						p.Nodes = append(p.Nodes, s.Id)
						reverseUint64(p.Nodes)
						reverseUint64(p.Edges)
						found = p
						break
					}
					queue = append(queue, nextNode)
				}
			}
			if found == nil {
				continue
			}
			if err := e.Emit(); err != nil {
				return
			}
			next := CloneSolution(sol)
			if as != "" {
				next[as] = NodeEntity(t.Id)
			}
			if pathAs != "" {
				next[pathAs] = PathEntity(found)
			}
			if !yield(next) {
				return
			}
		}
	}
}

// New creates one node per input solution (or exactly one when in is nil).
func (e *Exec) New(in Stream, labels []string, props map[string]Value, as string) Stream {
	return func(yield func(Solution) bool) {
		if in == nil {
			n, err := e.CreateNode(labels, props)
			if err != nil {
				e.Fail(err)
				return
			}
			sol := Solution{}
			if as != "" {
				sol[as] = NodeEntity(n.ID)
			}
			_ = e.Emit()
			yield(sol)
			return
		}
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			n, err := e.CreateNode(labels, props)
			if err != nil {
				e.Fail(err)
				return
			}
			next := CloneSolution(sol)
			if as != "" {
				next[as] = NodeEntity(n.ID)
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(next) {
				return
			}
		}
	}
}

// Keep filters solutions by a predicate. true passes; false and null drop;
// a type/evaluation error aborts the query (fail-fast).
func (e *Exec) Keep(in Stream, ex EvalFunc) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			v, err := ex(sol)
			if err != nil {
				e.Fail(err)
				return
			}
			if v.Type() != BoolType || !v.AsBool() {
				continue
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(sol) {
				return
			}
		}
	}
}

// Where is an alias for Keep used by the Cypher front-end.
func (e *Exec) Where(in Stream, ex EvalFunc) Stream { return e.Keep(in, ex) }

// Link creates one edge per input solution and optionally binds it to via.
func (e *Exec) Link(in Stream, a, typ, b string, props map[string]Value, via string) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			from, ok1 := sol[a]
			to, ok2 := sol[b]
			if !ok1 || !ok2 || !from.IsNode() || !to.IsNode() {
				e.Fail(fmt.Errorf("%w: link endpoints %q/%q must be nodes", ErrTypeMismatch, a, b))
				return
			}
			ed, err := e.CreateEdge(from.Id, to.Id, typ, props)
			if err != nil {
				e.Fail(err)
				return
			}
			next := CloneSolution(sol)
			if via != "" {
				next[via] = EdgeEntity(ed.ID)
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(next) {
				return
			}
		}
	}
}

// SetProp evaluates ex per solution and writes (or deletes on null) v.prop.
func (e *Exec) SetProp(in Stream, v, prop string, ex EvalFunc) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			ent, ok := sol[v]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: set target %q is not a node", ErrTypeMismatch, v))
				return
			}
			val, err := ex(sol)
			if err != nil {
				e.Fail(err)
				return
			}
			if val.IsNull() {
				err = e.DeleteNodeProperties(ent.Id, prop)
			} else {
				err = e.SetNodeProperties(ent.Id, map[string]Value{prop: val}, false)
			}
			if err != nil {
				e.Fail(err)
				return
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(sol) {
				return
			}
		}
	}
}

// SetMap overwrites all properties of a node with a map expression.
func (e *Exec) SetMap(in Stream, v string, ex func(Solution) (map[string]Value, error)) Stream {
	return e.SetMapMode(in, v, ex, true)
}

// SetMapMode replaces (replace=true) or merges (replace=false) properties.
func (e *Exec) SetMapMode(in Stream, v string, ex func(Solution) (map[string]Value, error), replace bool) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			ent, ok := sol[v]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: set target %q is not a node", ErrTypeMismatch, v))
				return
			}
			props, err := ex(sol)
			if err != nil {
				e.Fail(err)
				return
			}
			if err := e.SetNodeProperties(ent.Id, props, replace); err != nil {
				e.Fail(err)
				return
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(sol) {
				return
			}
		}
	}
}

// SetLabel appends a label and maintains node_label / property indexes.
func (e *Exec) SetLabel(in Stream, v, label string) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			ent, ok := sol[v]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: set target %q is not a node", ErrTypeMismatch, v))
				return
			}
			if err := e.AddNodeLabels(ent.Id, label); err != nil {
				e.Fail(err)
				return
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(sol) {
				return
			}
		}
	}
}

// RemoveProp removes v.prop.
func (e *Exec) RemoveProp(in Stream, v, prop string) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			ent, ok := sol[v]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: remove target %q is not a node", ErrTypeMismatch, v))
				return
			}
			if err := e.DeleteNodeProperties(ent.Id, prop); err != nil {
				e.Fail(err)
				return
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(sol) {
				return
			}
		}
	}
}

// RemoveLabel removes a label and its inverted-index entry.
func (e *Exec) RemoveLabel(in Stream, v, label string) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			ent, ok := sol[v]
			if !ok || !ent.IsNode() {
				e.Fail(fmt.Errorf("%w: remove target %q is not a node", ErrTypeMismatch, v))
				return
			}
			if err := e.RemoveNodeLabels(ent.Id, label); err != nil {
				e.Fail(err)
				return
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(sol) {
				return
			}
		}
	}
}

// Del deletes nodes (cascade) or edges bound in v (operator spec §3.9).
func (e *Exec) Del(in Stream, v string) Stream {
	return e.Delete(in, v, true)
}

// Delete implements DELETE / DETACH DELETE. For edges detach is ignored.
func (e *Exec) Delete(in Stream, v string, detach bool) Stream {
	return func(yield func(Solution) bool) {
		for sol := range in {
			if err := e.Check(); err != nil {
				return
			}
			ent, ok := sol[v]
			if !ok {
				continue
			}
			var err error
			switch ent.Kind {
			case NodeKind:
				err = e.DeleteNode(ent.Id, detach)
			case EdgeKind:
				err = e.DeleteEdge(ent.Id)
			default:
				err = fmt.Errorf("%w: cannot delete path", ErrTypeMismatch)
			}
			if err != nil {
				e.Fail(err)
				return
			}
			if err := e.Emit(); err != nil {
				return
			}
			if !yield(sol) {
				return
			}
		}
	}
}

func reverseUint64(a []uint64) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}

// SortedNodeIDs is a small helper used by planners/tests.
func (e *Exec) SortedNodeIDs() []uint64 {
	var ids []uint64
	for id := range e.AllNodeIDs() {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
