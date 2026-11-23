package executor

import (
	"errors"
)

var ErrRelationNotExists = errors.New("relation  not exists")

func (e *Exec) getOrCreateRelationTypes(edge *Edge) (uint64, error) {
	relations := e.mustBucket("relation_types")
	value := relations.Get([]byte(edge.Relation))
	if value == nil {
		id, err := relations.NextSequence()
		if err != nil {
			return 0, err
		}
		err = relations.Put([]byte(edge.Relation), utob(id))
		if err != nil {
			return 0, err
		}
		return id, nil
	} else {
		return btou(value), nil
	}
}
func (e *Exec) getRelationTypes(relation string) (uint64, error) {
	labels := e.mustBucket("relation_types")
	value := labels.Get([]byte(relation))
	if value == nil {
		return 0, ErrLabelNotExists
	} else {
		return btou(value), nil
	}
}
