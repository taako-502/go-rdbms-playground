package rdb

import (
	"context"
	"errors"
	"testing"
)

func TestCatalogSnapshot(t *testing.T) {
	db, _ := newTestDB(t)
	ctx := context.Background()
	s := testSchema()
	s.Name = "accounts"
	if err := db.CreateTable(ctx, s); err != nil {
		t.Fatal(err)
	}
	tables, err := db.Tables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 || tables[0].Schema.Name != "accounts" {
		t.Fatal(tables)
	}
	tables[1].Schema.Columns[0].Name = "changed"
	tables[1].Indexes[0] = "changed"
	again, err := db.Tables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again[1].Schema.Columns[0].Name != "id" || again[1].Indexes[0] != "id" {
		t.Fatal("catalog aliased")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = db.Tables(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	db.Close()
	if _, err = db.Tables(ctx); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
