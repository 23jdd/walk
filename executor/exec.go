package executor

import (
	"encoding/binary"
	"errors"
	"iter"

	"github.com/23jdd/walk/utils"
	"go.etcd.io/bbolt"
)

var ErrInitBucket = errors.New("init bucket error")
var ErrNodesNotExist = errors.New("node not exist")
var ErrEdgesNotExist = errors.New("edge not exist")
var ErrNodeIdDuplicated = errors.New("duplicated id node")
var ErrNodeLinkEdge = errors.New("link edge error")
var ErrEdgeFromOrToNodeNoExist = errors.New("edge from or to node not exist")

type Exec struct {
	tx  *bbolt.Tx
	err error
}

func (e *Exec) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}
func (e *Exec) insertNode(n *Node) (uint64, error) {
	nodes := e.mustBucket("nodes")
	id, err := nodes.NextSequence()
	if err != nil {
		return 0, err
	}
	value, err := n.marshal()
	if err != nil {
		return 0, err
	}
	n.id = id
	err = nodes.Put(utob(id), value)
	if err != nil {
		return 0, err
	}
	ids, err := e.getOrCreateLabels(n.Labels...)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		err := e.insertLabelsIndex(n, id)
		if err != nil {
			return 0, err
		}
	}
	return id, nil
}
func (e *Exec) BatchInsertNode(ns iter.Seq[*Node]) error {
	nodes := e.mustBucket("nodes")
	for n := range ns {
		id, err := nodes.NextSequence()
		if err != nil {
			return err
		}
		value, err := n.marshal()
		if err != nil {
			return err
		}
		n.id = id
		err = nodes.Put(utob(id), value)
		if err != nil {
			return err
		}
		ids, err := e.getOrCreateLabels(n.Labels...)
		if err != nil {
			return err
		}
		for _, id := range ids {
			err := e.insertLabelsIndex(n, id)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// InsertNodeWithId insert a point id Node
//
// Note: n where node id > current sequence where set node id as sequence
func (e *Exec) InsertNodeWithId(n *Node) error {
	nodes := e.mustBucket("nodes")
	seq := nodes.Sequence()
	if n.id > seq {
		err := nodes.SetSequence(n.id)
		if err != nil {
			return err
		}
	}
	id := utob(n.id)
	value := nodes.Get(id)
	if value != nil {
		return ErrNodeIdDuplicated
	}
	value, err := n.marshal()
	if err != nil {
		return err
	}
	err = nodes.Put(id, value)
	if err != nil {
		return err
	}
	ids, err := e.getOrCreateLabels(n.Labels...)
	if err != nil {
		return err
	}
	for _, id := range ids {
		err := e.insertLabelsIndex(n, id)
		if err != nil {
			return err
		}
	}
	return nil
}
func (e *Exec) GetNode(id uint64) (*Node, error) {
	n := &Node{}
	nodes := e.mustBucket("nodes")
	values := nodes.Get(utob(id))
	if values == nil {
		return nil, ErrNodesNotExist
	}
	err := n.unMarshal(values)
	if err != nil {
		return nil, err
	}
	return n, nil
}
func (e *Exec) GetNodeByLabel(label string) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		labelId, err := e.getLabel(label)
		if err != nil {
			return
		}
		for id := range e.scanLabelsIndex(labelId) {
			if !yield(id) {
				return
			}
		}
		return
	}
}
func (e *Exec) DeleteNode(id uint64) error {
	nodes := e.mustBucket("nodes")
	if e.haveLinkEdge(id) {
		return ErrNodeLinkEdge
	}
	err := nodes.Delete(utob(id))
	if err != nil {
		return err
	}
	return err
}
func (e *Exec) DetachDeleteNode(id uint64) error {
	err := e.deleteEdgesIter(e.allLinkEdge(id))
	if err != nil {
		return err
	}
	nodes := e.mustBucket("nodes")
	return nodes.Delete(utob(id))
}
func (e *Exec) UpdateNodeProperty(id uint64, properties map[string]Value) error {
	nodes := e.mustBucket("nodes")
	value := nodes.Get(utob(id))
	node := NewNodes()
	err := node.unMarshal(value)
	if err != nil {
		return err
	}
	// TODO
	node.WithProperties(properties)
	value, err = node.marshal()
	if err != nil {
		return err
	}
	return nodes.Put(utob(id), value)
}
func (e *Exec) DeleteNodeProperty(id uint64, keys ...string) error {
	nodes := e.mustBucket("nodes")
	value := nodes.Get(utob(id))
	node := NewNodes()
	err := node.unMarshal(value)
	if err != nil {
		return err
	}
	// TODO
	utils.DeleteKeysBySlice(node.Properties, keys)
	value, err = node.marshal()
	if err != nil {
		return err
	}
	return nodes.Put(utob(id), value)
}
func (e *Exec) RemoveNodeLabels(id uint64, labels ...string) error {
	nodes := e.mustBucket("nodes")
	value := nodes.Get(utob(id))
	node := NewNodes()
	err := node.unMarshal(value)
	if err != nil {
		return err
	}
	// TODO
	node.Labels = utils.Difference(node.Labels, labels)
	value, err = node.marshal()
	if err != nil {
		return err
	}
	return nodes.Put(utob(id), value)
}
func (e *Exec) AddNodeLabels(id uint64, labels ...string) error {
	nodes := e.mustBucket("nodes")
	value := nodes.Get(utob(id))
	node := NewNodes()
	err := node.unMarshal(value)
	if err != nil {
		return err
	}
	// TODO
	node.Labels = utils.Union(node.Labels, labels)
	value, err = node.marshal()
	if err != nil {
		return err
	}
	return nodes.Put(utob(id), value)
}
func (e *Exec) InsertEdge(edge *Edge) (uint64, error) {
	// get relation typeId
	typeId, err := e.getOrCreateRelationTypes(edge)
	if err != nil {
		return 0, err
	}
	edges := e.mustBucket("edges")
	id, err := edges.NextSequence()
	if err != nil {
		return 0, err
	}
	edge.id = id
	value, err := edge.marshal()
	if err != nil {
		return 0, err
	}
	err = edges.Put(utob(id), value)
	if err != nil {
		return 0, err
	}
	// insert adj index
	err = e.insertAdjIndex(edge, typeId)
	if err != nil {
		return 0, err
	}
	err = e.insertRelationIndex(edge, typeId)
	if err != nil {
		return 0, err
	}
	return id, nil
}
func (e *Exec) InsertEdgeWithExist(edge *Edge) (uint64, error) {
	nodes := e.mustBucket("nodes")
	// judge from node and to node exist
	if !exist(nodes, edge.From) || !exist(nodes, edge.To) {

		return 0, ErrEdgeFromOrToNodeNoExist
	}
	// get or create relation typeId
	typeId, err := e.getOrCreateRelationTypes(edge)
	if err != nil {
		return 0, err
	}
	edges := e.mustBucket("edges")
	id, err := edges.NextSequence()
	if err != nil {
		return 0, err
	}
	edge.id = id
	value, err := edge.marshal()
	if err != nil {
		return 0, err
	}
	// insert edges
	err = edges.Put(utob(id), value)
	if err != nil {
		return 0, err
	}
	// insert adj index
	err = e.insertAdjIndex(edge, typeId)
	if err != nil {
		return 0, err
	}
	err = e.insertRelationIndex(edge, typeId)
	if err != nil {
		return 0, err
	}
	return id, nil
}
func (e *Exec) GetEdge(id uint64) (*Edge, error) {
	edge := &Edge{}
	edges := e.mustBucket("edges")
	values := edges.Get(utob(id))
	if values == nil {
		return nil, ErrEdgesNotExist
	}
	err := edge.unMarshal(values)
	if err != nil {
		return nil, err
	}
	return edge, nil
}
func (e *Exec) GetEdgeByRelation(relation string) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		typeId, err := e.getRelationTypes(relation)
		if err != nil {
			return
		}
		for id := range e.scanRelationIndex(typeId) {
			if !yield(id) {
				return
			}
		}
	}
}
func (e *Exec) UpdateEdgeProperty(id uint64, properties map[string]Value) error {
	edges := e.mustBucket("edges")
	value := edges.Get(utob(id))
	edge := NewEdge()
	err := edge.unMarshal(value)
	if err != nil {
		return err
	}
	// TODO
	edge.WithProperties(properties)
	value, err = edge.marshal()
	if err != nil {
		return err
	}
	return edges.Put(utob(id), value)
}
func (e *Exec) DeleteEdgeProperty(id uint64, keys ...string) error {
	edges := e.mustBucket("edges")
	value := edges.Get(utob(id))
	edge := NewEdge()
	err := edge.unMarshal(value)
	if err != nil {
		return err
	}
	// TODO
	utils.DeleteKeysBySlice(edge.Properties, keys)
	value, err = edge.marshal()
	if err != nil {
		return err
	}
	return edges.Put(utob(id), value)
}
func (e *Exec) deleteEdge(id uint64) error {
	edges := e.mustBucket("edges")
	return edges.Delete(utob(id))
}
func (e *Exec) deleteEdgesIter(ids iter.Seq[uint64]) error {
	edges := e.mustBucket("edges")
	for id := range ids {
		err := edges.Delete(utob(id))
		if err != nil {
			return err
		}
	}
	return nil
}
func (e *Exec) NodesCount() int {
	return e.countBucket("nodes")
}
func (e *Exec) EdgeCount() int {
	return e.countBucket("edges")
}

// countBucket return bucket include key-value pair count
func (e *Exec) countBucket(bucket string) int {
	return e.mustBucket(bucket).Stats().KeyN
}
func (e *Exec) CountLabelsNode(label string) int {
	id, err := e.getLabel(label)
	if err != nil {
		return 0
	}
	return e.countLabelsNode(id)
}
func (e *Exec) CountRelationsEdge(relation string) int {
	typeId, err := e.getRelationTypes(relation)
	if err != nil {
		return 0
	}
	return e.countRelationEdge(typeId)
}

func (e *Exec) mustBucket(key string) *bbolt.Bucket {
	bucket := e.tx.Bucket([]byte(key))
	if bucket == nil {
		panic(ErrInitBucket)
	}
	return bucket
}

func utob(id uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, id)
	return buf
}
func btou(buf []byte) uint64 {
	return binary.BigEndian.Uint64(buf)
}
func exist(bucket *bbolt.Bucket, key uint64) bool {
	return bucket.Get(utob(key)) != nil
}
