package executor

import (
	"bytes"
	"iter"
	"slices"

	"github.com/23jdd/walk/utils"
)

func (e *Exec) insertLabelsIndex(n *Node, labelId uint64) error {
	nl := e.mustBucket("node_label")
	return nl.Put(slices.Concat(utob(labelId), utob(n.id)), nil)
}

func (e *Exec) scanLabelsIndex(labelId uint64) iter.Seq[uint64] {
	nl := e.mustBucket("node_label")
	cursor := nl.Cursor()
	prefix := utob(labelId)
	return func(yield func(uint64) bool) {
		for k, _ := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cursor.Next() {
			nodeId := btou(k[8:16])
			if !yield(nodeId) {
				return
			}
		}
	}
}
func (e *Exec) countLabelsNode(labelId uint64) int {
	return utils.Count(e.scanLabelsIndex(labelId))
}
func (e *Exec) deleteLabelIndex(nodeId uint64, labelId uint64) error {
	nl := e.mustBucket("node_label")
	return nl.Delete(slices.Concat(utob(labelId), utob(nodeId)))
}
func (e *Exec) deleteNodeAllLabelIndex(n *Node) error {
	id := n.id
	for _, label := range n.Labels {
		labelId, err := e.getLabel(label)
		if err != nil {
			return err
		}
		err = e.deleteLabelIndex(id, labelId)
		if err != nil {
			return err
		}
	}
	return nil
}
