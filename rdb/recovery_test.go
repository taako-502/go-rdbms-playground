package rdb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedInsertRecovery(t *testing.T) {
	for _, point := range []string{"row_synced", "index_id_temp_synced", "index_id_renamed", "index_active_temp_synced", "index_active_renamed"} {
		t.Run(point, func(t *testing.T) {
			db, tbl := newTestDB(t)
			ctx := context.Background()
			if err := db.CreateIndex(ctx, "users", "active"); err != nil {
				t.Fatal(err)
			}
			if err := tbl.Insert(ctx, testRow(1)); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected failure")
			db.testHook = func(p string) error {
				if p == point {
					return injected
				}
				return nil
			}
			if err := tbl.Insert(ctx, testRow(2)); !errors.Is(err, injected) {
				t.Fatal(err)
			}
			if _, err := tbl.All(ctx); !errors.Is(err, ErrNeedsRecovery) {
				t.Fatal(err)
			}
			if err := tbl.Insert(ctx, testRow(3)); !errors.Is(err, ErrNeedsRecovery) {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			again, err := Open(db.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			tbl, err = again.RawTable("users")
			if err != nil {
				t.Fatal(err)
			}
			if got, err := tbl.Where(RawEqual("active", true)).All(ctx); err != nil || len(got) != 2 {
				t.Fatalf("%v %v", got, err)
			}
			if err := tbl.Insert(ctx, testRow(2)); !errors.Is(err, ErrDuplicate) {
				t.Fatal(err)
			}
		})
	}
}
func TestRepairIndexDamage(t *testing.T) {
	for _, kind := range []string{"missing", "invalid_json", "wrong_offset", "nil_root", "wrong_type", "missing_entry"} {
		t.Run(kind, func(t *testing.T) {
			db, tbl := newTestDB(t)
			ctx := context.Background()
			for i := range int64(3) {
				if err := tbl.Insert(ctx, testRow(i)); err != nil {
					t.Fatal(err)
				}
			}
			path := db.indexPath("users", "id")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var disk diskIndex
			if err = json.Unmarshal(b, &disk); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				err = os.Remove(path)
			case "invalid_json":
				err = os.WriteFile(path, []byte("{"), 0600)
			default:
				switch kind {
				case "wrong_offset":
					disk.Tree.Root.Entries[0].Offsets[0] = 1
				case "nil_root":
					disk.Tree.Root = nil
				case "wrong_type":
					disk.Tree.Root.Entries[0].Key.Type = String
				case "missing_entry":
					disk.Tree.Root.Entries = disk.Tree.Root.Entries[1:]
				}
				err = atomicJSON(path, disk)
			}
			if err != nil {
				t.Fatal(err)
			}
			again, err := Open(db.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			tbl, err = again.RawTable("users")
			if err != nil {
				t.Fatal(err)
			}
			for i := range 3 {
				got, err := tbl.Where(RawEqual("id", i)).All(ctx)
				if err != nil || len(got) != 1 {
					t.Fatalf("%v %v", got, err)
				}
			}
		})
	}
}
func TestOpenRemovesIncompleteTail(t *testing.T) {
	db, tbl := newTestDB(t)
	if err := tbl.Insert(context.Background(), testRow(1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	f, err := os.OpenFile(filepath.Join(db.dir, "users.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`{"id":2`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	again, err := Open(db.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	tbl, err = again.RawTable("users")
	if err != nil {
		t.Fatal(err)
	}
	if err = tbl.Insert(context.Background(), testRow(2)); err != nil {
		t.Fatal(err)
	}
}
func TestCorruptRowsRejected(t *testing.T) {
	for _, kind := range []string{"complete_invalid", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			db, tbl := newTestDB(t)
			if err := tbl.Insert(context.Background(), testRow(1)); err != nil {
				t.Fatal(err)
			}
			db.Close()
			path := db.rowsPath("users")
			if kind == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				data := []byte("broken\n")
				if kind == "duplicate" {
					data, err = json.Marshal(testRow(1))
					if err != nil {
						t.Fatal(err)
					}
					data = append(data, '\n')
				}
				if _, err = f.Write(data); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
			if again, err := Open(db.dir); !errors.Is(err, ErrCorrupt) {
				if again != nil {
					again.Close()
				}
				t.Fatal(err)
			}
		})
	}
}

func TestMissingOrEmptyCatalogRejected(t *testing.T) {
	for _, kind := range []string{"missing", "null", "empty"} {
		t.Run(kind, func(t *testing.T) {
			db, tbl := newTestDB(t)
			if err := tbl.Insert(context.Background(), testRow(1)); err != nil {
				t.Fatal(err)
			}
			db.Close()
			path := filepath.Join(db.dir, "catalog.json")
			var err error
			if kind == "missing" {
				err = os.Remove(path)
			} else {
				data := "null"
				if kind == "empty" {
					data = "{}"
				}
				err = os.WriteFile(path, []byte(data), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if again, err := Open(db.dir); !errors.Is(err, ErrCorrupt) {
				if again != nil {
					again.Close()
				}
				t.Fatal(err)
			}
		})
	}
}
