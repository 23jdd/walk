package executor

import (
	"fmt"
	"iter"
	"sort"
)

// Dir selects the traversal direction for adjacency expansion.
type Dir uint8

const (
	OutDir Dir = iota
	InDir
	BothDir
)

func (d Dir) String() string {
	switch d {
	case OutDir:
		return "out"
	case InDir:
		return "in"
	case BothDir:
		return "both"
	default:
		return "unknown"
	}
}

// AdjEdge is one adjacency expansion result. Endpoint is the neighbour node
// reached from the source; From/To always reflect the stored edge direction.
type AdjEdge struct {
	EdgeID uint64
	From   uint64
	To     uint64
	TypeID uint64
}

// Endpoint returns the far end when expanding from node.
func (a AdjEdge) Endpoint(from uint64) uint64 {
	if a.From == from {
		return a.To
	}
	return a.From
}

func (e *Exec) EdgeCount() int { return e.bucket(BucketEdges).Stats().KeyN }

func (e *Exec) AllEdgeIDs() iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		c := e.bucket(BucketEdges).Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if len(k) == 8 && !yield(btou(k)) {
				return
			}
		}
	}
}

func (e *Exec) GetEdge(id uint64) (*Edge, error) {
	if ed, ok := e.edges[id]; ok {
		return ed, nil
	}
	raw := e.bucket(BucketEdges).Get(utob(id))
	if raw == nil {
		return nil, fmt.Errorf("%w: id=%d", ErrEdgesNotExist, id)
	}
	ed := NewEdge()
	ed.ID = id
	if err := ed.Unmarshal(raw); err != nil {
		return nil, fmt.Errorf("decode edge %d: %w", id, err)
	}
	e.edges[id] = ed
	return ed, nil
}

func (e *Exec) EdgeExists(id uint64) bool {
	return e.bucket(BucketEdges).Get(utob(id)) != nil
}

// CreateEdge writes edges + adj_out + adj_in + edge_type atomically.
func (e *Exec) CreateEdge(from, to uint64, typ string, props map[string]Value) (*Edge, error) {
	ed := NewEdge()
	ed.From, ed.To, ed.Type = from, to, typ
	for k, v := range props {
		if !v.IsNull() {
			ed.Props[k] = v
		}
	}
	if err := e.PutEdge(ed); err != nil {
		return nil, err
	}
	return ed, nil
}

// PutEdge inserts an edge. Zero ID allocates from edges.NextSequence().
func (e *Exec) PutEdge(ed *Edge) error {
	if err := e.Check(); err != nil {
		return err
	}
	if ed == nil {
		return fmt.Errorf("nil edge")
	}
	cleanNullProps(ed.Props)
	if err := verifyEdge(ed); err != nil {
		return err
	}
	if !e.NodeExists(ed.From) || !e.NodeExists(ed.To) {
		return fmt.Errorf("%w: from=%d to=%d", ErrEdgeFromOrToNodeNoExist, ed.From, ed.To)
	}
	typeID, err := e.GetOrCreateType(ed.Type)
	if err != nil {
		return err
	}
	edges := e.bucket(BucketEdges)
	if ed.ID == 0 {
		id, err := edges.NextSequence()
		if err != nil {
			return err
		}
		ed.ID = id
	} else {
		if edges.Get(utob(ed.ID)) != nil {
			return fmt.Errorf("duplicated edge id: %d", ed.ID)
		}
		if seq := edges.Sequence(); ed.ID > seq {
			if err := edges.SetSequence(ed.ID); err != nil {
				return err
			}
		}
	}
	raw, err := ed.Marshal()
	if err != nil {
		return err
	}
	if err := edges.Put(utob(ed.ID), raw); err != nil {
		return err
	}
	if err := e.PutAdjacency(ed, typeID); err != nil {
		return err
	}
	if err := e.PutEdgeTypeIndex(ed.ID, typeID); err != nil {
		return err
	}
	e.edges[ed.ID] = ed
	e.Summary.EdgesCreated++
	return nil
}

// PutAdjacency maintains both adjacency buckets for an edge.
func (e *Exec) PutAdjacency(ed *Edge, typeID uint64) error {
	if err := e.bucket(BucketAdjOut).Put(key3(ed.From, typeID, ed.ID), utob(ed.To)); err != nil {
		return err
	}
	return e.bucket(BucketAdjIn).Put(key3(ed.To, typeID, ed.ID), utob(ed.From))
}

func (e *Exec) DeleteAdjacency(ed *Edge, typeID uint64) error {
	if err := e.bucket(BucketAdjOut).Delete(key3(ed.From, typeID, ed.ID)); err != nil {
		return err
	}
	return e.bucket(BucketAdjIn).Delete(key3(ed.To, typeID, ed.ID))
}

// DeleteEdge removes an edge from all four places. Missing edges are an
// explicit error for user deletes; use DeleteEdgeIfExists for cascade paths.
func (e *Exec) DeleteEdge(id uint64) error {
	ok, err := e.DeleteEdgeIfExists(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrEdgesNotExist, id)
	}
	return nil
}

