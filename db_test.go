package rdb

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func newTestDB(t *testing.T) (*DB, *Table) {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.CreateTable(context.Background(), testSchema()); err != nil {
		t.Fatal(err)
	}
	table, err := db.Table("users")
	if err != nil {
		t.Fatal(err)
	}
	return db, table
}
func TestInsertAndReopen(t *testing.T) {
	ctx := context.Background()
	db, tbl := newTestDB(t)
	if err := tbl.Insert(ctx, testRow(1)); err != nil {
		t.Fatal(err)
	}
	if err := tbl.Insert(ctx, testRow(1)); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	if _, err := Open(db.dir); !errors.Is(err, ErrInUse) {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tbl.Insert(ctx, testRow(2)); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	reopened, err := Open(db.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	tbl, err = reopened.Table("users")
	if err != nil {
		t.Fatal(err)
	}
	if err := tbl.Insert(ctx, testRow(1)); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
}
func TestConcurrentInsert(t *testing.T) {
	_, tbl := newTestDB(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- tbl.Insert(context.Background(), testRow(1)) }()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrDuplicate) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal(successes)
	}
}
