package rdb

import (
	"context"
	"errors"
	"testing"
)

func TestQuery(t *testing.T) {
	db, tbl := newTestDB(t)
	ctx := context.Background()
	for i := range int64(3) {
		if err := tbl.Insert(ctx, testRow(i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		p     RawPredicate
		count int
	}{{RawEqual("id", 1), 1}, {RawEqual("name", "日本語"), 3}, {RawEqual("active", true), 3}, {RawEqual("id", 99), 0}} {
		got, err := tbl.Where(tt.p).All(ctx)
		if err != nil || len(got) != tt.count || got == nil {
			t.Fatalf("%v %v", got, err)
		}
	}
	if got, err := tbl.All(ctx); err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := tbl.Where(RawEqual("id", 1)).Where(RawEqual("id", 2)).All(ctx); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
	if _, err := tbl.Where(RawEqual("unknown", 1)).All(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := tbl.Where(RawEqual("id", "1")).All(ctx); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tbl.All(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := Open(db.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	tbl, err = db2.RawTable("users")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tbl.Where(RawEqual("id", 1)).All(ctx); err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}
