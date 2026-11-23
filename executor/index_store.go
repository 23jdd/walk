package executor

import (
	"bytes"
	"fmt"
	"iter"
	"sort"
)

// ---------------------------------------------------------------------------
// Label dictionary and node_label inverted index (F-03)
// ---------------------------------------------------------------------------

func (e *Exec) GetOrCreateLabel(label string) (uint64, error) {
	if err := verifyName("label", label); err != nil {
		return 0, err
	}
	labels := e.bucket(BucketLabels)
	if raw := labels.Get([]byte(label)); raw != nil {
		return btou(raw), nil
	}
	id, err := labels.NextSequence()
	if err != nil {
		return 0, err
	}
	if err := labels.Put([]byte(label), utob(id)); err != nil {
		return 0, err
	}
	return id, nil
}

func (e *Exec) GetLabelID(label string) (uint64, error) {
	raw := e.bucket(BucketLabels).Get([]byte(label))
	if raw == nil {
		return 0, fmt.Errorf("%w: %s", ErrLabelNotExists, label)
	}
	return btou(raw), nil
}

func (e *Exec) getOrCreateLabel(label string) (uint64, error) {
	return e.GetOrCreateLabel(label)
}
func (e *Exec) getOrCreateLabels(labels ...string) ([]uint64, error) {
	ids := make([]uint64, len(labels))
	for i, l := range labels {
		id, err := e.GetOrCreateLabel(l)
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	return ids, nil
}
func (e *Exec) getLabel(label string) (uint64, error) { return e.GetLabelID(label) }

// PutNodeLabelIndex writes one node_label entry.
func (e *Exec) PutNodeLabelIndex(nodeID, labelID uint64) error {
	return e.bucket(BucketNodeLabel).Put(nodeLabelKey(labelID, nodeID), nil)
}

func (e *Exec) DeleteNodeLabelIndex(nodeID, labelID uint64) error {
	return e.bucket(BucketNodeLabel).Delete(nodeLabelKey(labelID, nodeID))
}

func (e *Exec) insertLabelsIndex(n *Node, labelID uint64) error {
	return e.PutNodeLabelIndex(n.ID, labelID)
}

// ScanNodesByLabel iterates node ids carrying a label in nodeID order.
func (e *Exec) ScanNodesByLabel(label string) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		id, err := e.GetLabelID(label)
		if err != nil {
			return
		}
		for nodeID := range e.scanLabelsIndex(id) {
			if !yield(nodeID) {
				return
			}
		}
	}
}

func (e *Exec) scanLabelsIndex(labelID uint64) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		b := e.bucket(BucketNodeLabel)
		c := b.Cursor()
		prefix := utob(labelID)
		for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
			if len(k) != 16 {
				continue
			}
			if !yield(btou(k[8:16])) {
				return
			}
		}
	}
}

func (e *Exec) countLabelsNode(labelID uint64) int {
	return countSeq(e.scanLabelsIndex(labelID))
}

func (e *Exec) deleteLabelIndex(nodeID, labelID uint64) error {
	return e.DeleteNodeLabelIndex(nodeID, labelID)
}

func (e *Exec) deleteNodeAllLabelIndex(n *Node) error {
	for _, label := range n.Labels {
		id, err := e.GetLabelID(label)
		if err != nil {
			continue
		}
		if err := e.DeleteNodeLabelIndex(n.ID, id); err != nil {
			return err
		}
	}
	return nil
}

