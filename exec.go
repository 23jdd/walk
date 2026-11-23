package walk

import "go.etcd.io/bbolt"

type Exec struct {
	tx  *bbolt.Tx
	err error
}

func (e *Exec) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}
