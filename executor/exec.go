package executor

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sort"

	"go.etcd.io/bbolt"
)

var (
	ErrInitBucket              = errors.New("init bucket error")
	ErrNodesNotExist           = errors.New("node not exist")
	ErrEdgesNotExist           = errors.New("edge not exist")
	ErrNodeIDDuplicated        = errors.New("duplicated id node")
	ErrNodeLinkEdge            = errors.New("node still has linked edges")
	ErrEdgeFromOrToNodeNoExist = errors.New("edge from or to node not exist")
	ErrLabelNotExists          = errors.New("label does not exist")
	ErrRelationNotExists       = errors.New("relationship type does not exist")
	ErrTooManyDetach           = errors.New("too many edges to detach")
	ErrMaxSolutions            = errors.New("maximum solution count exceeded")
	ErrContextCanceled         = errors.New("query canceled")
)

// Config holds execution limits (F-04/F-09/F-10/F-15).
type Config struct {
	MaxSolutions int
	MaxDepth     int
	MaxDetach    int
}

func DefaultConfig() Config {
	return Config{MaxSolutions: 1_000_000, MaxDepth: 8, MaxDetach: 100_000}
}

func (c Config) withDefaults() Config {
	if c.MaxSolutions <= 0 {
		c.MaxSolutions = 1_000_000
	}
	if c.MaxDepth <= 0 {
		c.MaxDepth = 8
	}
	if c.MaxDetach <= 0 {
		c.MaxDetach = 100_000
	}
	return c
}

// WriteSummary reports write-query counters (see the operator specification).
type WriteSummary struct {
	NodesCreated   int64 `json:"nodes_created"`
	NodesDeleted   int64 `json:"nodes_deleted"`
	EdgesCreated   int64 `json:"edges_created"`
	EdgesDeleted   int64 `json:"edges_deleted"`
	PropertiesSet  int64 `json:"properties_set"`
	LabelsAdded    int64 `json:"labels_added"`
	LabelsRemoved  int64 `json:"labels_removed"`
	EdgesSkipped   int64 `json:"edges_skipped"`
	NodesMatched   int64 `json:"nodes_matched"`
	EdgesMatched   int64 `json:"edges_matched"`
}

// Exec is a single-operation executor bound to one bbolt transaction. It is
// the storage API used by every operator.
type Exec struct {
	tx  *bbolt.Tx
	ctx context.Context
	cfg Config

	err       error
	solutions int

	nodes map[uint64]*Node
	edges map[uint64]*Edge

	Warnings []string
	Summary  WriteSummary
}

func NewExec(tx *bbolt.Tx, ctx context.Context, cfg Config) *Exec {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Exec{
		tx:    tx,
		ctx:   ctx,
		cfg:   cfg.withDefaults(),
		nodes: make(map[uint64]*Node),
		edges: make(map[uint64]*Edge),
	}
}

func (e *Exec) Config() Config { return e.cfg }
func (e *Exec) Context() context.Context {
	return e.ctx
}

// Fail records the first error and stops the pipeline (fail-fast discipline).
func (e *Exec) Fail(err error) {
	if err != nil && e.err == nil {
		e.err = err
	}
}

func (e *Exec) Err() error { return e.err }

// Warn records a non-fatal planner/executor warning (EXPLAIN and CLI output).
func (e *Exec) Warn(msg string) {
	for _, w := range e.Warnings {
		if w == msg {
			return
		}
	}
	e.Warnings = append(e.Warnings, msg)
}

// Check returns a cancellation error if the context is done.
func (e *Exec) Check() error {
	if e.err != nil {
		return e.err
	}
	select {
	case <-e.ctx.Done():
		if e.err == nil {
			e.err = e.ctx.Err()
		}
		return e.err
	default:
	}
	return nil
}

// Emit records one produced solution and enforces limits/timeouts every 1024
// solutions (F-04).
func (e *Exec) Emit() error {
	if err := e.Check(); err != nil {
		return err
	}
	e.solutions++
	if e.solutions > e.cfg.MaxSolutions {
		err := fmt.Errorf("%w: limit %d", ErrMaxSolutions, e.cfg.MaxSolutions)
		e.Fail(err)
		return err
	}
	if e.solutions%1024 == 0 {
		return e.Check()
	}
	return nil
}

func (e *Exec) bucket(name string) *bbolt.Bucket {
	b := e.tx.Bucket([]byte(name))
	if b == nil {
		panic(fmt.Errorf("%w: %s", ErrInitBucket, name))
	}
	return b
}

