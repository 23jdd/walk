package walk

import (
	"errors"
	"path/filepath"
)

var ErrFileExt = errors.New("file ext error")

func (db *DB) LoadNodeFromJsonL(path string) error {
	if filepath.Ext(path) != ".jsonl" {
		return ErrFileExt
	}
	return nil
}
func (db *DB) LoadEdgeFromJsonL(path string) error {
	if filepath.Ext(path) != ".jsonl" {
		return ErrFileExt
	}
	return nil
}
