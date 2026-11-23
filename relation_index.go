package walk

import (
	"bytes"
	"iter"
	"slices"

	"github.com/23jdd/walk/utils"
	"go.etcd.io/bbolt"
)

func insertRelationIndex(tx *bbolt.Tx, e *Edge, typeId uint64) error {
	nl := mustBucket(tx, "edge_type")
	return nl.Put(slices.Concat(utob(typeId), utob(e.id)), nil)
}

func scanRelationIndex(tx *bbolt.Tx, typeId uint64) iter.Seq[uint64] {
	nl := mustBucket(tx, "edge_type")
	cursor := nl.Cursor()
	prefix := utob(typeId)
	return func(yield func(uint64) bool) {
		for k, _ := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cursor.Next() {
			nodeId := btou(k[8:16])
			if !yield(nodeId) {
				return
			}
		}
	}
}
func countRelationEdge(tx *bbolt.Tx, typeId uint64) int {
	return utils.Count(scanRelationIndex(tx, typeId))
}