// Legacy helper name.
func (e *Exec) mustBucket(name string) *bbolt.Bucket { return e.bucket(name) }

// ---------------------------------------------------------------------------
// Node storage (F-01/F-05/F-14)
// ---------------------------------------------------------------------------

func (e *Exec) NodeCount() int { return e.bucket(BucketNodes).Stats().KeyN }

func (e *Exec) AllNodeIDs() iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		c := e.bucket(BucketNodes).Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if len(k) != 8 {
				continue
			}
			if !yield(btou(k)) {
				return
			}
		}
	}
}

func (e *Exec) GetNode(id uint64) (*Node, error) {
	if n, ok := e.nodes[id]; ok {
		return n, nil
	}
	raw := e.bucket(BucketNodes).Get(utob(id))
	if raw == nil {
		return nil, fmt.Errorf("%w: id=%d", ErrNodesNotExist, id)
	}
	n := NewNodeWithID(id)
	if err := n.Unmarshal(raw); err != nil {
		return nil, fmt.Errorf("decode node %d: %w", id, err)
	}
	e.nodes[id] = n
	return n, nil
}

// nodeExists reports existence without decoding.
func (e *Exec) NodeExists(id uint64) bool {
	return e.bucket(BucketNodes).Get(utob(id)) != nil
}

// CreateNode allocates an id and writes the node plus its label/property
// indexes in the current transaction.
func (e *Exec) CreateNode(labels []string, props map[string]Value) (*Node, error) {
	n := NewNode()
	n.Labels = unionStrings(nil, labels...)
	n.Props = make(map[string]Value, len(props))
	for k, v := range props {
		if v.IsNull() {
			continue
		}
		n.Props[k] = v
	}
	if err := e.PutNode(n); err != nil {
		return nil, err
	}
	return n, nil
}

// PutNode inserts a node. A zero ID triggers sequence allocation; a non-zero
// ID is treated as an explicit import and must not collide (F-05).
func (e *Exec) PutNode(n *Node) error {
	if err := e.Check(); err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("nil node")
	}
	cleanNullProps(n.Props)
	if err := verifyNode(n); err != nil {
		return err
	}
	nodes := e.bucket(BucketNodes)
	if n.ID == 0 {
		id, err := nodes.NextSequence()
		if err != nil {
			return err
		}
		n.ID = id
	} else {
		if nodes.Get(utob(n.ID)) != nil {
			return fmt.Errorf("%w: id=%d", ErrNodeIDDuplicated, n.ID)
		}
		if seq := nodes.Sequence(); n.ID > seq {
			if err := nodes.SetSequence(n.ID); err != nil {
				return err
			}
		}
	}
	raw, err := n.Marshal()
	if err != nil {
		return err
	}
	if err := nodes.Put(utob(n.ID), raw); err != nil {
		return err
	}
	if err := e.indexNode(n, nil); err != nil {
		return err
	}
	e.nodes[n.ID] = n
	e.Summary.NodesCreated++
	return nil
}

// UpdateNode applies fn to the stored node and rewrites indexes atomically.
func (e *Exec) UpdateNode(id uint64, fn func(n *Node) error) (*Node, error) {
	old, err := e.GetNode(id)
	if err != nil {
		return nil, err
	}
	next := &Node{ID: old.ID, Labels: append([]string(nil), old.Labels...), Props: make(map[string]Value, len(old.Props))}
	for k, v := range old.Props {
		next.Props[k] = v
	}
	if err := fn(next); err != nil {
		return nil, err
	}
	cleanNullProps(next.Props)
	if err := verifyNode(next); err != nil {
		return nil, err
	}
	raw, err := next.Marshal()
	if err != nil {
		return nil, err
	}
	if err := e.bucket(BucketNodes).Put(utob(id), raw); err != nil {
		return nil, err
	}
	if err := e.reindexNode(old, next); err != nil {
		return nil, err
	}
	e.nodes[id] = next
	return next, nil
}

func (e *Exec) SetNodeProperties(id uint64, props map[string]Value, replace bool) error {
	_, err := e.UpdateNode(id, func(n *Node) error {
		if replace && props != nil {
			n.Props = make(map[string]Value, len(props))
		}
		for k, v := range props {
			if v.IsNull() {
				delete(n.Props, k)
				continue
			}
			n.Props[k] = v
		}
		e.Summary.PropertiesSet++
		return nil
	})
	return err
}

