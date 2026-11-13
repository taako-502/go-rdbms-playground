package rdb

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

// The child exits without Close or deferred cleanup, exercising actual process
// interruption. This models process crashes, not hardware power-loss behavior.
func TestCrashHelper(t *testing.T) {
	dir := os.Getenv("RDB_CRASH_DIR")
	if dir == "" {
		return
	}
	point := os.Getenv("RDB_CRASH_POINT")
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := db.Table("users")
	if err != nil {
		t.Fatal(err)
	}
	if point == "partial_row" {
		f, err := os.OpenFile(db.rowsPath("users"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.WriteString(`{"id":2,"name":`); err != nil {
			t.Fatal(err)
		}
		if err = f.Sync(); err != nil {
			t.Fatal(err)
		}
		os.Exit(77)
	}
	db.testHook = func(p string) error {
		if p == point {
			os.Exit(77)
		}
		return nil
	}
	if point == "migration_changed" || point == "migration_history_temp_synced" || point == "migration_history_renamed" {
		err = db.Migrate(context.Background(), Migration{Version: 1, Operations: []Operation{CreateTableOp(testSchema()), CreateIndexOp("users", "active")}})
	} else {
		err = tbl.Insert(context.Background(), testRow(2))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash point not reached")
}
func TestProcessCrashRecovery(t *testing.T) {
	points := []string{"partial_row", "row_synced", "index_id_temp_synced", "index_id_renamed", "index_active_temp_synced", "index_active_renamed", "migration_changed", "migration_history_temp_synced", "migration_history_renamed"}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			db, tbl := newTestDB(t)
			ctx := context.Background()
			if err := db.CreateIndex(ctx, "users", "active"); err != nil {
				t.Fatal(err)
			}
			if err := tbl.Insert(ctx, testRow(1)); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
			cmd.Env = append(os.Environ(), "RDB_CRASH_DIR="+db.dir, "RDB_CRASH_POINT="+point)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 77 {
				t.Fatalf("%v: %s", err, out)
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
			want := 2
			if point == "partial_row" || point == "migration_changed" || point == "migration_history_temp_synced" || point == "migration_history_renamed" {
				want = 1
			}
			got, err := tbl.Where(Equal("active", true)).All(ctx)
			if err != nil || len(got) != want {
				t.Fatalf("%v %v", got, err)
			}
			if err = again.Migrate(ctx, Migration{Version: 1, Operations: []Operation{CreateTableOp(testSchema()), CreateIndexOp("users", "active")}}); err != nil {
				t.Fatal(err)
			}
			if err = tbl.Insert(ctx, testRow(3)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
