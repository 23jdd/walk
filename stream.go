package walk

import (
	"errors"
	"iter"
)

var ErrUnknownKind = errors.New("unknown kind")

type Kind uint8

const (
	NodeKind Kind = iota
	EdgeKind
)

type Entity struct {
	Kind Kind
	Id   uint64
}
type Solution map[string]Entity
type Stream iter.Seq[Solution]
