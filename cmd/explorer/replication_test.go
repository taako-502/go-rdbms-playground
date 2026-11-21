package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/taako-502/go-rdbms-playground/rdb"
)

type handlerTransport struct{ handler http.Handler }

func (h handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func TestReplicaCatchUp(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := rdb.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	open := func(dir, role string) *rdb.DB {
		db, err := rdb.OpenReplica(dir, role)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	open(dir, "primary").Close()
	a, b := t.TempDir(), t.TempDir()
	for _, dst := range []string{a, b} {
		if err = os.CopyFS(dst, os.DirFS(dir)); err != nil {
			t.Fatal(err)
		}
	}
	primary := open(dir, "primary")
	first := open(a, "secondary")
	second := open(b, "secondary")
	client := &http.Client{Transport: handlerTransport{logHandler(primary)}}
	syncReplica := func(db *rdb.DB) {
		t.Helper()
		if _, err := pull(ctx, db, "http://primary", client); err != nil {
			t.Fatal(err)
		}
	}
	table, _ := primary.RawTable("users")
	insert := func(id int64) {
		t.Helper()
		if err := table.Insert(ctx, rdb.Row{"id": id, "name": "sample", "active": true, "age": int64(20)}); err != nil {
			t.Fatal(err)
		}
	}
	insert(4)
	syncReplica(first)
	syncReplica(second)
	second.Close()
	insert(5)
	syncReplica(first)
	second = open(b, "secondary")
	syncReplica(second)
	for _, node := range []*rdb.DB{primary, first, second} {
		users, _ := node.RawTable("users")
		rows, err := users.Where(rdb.RawEqual("id", int64(5))).All(ctx)
		if err != nil || len(rows) != 1 || node.Position().LSN != 2 {
			t.Fatalf("position=%v rows=%v err=%v", node.Position(), rows, err)
		}
	}
}