func (e *Exec) DeleteEdgeIfExists(id uint64) (bool, error) {
	ed, err := e.GetEdge(id)
	if err != nil {
		if isNotExist(err, ErrEdgesNotExist) {
			e.Summary.EdgesSkipped++
			return false, nil
		}
		return false, err
	}
	typeID, err := e.GetTypeID(ed.Type)
	if err != nil {
		// Fall back to scanning the adjacency key if the registry is damaged.
		typeID = 0
	}
	if err := e.DeleteAdjacency(ed, typeID); err != nil {
		return false, err
	}
	if typeID != 0 {
		if err := e.DeleteEdgeTypeIndex(ed.ID, typeID); err != nil {
			return false, err
		}
	}
	if err := e.bucket(BucketEdges).Delete(utob(id)); err != nil {
		return false, err
	}
	delete(e.edges, id)
	e.Summary.EdgesDeleted++
	return true, nil
}

// Adjacent expands one hop from node in the given direction. Results are
// deterministic: deduplicated and sorted by edge id (operator spec §1.4).
func (e *Exec) Adjacent(node uint64, dir Dir, typeIDs []uint64) iter.Seq[AdjEdge] {
	return func(yield func(AdjEdge) bool) {
		seen := make(map[uint64]struct{})
		var out []AdjEdge
		add := func(a AdjEdge) {
			if _, ok := seen[a.EdgeID]; ok {
				return
			}
			seen[a.EdgeID] = struct{}{}
			out = append(out, a)
		}
		if dir == OutDir || dir == BothDir {
			e.scanAdjBucket(BucketAdjOut, node, typeIDs, true, add)
		}
		if dir == InDir || dir == BothDir {
			e.scanAdjBucket(BucketAdjIn, node, typeIDs, false, add)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
		for _, a := range out {
			if !yield(a) {
				return
			}
		}
	}
}

// scanAdjBucket scans [node|type|edge] prefixes. For adj_out the value is the
// destination; for adj_in the value is the source.
func (e *Exec) scanAdjBucket(bucket string, node uint64, typeIDs []uint64, out bool, add func(AdjEdge)) {
	b := e.bucket(bucket)
	c := b.Cursor()
	scan := func(prefix []byte) {
		for k, v := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = c.Next() {
			if len(k) != 24 || len(v) != 8 {
				continue
			}
			typeID := btou(k[8:16])
			edgeID := btou(k[16:])
			peer := btou(v)
			if out {
				add(AdjEdge{EdgeID: edgeID, From: node, To: peer, TypeID: typeID})
			} else {
				add(AdjEdge{EdgeID: edgeID, From: peer, To: node, TypeID: typeID})
			}
		}
	}
	if len(typeIDs) == 0 {
		scan(utob(node))
		return
	}
	for _, typeID := range typeIDs {
		scan(key2(node, typeID))
	}
}

// AdjacentTyped is a convenience wrapper for a single type (0 = all types).
func (e *Exec) AdjacentTyped(node uint64, dir Dir, typeID uint64) iter.Seq[AdjEdge] {
	if typeID == 0 {
		return e.Adjacent(node, dir, nil)
	}
	return e.Adjacent(node, dir, []uint64{typeID})
}

// ScanEdgesByType iterates edges of a type in edge-id order.
func (e *Exec) ScanEdgesByType(typ string) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		typeID, err := e.GetTypeID(typ)
		if err != nil {
			return
		}
		for id := range e.scanRelationIndex(typeID) {
			if !yield(id) {
				return
			}
		}
	}
}

func (e *Exec) EdgeTypeID(ed *Edge) uint64 {
	id, err := e.GetTypeID(ed.Type)
	if err != nil {
		return 0
	}
	return id
}

func (e *Exec) haveLinkEdge(id uint64) bool {
	ids, _ := e.EdgeIDsOfNode(id)
	return len(ids) > 0
}

func (e *Exec) allLinkEdge(id uint64) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		ids, _ := e.EdgeIDsOfNode(id)
		for _, eid := range ids {
			if !yield(eid) {
				return
			}
		}
	}
}

func (e *Exec) deleteEdgesIter(ids iter.Seq[uint64]) error {
	for id := range ids {
		if _, err := e.DeleteEdgeIfExists(id); err != nil {
			return err
		}
	}
	return nil
}

func (e *Exec) insertEdge(edge *Edge) (uint64, error) {
	if err := e.PutEdge(edge); err != nil {
		return 0, err
	}
	return edge.ID, nil
}

func (e *Exec) insertEdgeWithExist(edge *Edge) (uint64, error) {
	if !e.NodeExists(edge.From) || !e.NodeExists(edge.To) {
		return 0, ErrEdgeFromOrToNodeNoExist
	}
	return e.insertEdge(edge)
}

func isNotExist(err error, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
