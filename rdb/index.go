package rdb

import (
	"context"
	"fmt"
	"path/filepath"
)

func (db *DB) buildIndexes(ctx context.Context, st *tableState, recoverTail bool) error {
	st.indexes = map[string]*btree{}
	for _, name := range st.def.Indexes {
		if _, err := st.def.Schema.column(name); err != nil {
			return fmt.Errorf("%w: index column %s", ErrCorrupt, name)
		}
		if st.indexes[name] != nil {
			return fmt.Errorf("%w: duplicate index", ErrCorrupt)
		}
		st.indexes[name] = newTree()
	}
	pk := st.def.Schema.PrimaryKey
	if st.indexes[pk] == nil {
		return fmt.Errorf("%w: missing primary index definition", ErrCorrupt)
	}
	return st.rows.scan(ctx, recoverTail, func(r record) error {
		if len(st.indexes[pk].search(makeKey(r.Row[pk]))) > 0 {
			return fmt.Errorf("%w: duplicate primary key", ErrCorrupt)
		}
		for col, idx := range st.indexes {
			idx.insert(makeKey(r.Row[col]), r.Offset)
		}
		return nil
	})
}
func (db *DB) CreateIndex(ctx context.Context, table, column string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.createIndex(ctx, table, column)
}
func (db *DB) createIndex(ctx context.Context, table, column string) error {
	if err := db.check(ctx); err != nil {
		return err
	}
	st := db.tables[table]
	if st == nil {
		return ErrNotFound
	}
	if _, err := st.def.Schema.column(column); err != nil {
		return err
	}
	if st.indexes[column] != nil {
		return ErrExists
	}
	idx := newTree()
	if err := st.rows.scan(ctx, false, func(r record) error { idx.insert(makeKey(r.Row[column]), r.Offset); return nil }); err != nil {
		return err
	}
	st.def.Indexes = append(st.def.Indexes, column)
	st.indexes[column] = idx
	for i := range db.cat.Tables {
		if db.cat.Tables[i].Schema.Name == table {
			db.cat.Tables[i] = st.def
		}
	}
	if err := atomicJSON(filepath.Join(db.dir, "catalog.json"), db.cat); err != nil {
		return db.fail(err)
	}
	return db.fail(db.saveIndex(st, column))
}
