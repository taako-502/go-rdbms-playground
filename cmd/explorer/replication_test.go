package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	primary := open(dir, "primary")
	a, b := filepath.Join(dir, "secondary-a"), filepath.Join(dir, "secondary-b")
	for _, dst := range []string{a, b} {
		if err := primary.SnapshotSecondary(ctx, dst); err != nil {
			t.Fatal(err)
		}
		if err := primary.SnapshotSecondary(ctx, dst); !os.IsExist(err) {
			t.Fatalf("existing snapshot: %v", err)
		}
	}
	first := open(a, "secondary")
	second := open(b, "secondary")
	monitor := newReplicaMonitor()
	client := &http.Client{Transport: handlerTransport{logHandler(primary, monitor)}}
	syncReplica := func(db *rdb.DB, port int) {
		t.Helper()
		if _, err := pull(ctx, db, "http://primary", client, port); err != nil {
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
	syncReplica(first, 8081)
	syncReplica(second, 8082)
	second.Close()
	insert(5)
	syncReplica(first, 8081)
	syncReplica(first, 8081)
	w := httptest.NewRecorder()
	monitor.status(primary, 8080, "")(w, httptest.NewRequest("GET", "/api/replication/status", nil))
	var snapshot struct{ Nodes []nodeStatus }
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	nodes := snapshot.Nodes
	if len(nodes) != 3 || nodes[0].LSN != "2" || nodes[1].Lag != "0" || nodes[2].Port != 8082 || nodes[2].Lag != "2" || nodes[2].LastSeen == nil {
		t.Fatalf("unexpected snapshot: %+v", nodes)
	}
	second = open(b, "secondary")
	syncReplica(second, 8082)
	for _, node := range []*rdb.DB{primary, first, second} {
		users, _ := node.RawTable("users")
		rows, err := users.Where(rdb.RawEqual("id", int64(5))).All(ctx)
		if err != nil || len(rows) != 1 || node.Position().LSN != 2 {
			t.Fatalf("position=%v rows=%v err=%v", node.Position(), rows, err)
		}
	}
	for _, mode := range []rdb.ResetMode{rdb.ResetRows, rdb.ResetAll} {
		if err := primary.Reset(ctx, mode); err != nil {
			t.Fatal(err)
		}
		syncReplica(first, 8081)
		syncReplica(second, 8082)
		for _, node := range []*rdb.DB{primary, first, second} {
			users, err := node.RawTable("users")
			if mode == rdb.ResetAll {
				if err == nil {
					t.Fatal("table definition survived reset")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			rows, err := users.All(ctx)
			if err != nil || len(rows) != 0 {
				t.Fatalf("rows after reset: %v, %v", rows, err)
			}
		}
	}

}

func TestSecondaryPrimaryLink(t *testing.T) {
	db, err := rdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w := httptest.NewRecorder()
	upstream := primaryLink("https://user:pass@localhost:8080/?token=secret")
	newReplicaMonitor().status(db, 8081, upstream)(w, httptest.NewRequest("GET", "/api/replication/status", nil))
	var status struct{ PrimaryURL string }
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.PrimaryURL != "https://localhost:8080/" {
		t.Fatalf("unexpected Primary URL: %q", status.PrimaryURL)
	}
}
