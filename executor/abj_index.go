package executor

import (
	"bytes"
	"iter"
	"slices"

	"github.com/23jdd/walk/utils"
)

func (e *Exec) insertAdjIndex(edge *Edge, relationId uint64) error {
	out := e.mustBucket("adj_out")
	in := e.mustBucket("adj_in")
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

func (e *Exec) scanAbjIndexByFrom(from uint64) iter.Seq[ScanResult] {
	out := e.mustBucket("adj_out")
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
func (e *Exec) scanAbjIndexByTo(to uint64) iter.Seq[ScanResult] {
	out := e.mustBucket("adj_in")
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
func (e *Exec) scanAdjIndexByFromAndType(from uint64, typeId uint64) iter.Seq[ScanResult] {
	out := e.mustBucket("adj_out")
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
func (e *Exec) scanAdjIndexByToAndType(to uint64, typeId uint64) iter.Seq[ScanResult] {
	out := e.mustBucket("adj_in")
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

func (e *Exec) haveLinkEdge(id uint64) bool {
	for _ = range e.scanAbjIndexByFrom(id) {
		return true
	}
	for _ = range e.scanAbjIndexByTo(id) {
		return true
	}
	return false
}
func (e *Exec) allLinkEdge(id uint64) iter.Seq[uint64] {
	return func(yield func(uint64) bool) {
		for r := range utils.Concat(e.scanAbjIndexByFrom(id), e.scanAbjIndexByTo(id)) {
			if !yield(r.EdgeId) {
				return
			}
		}
	}
}
