package walk

import (
	"bytes"
	"iter"
	"slices"

	"github.com/23jdd/walk/utils"
	"go.etcd.io/bbolt"
)

func insertLabelsIndex(tx *bbolt.Tx, n *Node, labelId uint64) error {
	nl := mustBucket(tx, "node_label")
	return nl.Put(slices.Concat(utob(labelId), utob(n.id)), nil)
}

func scanLabelsIndex(tx *bbolt.Tx, labelId uint64) iter.Seq[uint64] {
	nl := mustBucket(tx, "node_label")
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
func countLabelsNode(tx *bbolt.Tx, labelId uint64) int {
	return utils.Count(scanLabelsIndex(tx, labelId))
}
func deleteLabelIndex(tx *bbolt.Tx, nodeId uint64, labelId uint64) error {
	nl := mustBucket(tx, "node_label")
	return nl.Delete(slices.Concat(utob(labelId), utob(nodeId)))
}
func deleteNodeAllLabelIndex(tx *bbolt.Tx, n *Node) error {
	id := n.id
	for _, label := range n.Labels {
		labelId, err := getLabel(tx, label)
		if err != nil {
			return err
		}
		err = deleteLabelIndex(tx, id, labelId)
		if err != nil {
			return err
		}
	}
	return nil
}
