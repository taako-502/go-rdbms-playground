package rdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const wipeMarker = ".wipe-pending"

// Directory returns the absolute path of the node's storage directory.
func (db *DB) Directory() string { return db.dir }

// Wipe removes this node's persisted database state and closes the database.
func (db *DB) Wipe(ctx context.Context) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(db.dir, wipeMarker), true); err != nil {
		return db.fail(err)
	}
	for name, table := range db.tables {
		delete(db.tables, name)
		if err := table.rows.close(); err != nil {
			return db.fail(err)
		}
	}
	if err := finishWipe(db.dir); err != nil {
		return db.fail(err)
	}
	return db.closeLocked()
}

// Resume before reading the catalog so an interrupted wipe can finish safely.
func finishWipe(dir string) error {
	marker := filepath.Join(dir, wipeMarker)
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		switch {
		case name == "replication":
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		case name == "catalog.json", name == "migrations.json", name == "replica.json", name == "reset.json",
			strings.HasSuffix(name, ".jsonl"), strings.HasSuffix(name, ".index.json"), strings.HasPrefix(name, ".pending-"):
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	if err := os.Remove(marker); err != nil {
		return err
	}
	return syncDir(dir)
}
