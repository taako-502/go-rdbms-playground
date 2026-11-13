package rdb

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestIndexMatchesScan(t *testing.T) {
	db, tbl := newTestDB(t)
	ctx := context.Background()
	for i := int64(0); i < 40; i++ {
		r := testRow(i)
		r["active"] = i%2 == 0
		if err := tbl.Insert(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	before, err := tbl.Where(Equal("active", true)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.CreateIndex(ctx, "users", "active"); err != nil {
		t.Fatal(err)
	}
	after, err := tbl.Where(Equal("active", true)).All(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("%v %v", after, err)
	}
	if err = tbl.Insert(ctx, testRow(40)); err != nil {
		t.Fatal(err)
	}
	if got, err := tbl.Where(Equal("active", true)).All(ctx); err != nil || len(got) != 21 {
		t.Fatalf("%v %v", got, err)
	}
	if err = tbl.Insert(ctx, testRow(40)); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	if err = db.CreateIndex(ctx, "users", "id"); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	if err = db.CreateIndex(ctx, "users", "name"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(db.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	tbl, err = again.Table("users")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tbl.Where(Equal("name", "日本語")).All(ctx); err != nil || len(got) != 41 {
		t.Fatalf("%v %v", got, err)
	}
}
