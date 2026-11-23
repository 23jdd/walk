package executor

import (
	"errors"
)

var ErrLabelNotExists = errors.New("label does not exists")

func (e *Exec) getOrCreateLabel(label string) (uint64, error) {
	labels := e.mustBucket("labels")
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
func (e *Exec) getOrCreateLabels(labels ...string) ([]uint64, error) {
	ids := make([]uint64, len(labels))
	for index, label := range labels {
		id, err := e.getOrCreateLabel(label)
		ids[index] = id
		if err != nil {
			return nil, err
		}
	}
	return ids, nil
}
func (e *Exec) getLabel(label string) (uint64, error) {
	labels := e.mustBucket("labels")
	value := labels.Get([]byte(label))
	if value == nil {
		return 0, ErrLabelNotExists
	} else {
		return btou(value), nil
	}
}
