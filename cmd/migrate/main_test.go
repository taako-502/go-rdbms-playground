package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/taako-502/go-rdbms-playground/migrations"
	"github.com/taako-502/go-rdbms-playground/rdb"
)

func TestMigrateExistingDataAndRepeat(t *testing.T) {
	dir := t.TempDir()
	db, err := rdb.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// 旧Explorerのように履歴なしで定義とデータを作る。
	if err := db.CreateTable(ctx, *migrations.All()[0].Operations[0].Schema); err != nil {
		t.Fatal(err)
	}
	table, err := db.RawTable("users")
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Insert(ctx, rdb.Row{"id": int64(42), "name": "kept", "age": int64(20), "active": true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-data", dir}); err != nil {
		t.Fatal(err)
	}
	history, err := os.ReadFile(filepath.Join(dir, "migrations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-data", dir}); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filepath.Join(dir, "migrations.json"))
	if err != nil || string(history) != string(again) {
		t.Fatalf("history changed: %v", err)
	}
	db, err = rdb.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	table, err = db.RawTable("users")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := table.Where(rdb.RawEqual("age", int64(20))).All(ctx)
	if err != nil || len(rows) != 1 || rows[0]["name"] != "kept" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestMigrateFresh(t *testing.T) {
	if err := run([]string{"-data", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"unexpected"}); err == nil {
		t.Fatal("accepted unexpected argument")
	}
}