func (e *Exec) DeleteNodeProperties(id uint64, keys ...string) error {
	_, err := e.UpdateNode(id, func(n *Node) error {
		for _, k := range keys {
			delete(n.Props, k)
		}
		return nil
	})
	return err
}

func (e *Exec) AddNodeLabels(id uint64, labels ...string) error {
	_, err := e.UpdateNode(id, func(n *Node) error {
		for _, l := range labels {
			if err := verifyName("label", l); err != nil {
				return err
			}
			if !n.HasLabel(l) {
				n.Labels = append(n.Labels, l)
				e.Summary.LabelsAdded++
			}
		}
		return nil
	})
	return err
}

func (e *Exec) RemoveNodeLabels(id uint64, labels ...string) error {
	_, err := e.UpdateNode(id, func(n *Node) error {
		for _, l := range labels {
			if n.HasLabel(l) {
				n.Labels = removeString(n.Labels, l)
				e.Summary.LabelsRemoved++
			}
		}
		return nil
	})
	return err
}

// EdgeIDsOfNode returns the deduplicated ids of all edges incident to a node.
func (e *Exec) EdgeIDsOfNode(id uint64) ([]uint64, error) {
	seen := make(map[uint64]struct{})
	var out []uint64
	add := func(edgeID uint64) {
		if _, ok := seen[edgeID]; ok {
			return
		}
		seen[edgeID] = struct{}{}
		out = append(out, edgeID)
	}
	outBucket := e.bucket(BucketAdjOut)
	prefix := utob(id)
	c := outBucket.Cursor()
	for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
		if len(k) == 24 {
			add(btou(k[16:]))
		}
	}
	inBucket := e.bucket(BucketAdjIn)
	c = inBucket.Cursor()
	for k, _ := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, _ = c.Next() {
		if len(k) == 24 {
			add(btou(k[16:]))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// DeleteNode implements DELETE / DETACH DELETE semantics (F-15).
func (e *Exec) DeleteNode(id uint64, detach bool) error {
	if err := e.Check(); err != nil {
		return err
	}
	edgeIDs, err := e.EdgeIDsOfNode(id)
	if err != nil {
		return err
	}
	if len(edgeIDs) > 0 {
		if !detach {
			return fmt.Errorf("%w: id=%d edges=%d (use DETACH DELETE)", ErrNodeLinkEdge, id, len(edgeIDs))
		}
		if len(edgeIDs) > e.cfg.MaxDetach {
			return fmt.Errorf("%w: %d > --max-detach %d", ErrTooManyDetach, len(edgeIDs), e.cfg.MaxDetach)
		}
		for _, edgeID := range edgeIDs {
			if err := e.DeleteEdge(edgeID); err != nil {
				return err
			}
		}
	}
	n, err := e.GetNode(id)
	if err != nil {
		return err
	}
	if err := e.unindexNode(n); err != nil {
		return err
	}
	if err := e.bucket(BucketNodes).Delete(utob(id)); err != nil {
		return err
	}
	delete(e.nodes, id)
	e.Summary.NodesDeleted++
	return nil
}

// Legacy aliases.
func (e *Exec) deleteNode(id uint64) error         { return e.DeleteNode(id, false) }
func (e *Exec) detachDeleteNode(id uint64) error   { return e.DeleteNode(id, true) }
func (e *Exec) updateNodeProperty(id uint64, p map[string]Value) error {
	return e.SetNodeProperties(id, p, false)
}
func (e *Exec) deleteNodeProperty(id uint64, keys ...string) error {
	return e.DeleteNodeProperties(id, keys...)
}
func (e *Exec) addNodeLabels(id uint64, labels ...string) error {
	return e.AddNodeLabels(id, labels...)
}
func (e *Exec) removeNodeLabels(id uint64, labels ...string) error {
	return e.RemoveNodeLabels(id, labels...)
}
func (e *Exec) getNode(id uint64) (*Node, error) { return e.GetNode(id) }

func (e *Exec) NodeCountByLabel(label string) int {
	id, err := e.GetLabelID(label)
	if err != nil {
		return 0
	}
	return e.countLabelsNode(id)
}

func removeString(a []string, s string) []string {
	out := a[:0]
	for _, v := range a {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func hasPrefix(b, p []byte) bool {
	if len(b) < len(p) {
		return false
	}
	for i := range p {
		if b[i] != p[i] {
			return false
		}
	}
	return true
}
