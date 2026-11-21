package rdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWipeAllowsMigrationAtSamePath(t *testing.T) {
	ctx := context.Background()
	db, _ := newTestDB(t)
	dir := db.Directory()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	primary, err := OpenReplica(dir, "primary")
	if err != nil {
		t.Fatal(err)
	}
	table, err := primary.RawTable("users")
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Insert(ctx, testRow(1)); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := primary.Wipe(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "README.md" {
		t.Fatalf("remaining files: %v", entries)
	}
	fresh, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := fresh.Migrate(ctx, Migration{Version: 1, Operations: []Operation{CreateTableOp(testSchema())}}); err != nil {
		t.Fatal(err)
	}
}
