package executor

import (
	"bytes"
	"iter"
	"slices"

	"github.com/23jdd/walk/utils"
)

func (e *Exec) insertRelationIndex(edge *Edge, typeId uint64) error {
	nl := e.mustBucket("edge_type")
	return nl.Put(slices.Concat(utob(typeId), utob(edge.id)), nil)
}

func (e *Exec) scanRelationIndex(typeId uint64) iter.Seq[uint64] {
	nl := e.mustBucket("edge_type")
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
func (e *Exec) countRelationEdge(typeId uint64) int {
	return utils.Count(e.scanRelationIndex(typeId))
}
