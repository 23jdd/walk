package executor

import (
	"errors"
	"iter"
)

var ErrUnknownKind = errors.New("unknown kind")

type Kind uint8

const (
	NodeKind Kind = iota
	EdgeKind
	PathKind
)

// Entity is a binding target: a node, an edge, or a path value.
type Entity struct {
	Kind Kind
	Id   uint64
	Path *Path
}

func NodeEntity(id uint64) Entity { return Entity{Kind: NodeKind, Id: id} }
func EdgeEntity(id uint64) Entity { return Entity{Kind: EdgeKind, Id: id} }
func PathEntity(p *Path) Entity   { return Entity{Kind: PathKind, Path: p} }

func (e Entity) IsNode() bool { return e.Kind == NodeKind }
func (e Entity) IsEdge() bool { return e.Kind == EdgeKind }
func (e Entity) IsPath() bool { return e.Kind == PathKind }

// Path is an ordered walk alternating nodes and edges. Nodes[i] and
// Nodes[i+1] are joined by Edges[i].
type Path struct {
	Nodes []uint64 `json:"nodes"`
	Edges []uint64 `json:"edges"`
}

func NewPath() *Path { return &Path{} }

func (p *Path) Length() int {
	if p == nil {
		return 0
	}
	return len(p.Edges)
}

func (p *Path) Clone() *Path {
	if p == nil {
		return nil
	}
	return &Path{
		Nodes: append([]uint64(nil), p.Nodes...),
		Edges: append([]uint64(nil), p.Edges...),
	}
}

// Solution maps variable names to entities (the operator-spec Solution).
type Solution = map[string]Entity

// Stream is a pull-based sequence of solution mappings.
type Stream = iter.Seq[Solution]

// Row is one projected output row.
type Row []any

// RowStream is a pull-based sequence of rows.
type RowStream = iter.Seq[Row]

// Clone returns a shallow copy suitable for binding new variables on a
// branching operator (mandatory pre-branch clone discipline).
func CloneSolution(s Solution) Solution {
	out := make(Solution, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	return out
}

// Bind returns a cloned solution with name bound to entity.
func Bind(s Solution, name string, entity Entity) Solution {
	if name == "" {
		return s
	}
	out := CloneSolution(s)
	out[name] = entity
	return out
}

func GetSolution(s Solution, name string) (Entity, bool) {
	e, ok := s[name]
	return e, ok
}
