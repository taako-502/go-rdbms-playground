package main

import (
	"context"
	rdb "github.com/taako-502/go-rdbms-playground/rdb"
	"testing"
)

func TestDemoSeedOnlyEmpty(t *testing.T) {
	db, err := rdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	table, err := db.RawTable("users")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := table.All(ctx)
	if err != nil || len(rows) != 3 {
		t.Fatal(rows, err)
	}
	other, err := rdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err = other.CreateTable(ctx, rdb.Schema{Name: "custom", Columns: []rdb.Column{{Name: "key", Type: rdb.String}}, PrimaryKey: "key"}); err != nil {
		t.Fatal(err)
	}
	if err = seed(ctx, other); err != nil {
		t.Fatal(err)
	}
	tables, err := other.Tables(ctx)
	if err != nil || len(tables) != 2 || tables[0].Schema.Name != "custom" {
		t.Fatal(tables, err)
	}
}
