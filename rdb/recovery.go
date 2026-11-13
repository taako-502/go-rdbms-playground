package rdb

import (
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

type diskIndex struct {
	Version int    `json:"version"`
	Table   string `json:"table"`
	Column  string `json:"column"`
	Degree  int    `json:"degree"`
	Tree    *btree `json:"tree"`
}

func (db *DB) indexPath(table, column string) string {
	return filepath.Join(db.dir, table+"."+column+".index.json")
}
func (db *DB) saveIndex(st *tableState, col string) error {
	disk := diskIndex{1, st.def.Schema.Name, col, treeDegree, st.indexes[col]}
	return atomicJSONHook(db.indexPath(disk.Table, col), disk, func(point string) error { return db.hit("index_" + col + "_" + point) })
}
func (db *DB) saveIndexes(st *tableState) error {
	for _, col := range st.def.Indexes {
		if err := db.saveIndex(st, col); err != nil {
			return err
		}
	}
	return nil
}

// The authoritative row scan has already built expected indexes. Compare every
// key and offset, not merely row count, so stale or incorrectly pointed indexes
// are repaired even when the number of rows has not changed.
func (db *DB) recoverIndexes(st *tableState) error {
	for _, col := range st.def.Indexes {
		b, err := os.ReadFile(db.indexPath(st.def.Schema.Name, col))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var disk diskIndex
		valid := err == nil && json.Unmarshal(b, &disk) == nil && disk.Version == 1 && disk.Table == st.def.Schema.Name && disk.Column == col && disk.Degree == treeDegree && disk.Tree != nil
		c, _ := st.def.Schema.column(col)
		if valid {
			valid = disk.Tree.validate(c.Type) == nil
		}
		if valid {
			valid = reflect.DeepEqual(disk.Tree.entries(), st.indexes[col].entries())
		}
		if valid {
			st.indexes[col] = disk.Tree
		} else if err := db.saveIndex(st, col); err != nil {
			return err
		}
	}
	return nil
}