// Labels returns the label dictionary as name -> id.
func (e *Exec) Labels() map[string]uint64 {
	out := make(map[string]uint64)
	c := e.bucket(BucketLabels).Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		if len(v) == 8 {
			out[string(k)] = btou(v)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Relationship-type registry (bidirectional, F-03 / operator spec §1.1)
// ---------------------------------------------------------------------------

func (e *Exec) GetOrCreateType(typ string) (uint64, error) {
	if err := verifyName("relationship type", typ); err != nil {
		return 0, err
	}
	rel := e.bucket(BucketRelTypes)
	if raw := rel.Get([]byte(typ)); raw != nil {
		return btou(raw), nil
	}
	id, err := rel.NextSequence()
	if err != nil {
		return 0, err
	}
	if err := rel.Put([]byte(typ), utob(id)); err != nil {
		return 0, err
	}
	if err := e.bucket(BucketRelTypeByID).Put(utob(id), []byte(typ)); err != nil {
		return 0, err
	}
	return id, nil
}

func (e *Exec) GetTypeID(typ string) (uint64, error) {
	raw := e.bucket(BucketRelTypes).Get([]byte(typ))
	if raw == nil {
		return 0, fmt.Errorf("%w: %s", ErrRelationNotExists, typ)
	}
	return btou(raw), nil
}

func (e *Exec) TypeName(id uint64) string {
	return string(e.bucket(BucketRelTypeByID).Get(utob(id)))
}

func (e *Exec) getOrCreateRelationTypes(edge *Edge) (uint64, error) {
	return e.GetOrCreateType(edge.Type)
}
func (e *Exec) getRelationTypes(relation string) (uint64, error) {
	return e.GetTypeID(relation)
}

// PutEdgeTypeIndex writes the type -> edge inverted entry.
func (e *Exec) PutEdgeTypeIndex(edgeID, typeID uint64) error {
	return e.bucket(BucketEdgeType).Put(edgeTypeKey(typeID, edgeID), nil)
}

func (e *Exec) DeleteEdgeTypeIndex(edgeID, typeID uint64) error {
	return e.bucket(BucketEdgeType).Delete(edgeTypeKey(typeID, edgeID))
}

func (e *Exec) insertRelationIndex(edge *Edge, typeID uint64) error {
	return e.PutEdgeTypeIndex(edge.ID, typeID)
}

func (e *Exec) scanRelationIndex(typeID uint64) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		b := e.bucket(BucketEdgeType)
		c := b.Cursor()
		prefix := utob(typeID)
		for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
			if len(k) != 16 {
				continue
			}
			if !yield(btou(k[8:16])) {
				return
			}
		}
	}
}

func (e *Exec) countRelationEdge(typeID uint64) int {
	return countSeq(e.scanRelationIndex(typeID))
}

func (e *Exec) CountRelationsEdge(relation string) int {
	id, err := e.GetTypeID(relation)
	if err != nil {
		return 0
	}
	return e.countRelationEdge(id)
}

func (e *Exec) CountLabelsNode(label string) int {
	id, err := e.GetLabelID(label)
	if err != nil {
		return 0
	}
	return e.countLabelsNode(id)
}

// ---------------------------------------------------------------------------
// Property secondary index (F-25)
// ---------------------------------------------------------------------------

func (e *Exec) HasIndex(label, prop string) bool {
	return e.bucket(BucketMeta).Get(indexDefKey(label, prop)) != nil
}

// CreateIndex registers a (label, property) index and backfills existing data.
func (e *Exec) CreateIndex(label, prop string) error {
	if err := verifyName("label", label); err != nil {
		return err
	}
	if err := verifyName("property", prop); err != nil {
		return err
	}
	if e.HasIndex(label, prop) {
		return nil
	}
	if err := e.bucket(BucketMeta).Put(indexDefKey(label, prop), []byte{1}); err != nil {
		return err
	}
	labelID, err := e.GetLabelID(label)
	if err != nil {
		return nil // no nodes with this label yet
	}
	idx := e.bucket(BucketPropIndex)
	for nodeID := range e.scanLabelsIndex(labelID) {
		n, err := e.GetNode(nodeID)
		if err != nil {
			continue
		}
		v, ok := n.Props[prop]
		if !ok || v.IsNull() {
			continue
		}
		key, err := propIndexKey(labelID, prop, v, nodeID)
		if err != nil {
			continue
		}
		if err := idx.Put(key, nil); err != nil {
			return err
		}
	}
	return nil
}

// DropIndex removes an index definition and all of its entries.
func (e *Exec) DropIndex(label, prop string) error {
	if !e.HasIndex(label, prop) {
		return nil
	}
	if err := e.bucket(BucketMeta).Delete(indexDefKey(label, prop)); err != nil {
		return err
	}
	labelID, err := e.GetLabelID(label)
	if err != nil {
		return nil
	}
	prefix := propIndexPrefix(labelID, prop)
	b := e.bucket(BucketPropIndex)
	c := b.Cursor()
	var keys [][]byte
	for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
		keys = append(keys, append([]byte(nil), k...))
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

// Indexes returns all declared indexes as label -> props.
func (e *Exec) Indexes() map[string][]string {
	out := make(map[string][]string)
	c := e.bucket(BucketMeta).Cursor()
	prefix := []byte("idx\x00")
	for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
		parts := bytes.Split(k[len(prefix):], []byte{0})
		if len(parts) != 2 {
			continue
		}
		out[string(parts[0])] = append(out[string(parts[0])], string(parts[1]))
	}
	return out
}

