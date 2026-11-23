package walk

import (
	"bytes"
	"iter"
	"slices"

	"github.com/23jdd/walk/utils"
	"go.etcd.io/bbolt"
)

func insertAdjIndex(tx *bbolt.Tx, edge *Edge, relationId uint64) error {
	out := mustBucket(tx, "adj_out")
	in := mustBucket(tx, "adj_in")
	from := utob(edge.From)
	to := utob(edge.To)
	id := utob(edge.id)
	typeId := utob(relationId)
	err := out.Put(slices.Concat(from, typeId, id), to)
	if err != nil {
		return err
	}
	return in.Put(slices.Concat(to, typeId, id), from)
}

type ScanResult struct {
	From   uint64
	To     uint64
	EdgeId uint64
	TypeId uint64
}

func scanAbjIndexByFrom(tx *bbolt.Tx, from uint64) iter.Seq[ScanResult] {
	out := mustBucket(tx, "adj_out")
	cursor := out.Cursor()
	prefix := utob(from)
	return func(yield func(ScanResult) bool) {
		for k, v := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cursor.Next() {
			typeId, id := btou(k[8:16]), btou(k[16:])
			to := btou(v)
			if !yield(ScanResult{From: from, To: to, EdgeId: id, TypeId: typeId}) {
				return
			}
		}
	}
}
func scanAbjIndexByTo(tx *bbolt.Tx, to uint64) iter.Seq[ScanResult] {
	out := mustBucket(tx, "adj_in")
	cursor := out.Cursor()
	prefix := utob(to)
	return func(yield func(ScanResult) bool) {
		for k, v := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cursor.Next() {
			typeId, id := btou(k[8:16]), btou(k[16:])
			from := btou(v)
			if !yield(ScanResult{From: from, To: to, EdgeId: id, TypeId: typeId}) {
				return
			}
		}
	}
}
func scanAdjIndexByFromAndType(tx *bbolt.Tx, from uint64, typeId uint64) iter.Seq[ScanResult] {
	out := mustBucket(tx, "adj_out")
	cursor := out.Cursor()
	prefix := slices.Concat(utob(from), utob(typeId))
	return func(yield func(ScanResult) bool) {
		for k, v := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cursor.Next() {
			id := btou(k[16:])
			to := btou(v)
			if !yield(ScanResult{From: from, To: to, EdgeId: id, TypeId: typeId}) {
				return
			}
		}
	}
}
func scanAdjIndexByToAndType(tx *bbolt.Tx, to uint64, typeId uint64) iter.Seq[ScanResult] {
	out := mustBucket(tx, "adj_in")
	cursor := out.Cursor()
	prefix := slices.Concat(utob(to), utob(typeId))
	return func(yield func(ScanResult) bool) {
		for k, v := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cursor.Next() {
			id := btou(k[16:])
			from := btou(v)
			if !yield(ScanResult{From: from, To: to, EdgeId: id, TypeId: typeId}) {
				return
			}
		}
	}
}

func haveLinkEdge(tx *bbolt.Tx, id uint64) bool {
	for _ = range scanAbjIndexByFrom(tx, id) {
		return true
	}
	for _ = range scanAbjIndexByTo(tx, id) {
		return true
	}
	return false
}
func allLinkEdge(tx *bbolt.Tx, id uint64) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		for r := range utils.Concat(scanAbjIndexByFrom(tx, id), scanAbjIndexByTo(tx, id)) {
			if !yield(r.EdgeId) {
				return
			}
		}
	}
}
