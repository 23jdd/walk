package walk

import (
	"encoding/binary"
	"errors"
	"iter"

	"github.com/23jdd/walk/utils"
	"go.etcd.io/bbolt"
)

var buckets = []string{
	"adj_in", "adj_out", "relation_types", "nodes", "edges", "labels", "node_label", "edge_type",
}
var ErrInitBucket = errors.New("init bucket error")
var ErrNodesNotExist = errors.New("node not exist")
var ErrEdgesNotExist = errors.New("edge not exist")
var ErrNodeIdDuplicated = errors.New("duplicated id node")
var ErrNodeLinkEdge = errors.New("link edge error")
var ErrEdgeFromOrToNodeNoExist = errors.New("edge from or to node not exist")

type DB struct {
	bolt *bbolt.DB
}

func Open(path string) (*DB, error) {
	bolt, err := bbolt.Open(path, 0600, &bbolt.Options{})
	if err != nil {
		return nil, err
	}
	db := &DB{bolt: bolt}
	err = db.initBuckets()
	if err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.bolt.Close()
}

func (db *DB) initBuckets() error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range buckets {
			_, err := tx.CreateBucketIfNotExists([]byte(bucket))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) InsertNode(n *Node) (id uint64, err error) {
	var seq uint64
	err = db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
		id, err := nodes.NextSequence()
		if err != nil {
			return err
		}
		value, err := n.marshal()
		if err != nil {
			return err
		}
		seq = id
		n.id = id
		err = nodes.Put(utob(id), value)
		if err != nil {
			return err
		}
		ids, err := getOrCreateLabels(tx, n.Labels...)
		if err != nil {
			return err
		}
		for _, id := range ids {
			err := insertLabelsIndex(tx, n, id)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return seq, err
}
func (db *DB) BatchInsertNode(ns iter.Seq[*Node]) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
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
			ids, err := getOrCreateLabels(tx, n.Labels...)
			if err != nil {
				return err
			}
			for _, id := range ids {
				err := insertLabelsIndex(tx, n, id)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// InsertNodeWithId insert a point id Node
//
// Note: n where node id > current sequence where set node id as sequence
func (db *DB) InsertNodeWithId(n *Node) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
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
		ids, err := getOrCreateLabels(tx, n.Labels...)
		if err != nil {
			return err
		}
		for _, id := range ids {
			err := insertLabelsIndex(tx, n, id)
			if err != nil {
				return err
			}
		}
		return nil
	})
}
func (db *DB) GetNode(id uint64) (*Node, error) {
	n := &Node{}
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
		values := nodes.Get(utob(id))
		if values == nil {
			return ErrNodesNotExist
		}
		return n.unMarshal(values)
	})
	if err != nil {
		return nil, err
	}
	return n, nil
}
func (db *DB) GetNodeByLabel(label string) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		_ = db.bolt.View(func(tx *bbolt.Tx) error {
			labelId, err := getLabel(tx, label)
			if err != nil {
				return nil
			}
			for id := range scanLabelsIndex(tx, labelId) {
				if !yield(id) {
					return nil
				}
			}
			return nil
		})
	}
}
func (db *DB) DeleteNode(id uint64) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
		if haveLinkEdge(tx, id) {
			return ErrNodeLinkEdge
		}
		err := nodes.Delete(utob(id))
		if err != nil {
			return err
		}
		return err
	})
}
func (db *DB) DetachDeleteNode(id uint64) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		err := deleteEdgesIter(tx, allLinkEdge(tx, id))
		if err != nil {
			return err
		}
		nodes := mustBucket(tx, "nodes")
		return nodes.Delete(utob(id))
	})
}
func (db *DB) UpdateNodeProperty(id uint64, properties map[string]Value) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
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
	})
}
func (db *DB) DeleteNodeProperty(id uint64, keys ...string) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
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
	})
}
func (db *DB) RemoveNodeLabels(id uint64, labels ...string) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
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
	})
}
func (db *DB) AddNodeLabels(id uint64, labels ...string) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
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
	})
}
func (db *DB) InsertEdge(e *Edge) (id uint64, err error) {
	var seq uint64
	err = db.bolt.Update(func(tx *bbolt.Tx) error {
		// get relation typeId
		typeId, err := getOrCreateRelationTypes(tx, e)
		if err != nil {
			return err
		}
		edges := mustBucket(tx, "edges")
		id, err := edges.NextSequence()
		if err != nil {
			return err
		}
		e.id = id
		value, err := e.marshal()
		if err != nil {
			return err
		}
		seq = id
		err = edges.Put(utob(id), value)
		if err != nil {
			return err
		}
		// insert adj index
		err = insertAdjIndex(tx, e, typeId)
		if err != nil {
			return err
		}
		return insertRelationIndex(tx, e, typeId)
	})
	return seq, err
}
func (db *DB) InsertEdgeWithExist(e *Edge) (id uint64, err error) {
	var seq uint64
	err = db.bolt.Update(func(tx *bbolt.Tx) error {
		nodes := mustBucket(tx, "nodes")
		// judge from node and to node exist
		if !exist(nodes, e.From) || !exist(nodes, e.To) {
			return ErrEdgeFromOrToNodeNoExist
		}
		// get or create relation typeId
		typeId, err := getOrCreateRelationTypes(tx, e)
		if err != nil {
			return err
		}
		edges := mustBucket(tx, "edges")
		id, err := edges.NextSequence()
		if err != nil {
			return err
		}
		e.id = id
		value, err := e.marshal()
		if err != nil {
			return err
		}
		seq = id
		// insert edges
		err = edges.Put(utob(id), value)
		if err != nil {
			return err
		}
		// insert adj index
		err = insertAdjIndex(tx, e, typeId)
		if err != nil {
			return err
		}
		return insertRelationIndex(tx, e, typeId)
	})
	return seq, err
}
func (db *DB) GetEdge(id uint64) (*Edge, error) {
	e := &Edge{}
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		edges := mustBucket(tx, "edges")
		values := edges.Get(utob(id))
		if values == nil {
			return ErrEdgesNotExist
		}
		return e.unMarshal(values)
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}
func (db *DB) GetEdgeByRelation(relation string) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		_ = db.bolt.View(func(tx *bbolt.Tx) error {
			typeId, err := getRelationTypes(tx, relation)
			if err != nil {
				return nil
			}
			for id := range scanRelationIndex(tx, typeId) {
				if !yield(id) {
					return nil
				}
			}
			return nil
		})
	}
}
func (db *DB) UpdateEdgeProperty(id uint64, properties map[string]Value) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		edges := mustBucket(tx, "edges")
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
	})
}
func (db *DB) DeleteEdgeProperty(id uint64, keys ...string) error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		edges := mustBucket(tx, "edges")
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
	})
}
func deleteEdge(tx *bbolt.Tx, id uint64) error {
	edges := mustBucket(tx, "edges")
	return edges.Delete(utob(id))
}
func deleteEdgesIter(tx *bbolt.Tx, ids iter.Seq[uint64]) error {
	edges := mustBucket(tx, "edges")
	for id := range ids {
		err := edges.Delete(utob(id))
		if err != nil {
			return err
		}
	}
	return nil
}
func (db *DB) NodesCount() int {
	return db.countBucket("nodes")
}
func (db *DB) EdgeCount() int {
	return db.countBucket("edges")
}

// countBucket return bucket include key-value pair count
func (db *DB) countBucket(bucket string) int {
	var count int
	_ = db.bolt.View(func(tx *bbolt.Tx) error {
		count = mustBucket(tx, bucket).Stats().KeyN
		return nil
	})
	return count
}
func (db *DB) CountLabelsNode(label string) int {
	var count int
	_ = db.bolt.View(func(tx *bbolt.Tx) error {
		id, err := getLabel(tx, label)
		if err != nil {
			return nil
		}
		count = countLabelsNode(tx, id)
		return nil
	})
	return count
}
func (db *DB) CountRelationsEdge(relation string) int {
	var count int
	_ = db.bolt.View(func(tx *bbolt.Tx) error {
		typeId, err := getRelationTypes(tx, relation)
		if err != nil {
			return nil
		}
		count = countRelationEdge(tx, typeId)
		return nil
	})
	return count
}
func (db *DB) Exec(exec string) error {
	return nil
}
func (db *DB) Query(query string) error {
	return nil
}

func mustBucket(tx *bbolt.Tx, key string) *bbolt.Bucket {
	bucket := tx.Bucket([]byte(key))
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