func (e *Exec) indexPropsForLabel(label string) []string {
	var out []string
	c := e.bucket(BucketMeta).Cursor()
	prefix := bytesKey([]byte("idx\x00"), []byte(label), []byte{0})
	for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
		out = append(out, string(k[len(prefix):]))
	}
	return out
}

// ScanIndexEquals returns node ids whose indexed property equals value.
func (e *Exec) ScanIndexEquals(label, prop string, value Value) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		labelID, err := e.GetLabelID(label)
		if err != nil {
			return
		}
		enc, err := encodeIndexValue(value)
		if err != nil {
			return
		}
		prefix := append(propIndexPrefix(labelID, prop), enc...)
		b := e.bucket(BucketPropIndex)
		c := b.Cursor()
		for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
			if len(k) < 8 {
				continue
			}
			nodeID := btou(k[len(k)-8:])
			if !yield(nodeID) {
				return
			}
		}
	}
}

// ScanIndexRange returns node ids with lo <= value <= hi. A null bound means
// unbounded on that side. It is a linear prefix scan filtered by the ordering
// encoding; sufficient for the subset's range predicates.
func (e *Exec) ScanIndexRange(label, prop string, lo, hi *Value) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		labelID, err := e.GetLabelID(label)
		if err != nil {
			return
		}
		base := propIndexPrefix(labelID, prop)
		var loEnc, hiEnc []byte
		if lo != nil {
			loEnc, err = encodeIndexValue(*lo)
			if err != nil {
				return
			}
		}
		if hi != nil {
			hiEnc, err = encodeIndexValue(*hi)
			if err != nil {
				return
			}
		}
		b := e.bucket(BucketPropIndex)
		c := b.Cursor()
		for k, _ := c.Seek(base); k != nil && hasPrefix(k, base); k, _ = c.Next() {
			enc := k[len(base) : len(k)-8]
			if loEnc != nil && bytes.Compare(enc, loEnc) < 0 {
				continue
			}
			if hiEnc != nil && bytes.Compare(enc, hiEnc) > 0 {
				continue
			}
			if !yield(btou(k[len(k)-8:])) {
				return
			}
		}
	}
}

// indexNode writes node_label entries and property-index entries for new.
func (e *Exec) indexNode(n *Node, _ *Node) error {
	for _, label := range n.Labels {
		labelID, err := e.GetOrCreateLabel(label)
		if err != nil {
			return err
		}
		if err := e.PutNodeLabelIndex(n.ID, labelID); err != nil {
			return err
		}
	}
	for _, label := range n.Labels {
		labelID, err := e.GetLabelID(label)
		if err != nil {
			continue
		}
		for _, prop := range e.indexPropsForLabel(label) {
			v, ok := n.Props[prop]
			if !ok || v.IsNull() {
				continue
			}
			key, err := propIndexKey(labelID, prop, v, n.ID)
			if err != nil {
				continue
			}
			if err := e.bucket(BucketPropIndex).Put(key, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Exec) unindexNode(n *Node) error {
	for _, label := range n.Labels {
		labelID, err := e.GetLabelID(label)
		if err != nil {
			continue
		}
		if err := e.DeleteNodeLabelIndex(n.ID, labelID); err != nil {
			return err
		}
		for _, prop := range e.indexPropsForLabel(label) {
			v, ok := n.Props[prop]
			if !ok || v.IsNull() {
				continue
			}
			key, err := propIndexKey(labelID, prop, v, n.ID)
			if err != nil {
				continue
			}
			if err := e.bucket(BucketPropIndex).Delete(key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Exec) reindexNode(old, next *Node) error {
	if err := e.unindexNode(old); err != nil {
		return err
	}
	return e.indexNode(next, old)
}

// LabelCounts returns label distribution sorted by count desc then name.
type LabelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

func (e *Exec) LabelCounts() []LabelCount {
	out := make([]LabelCount, 0)
	for label, id := range e.Labels() {
		out = append(out, LabelCount{Label: label, Count: e.countLabelsNode(id)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

func (e *Exec) TypeCounts() []TypeCount {
	out := make([]TypeCount, 0)
	rel := e.bucket(BucketRelTypes)
	c := rel.Cursor()
	for k, v := c.First(); k != nil; k, v = c.Next() {
		if len(v) != 8 {
			continue
		}
		out = append(out, TypeCount{Type: string(k), Count: e.countRelationEdge(btou(v))})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func countSeq[V any](s iter.Seq[V]) int {
	n := 0
	for range s {
		n++
	}
	return n
}
