package walk

import (
	"errors"

	"go.etcd.io/bbolt"
)

var ErrLabelNotExists = errors.New("label does not exists")

func getOrCreateLabel(tx *bbolt.Tx, label string) (uint64, error) {
	labels := mustBucket(tx, "labels")
	value := labels.Get([]byte(label))
	if value == nil {
		id, err := labels.NextSequence()
		if err != nil {
			return 0, err
		}
		err = labels.Put([]byte(label), utob(id))
		if err != nil {
			return 0, err
		}
		return id, nil
	} else {
		return btou(value), nil
	}
}
func getOrCreateLabels(tx *bbolt.Tx, labels ...string) ([]uint64, error) {
	ids := make([]uint64, len(labels))
	for index, label := range labels {
		id, err := getOrCreateLabel(tx, label)
		ids[index] = id
		if err != nil {
			return nil, err
		}
	}
	return ids, nil
}
func getLabel(tx *bbolt.Tx, label string) (uint64, error) {
	labels := mustBucket(tx, "labels")
	value := labels.Get([]byte(label))
	if value == nil {
		return 0, ErrLabelNotExists
	} else {
		return btou(value), nil
	}
}
