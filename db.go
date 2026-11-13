package rdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type tableDef struct {
	Schema  Schema   `json:"schema"`
	Indexes []string `json:"indexes"`
}
type catalog struct {
	Version int        `json:"version"`
	Tables  []tableDef `json:"tables"`
}
type tableState struct {
	def  tableDef
	rows *rowStore
}
type DB struct {
	mu     sync.Mutex
	dir    string
	cat    catalog
	tables map[string]*tableState
	closed bool
	failed bool
	// testHook is package-private fault injection; callbacks run under mu.
	testHook func(string) error
}
type Table struct {
	db   *DB
	name string
}

var opened = struct {
	sync.Mutex
	paths map[string]bool
}{paths: map[string]bool{}}

func Open(dir string) (*DB, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	opened.Lock()
	if opened.paths[abs] {
		opened.Unlock()
		return nil, ErrInUse
	}
	opened.paths[abs] = true
	opened.Unlock()
	db := &DB{dir: abs, cat: catalog{Version: 1}, tables: map[string]*tableState{}}
	success := false
	defer func() {
		if !success {
			db.Close()
		}
	}()
	if err = syncDir(filepath.Dir(abs)); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(abs, "catalog.json"))
	if errors.Is(err, os.ErrNotExist) {
		err = atomicJSON(filepath.Join(abs, "catalog.json"), db.cat)
	} else if err == nil {
		err = json.Unmarshal(b, &db.cat)
	}
	if err != nil {
		return nil, err
	}
	if db.cat.Version != 1 {
		return nil, fmt.Errorf("%w: catalog version", ErrCorrupt)
	}
	for _, def := range db.cat.Tables {
		if err = def.Schema.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		name := def.Schema.Name
		if db.tables[name] != nil {
			return nil, fmt.Errorf("%w: duplicate table", ErrCorrupt)
		}
		path := db.rowsPath(name)
		if _, err = os.Stat(path); err != nil {
			return nil, fmt.Errorf("%w: rows for %s: %v", ErrCorrupt, name, err)
		}
		rows, e := openRows(path, def.Schema)
		if e != nil {
			return nil, e
		}
		st := &tableState{def: def, rows: rows}
		db.tables[name] = st
		seen := map[any]bool{}
		if err = rows.scan(context.Background(), true, func(r record) error {
			key := r.Row[def.Schema.PrimaryKey]
			if seen[key] {
				return fmt.Errorf("%w: duplicate primary key", ErrCorrupt)
			}
			seen[key] = true
			return nil
		}); err != nil {
			return nil, err
		}
	}
	success = true
	return db, nil
}
func (db *DB) rowsPath(name string) string { return filepath.Join(db.dir, name+".jsonl") }
func (db *DB) check(ctx context.Context) error {
	if db.closed {
		return ErrClosed
	}
	if db.failed {
		return ErrNeedsRecovery
	}
	return ctx.Err()
}
func (db *DB) fail(err error) error {
	if err != nil {
		db.failed = true
	}
	return err
}
func (db *DB) hit(point string) error {
	if db.testHook != nil {
		return db.testHook(point)
	}
	return nil
}
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	var errs []error
	for _, st := range db.tables {
		errs = append(errs, st.rows.close())
	}
	opened.Lock()
	delete(opened.paths, db.dir)
	opened.Unlock()
	return errors.Join(errs...)
}
func (db *DB) Table(name string) (*Table, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(context.Background()); err != nil {
		return nil, err
	}
	if db.tables[name] == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return &Table{db, name}, nil
}
func (db *DB) CreateTable(ctx context.Context, s Schema) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.createTable(ctx, s)
}
func (db *DB) createTable(ctx context.Context, s Schema) error {
	if err := db.check(ctx); err != nil {
		return err
	}
	if err := s.Validate(); err != nil {
		return err
	}
	if db.tables[s.Name] != nil {
		return ErrExists
	}
	s.Columns = append([]Column(nil), s.Columns...)
	rows, err := openRows(db.rowsPath(s.Name), s)
	if err != nil {
		return db.fail(err)
	}
	info, err := rows.file.Stat()
	if err != nil {
		rows.close()
		return db.fail(err)
	}
	if info.Size() != 0 {
		rows.close()
		return db.fail(fmt.Errorf("%w: nonempty orphan rows", ErrCorrupt))
	}
	st := &tableState{def: tableDef{Schema: s}, rows: rows}
	db.tables[s.Name] = st
	if err = rows.file.Sync(); err != nil {
		return db.fail(err)
	}
	db.cat.Tables = append(db.cat.Tables, st.def)
	if err = atomicJSON(filepath.Join(db.dir, "catalog.json"), db.cat); err != nil {
		return db.fail(err)
	}
	return nil
}
func (t *Table) Insert(ctx context.Context, row Row) error {
	db := t.db
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return err
	}
	st := db.tables[t.name]
	r, err := st.def.Schema.validateRow(row)
	if err != nil {
		return err
	}
	pk := st.def.Schema.PrimaryKey
	err = st.rows.scan(ctx, false, func(existing record) error {
		if existing.Row[pk] == r[pk] {
			return ErrDuplicate
		}
		return nil
	})
	if err != nil {
		return err
	}
	if _, err = st.rows.append(r); err != nil {
		return db.fail(err)
	}
	return db.fail(db.hit("row_synced"))
}
