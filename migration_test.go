package rdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func migrations() []Migration {
	return []Migration{{Version: 2, Operations: []Operation{CreateIndexOp("users", "active")}}, {Version: 1, Operations: []Operation{CreateTableOp(testSchema())}}}
}
func TestMigrations(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	plan := migrations()
	if err = db.Migrate(ctx, plan...); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx, plan...); err != nil {
		t.Fatal(err)
	}
	if len(db.history) != 2 || db.history[0].Version != 1 {
		t.Fatal(db.history)
	}
	plan[1].Operations = []Operation{CreateIndexOp("users", "name")}
	if err = db.Migrate(ctx, plan...); !errors.Is(err, ErrMigration) {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); !errors.Is(err, ErrMigration) {
		t.Fatal(err)
	}
}
func TestMigrationResume(t *testing.T) {
	for _, point := range []string{"migration_changed", "migration_history_temp_synced", "migration_history_renamed", "index_active_temp_synced"} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			db, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			// One migration containing two changes exercises resumption within a version.
			plan := []Migration{{Version: 1, Operations: []Operation{CreateTableOp(testSchema()), CreateIndexOp("users", "active")}}}
			injected := errors.New("injected")
			db.testHook = func(p string) error {
				if p == point {
					return injected
				}
				return nil
			}
			if err = db.Migrate(ctx, plan...); !errors.Is(err, injected) {
				t.Fatal(err)
			}
			db.Close()
			db, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.Migrate(ctx, plan...); err != nil {
				t.Fatal(err)
			}
			if len(db.history) != 1 || db.tables["users"].indexes["active"] == nil {
				t.Fatal("migration incomplete")
			}
			tbl, err := db.Table("users")
			if err != nil {
				t.Fatal(err)
			}
			if err = tbl.Insert(ctx, testRow(1)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMigrationConflict(t *testing.T) {
	db, _ := newTestDB(t)
	s := testSchema()
	s.Columns[1].Type = Boolean
	err := db.Migrate(context.Background(), Migration{Version: 1, Operations: []Operation{CreateTableOp(s)}})
	if !errors.Is(err, ErrMigration) {
		t.Fatal(err)
	}
	if len(db.history) != 0 {
		t.Fatal("recorded failed migration")
	}
}
func TestInvalidMigrationHistory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "migrations.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if db, err := Open(dir); !errors.Is(err, ErrCorrupt) {
		if db != nil {
			db.Close()
		}
		t.Fatal(err)
	}
}
