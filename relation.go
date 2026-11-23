package walk

import (
	"errors"

	"go.etcd.io/bbolt"
)

var ErrRelationNotExists = errors.New("relation  not exists")

func getOrCreateRelationTypes(tx *bbolt.Tx, e *Edge) (uint64, error) {
	relations := mustBucket(tx, "relation_types")
	value := relations.Get([]byte(e.Relation))
	if value == nil {
		id, err := relations.NextSequence()
		if err != nil {
			return 0, err
		}
		err = relations.Put([]byte(e.Relation), utob(id))
		if err != nil {
			return 0, err
		}
		return id, nil
	} else {
		return btou(value), nil
	}
}
func getRelationTypes(tx *bbolt.Tx, relation string) (uint64, error) {
	labels := mustBucket(tx, "relation_types")
	value := labels.Get([]byte(relation))
	if value == nil {
		return 0, ErrLabelNotExists
	} else {
		return btou(value), nil
	}
}
