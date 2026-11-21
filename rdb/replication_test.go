package rdb

import (
	"context"
	"errors"
	"testing"
)

func TestReplicaJournalRecovery(t *testing.T) {
	for _, point := range []string{"log_synced", "row_synced", "before_checkpoint"} {
		t.Run(point, func(t *testing.T) {
			db, _ := newTestDB(t)
			dir := db.dir
			db.Close()
			primary, err := OpenReplica(dir, "primary")
			if err != nil {
				t.Fatal(err)
			}
			primary.testHook = func(p string) error {
				if point == p {
					return errors.New("interrupted")
				}
				return nil
			}
			users, _ := primary.RawTable("users")
			if err := users.Insert(context.Background(), testRow(7)); err == nil {
				t.Fatal("expected interruption")
			}
			primary.Close()
			primary, err = OpenReplica(dir, "primary")
			if err != nil {
				t.Fatal(err)
			}
			defer primary.Close()
			users, _ = primary.RawTable("users")
			rows, err := users.All(context.Background())
			if err != nil || len(rows) != 1 || primary.Position().LSN != 1 {
				t.Fatalf("rows=%v position=%v err=%v", rows, primary.Position(), err)
			}
		})
	}
}
