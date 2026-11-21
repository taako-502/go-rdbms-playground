package rdb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type ResetMode string

const (
	ResetRows ResetMode = "rows"
	ResetAll  ResetMode = "all"
)

// Reset clears every table. ResetAll also clears the catalog and migration history.
func (db *DB) Reset(ctx context.Context, mode ResetMode) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return err
	}
	if mode != ResetRows && mode != ResetAll {
		return ErrCorrupt
	}
	switch db.role {
	case "secondary":
		return ErrReplicaReadOnly
	case "primary":
		return db.writeEntry(LogEntry{Reset: mode})
	}
	// Persist the intent before modifying any of the tables.
	if err := atomicJSON(filepath.Join(db.dir, "reset.json"), mode); err != nil {
		return db.fail(err)
	}
	return db.fail(db.finishReset())
}

func (db *DB) finishReset() error {
	path := filepath.Join(db.dir, "reset.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var mode ResetMode
	if err = json.Unmarshal(data, &mode); err != nil {
		return err
	}
	if err = db.applyReset(mode); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return syncDir(db.dir)
}

func (db *DB) applyReset(mode ResetMode) error {
	if mode != ResetRows && mode != ResetAll {
		return ErrCorrupt
	}
	for _, table := range db.tables {
		if err := table.rows.file.Truncate(0); err != nil {
			return err
		}
		if err := table.rows.file.Sync(); err != nil {
			return err
		}
		for column := range table.indexes {
			table.indexes[column] = newTree()
		}
		if err := db.saveIndexes(table); err != nil {
			return err
		}
	}
	if mode == ResetRows {
		return nil
	}
	if err := atomicJSON(filepath.Join(db.dir, "migrations.json"), []appliedMigration{}); err != nil {
		return err
	}
	empty := catalog{Version: db.cat.Version}
	if err := atomicJSON(filepath.Join(db.dir, "catalog.json"), empty); err != nil {
		return err
	}
	db.cat = empty
	db.history = nil
	for name, table := range db.tables {
		delete(db.tables, name)
		if err := table.rows.close(); err != nil {
			return err
		}
	}
	return nil
}
