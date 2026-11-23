package walk

import (
	"go.etcd.io/bbolt"
)

var buckets = []string{
	"adj_in", "adj_out", "relation_types", "nodes", "edges", "labels", "node_label", "edge_type",
}

type DB struct {
	bolt *bbolt.DB
}

func Open(path string) (*DB, error) {
	bolt, err := bbolt.Open(path, 0600, &bbolt.Options{})
	if err != nil {
		return nil, err
	}
	db := &DB{bolt: bolt}
	err = db.initBuckets()
	if err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.bolt.Close()
}

func (db *DB) initBuckets() error {
	return db.bolt.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range buckets {
			_, err := tx.CreateBucketIfNotExists([]byte(bucket))
			if err != nil {
				return err
			}
		}
		return nil
	})
}
func (db *DB) Exec(exec string) error {
	return nil
}
func (db *DB) Query(query string) error {
	return nil
}
