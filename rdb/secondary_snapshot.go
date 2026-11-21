package rdb

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// SnapshotSecondary creates a new node directory with the rows, schema and
// checkpoint from one write position. OpenReplica rebuilds indexes on open.
func (db *DB) SnapshotSecondary(ctx context.Context, dest string) (err error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err = db.check(ctx); err != nil {
		return err
	}
	if db.role != "primary" {
		return errors.New("snapshot requires primary")
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(dest))
		}
	}()
	names := []string{"catalog.json", "replica.json"}
	if len(db.history) > 0 {
		names = append(names, "migrations.json")
	}
	for name := range db.tables {
		names = append(names, name+".jsonl")
	}
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = copySnapshotFile(filepath.Join(db.dir, name), filepath.Join(dest, name)); err != nil {
			return err
		}
	}
	if err = syncDir(dest); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dest))
}

func copySnapshotFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}
