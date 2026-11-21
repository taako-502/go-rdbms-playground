package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taako-502/go-rdbms-playground/rdb"
)

func TestClusterWipePreflightAndCompletion(t *testing.T) {
	dir := t.TempDir()
	primary, err := rdb.OpenReplica(dir, "primary")
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	secondaryDir := t.TempDir()
	if err := os.CopyFS(secondaryDir, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	secondary, err := rdb.OpenReplica(secondaryDir, "secondary")
	if err != nil {
		t.Fatal(err)
	}
	defer secondary.Close()
	monitor := newReplicaMonitor()
	if err := monitor.loadPeers(dir); err != nil {
		t.Fatal(err)
	}
	if err := monitor.observe(8081, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	monitor = newReplicaMonitor()
	if err := monitor.loadPeers(dir); err != nil {
		t.Fatal(err)
	}
	primaryStopped, secondaryStopped, online := false, false, false
	peer := peerWipe(secondary, func() { secondaryStopped = true })
	client := &http.Client{Transport: handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !online {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		peer.ServeHTTP(w, r)
	})}}
	wipe := monitor.wipeCluster(primary, func() { primaryStopped = true }, client)
	request := httptest.NewRequest(http.MethodPost, "/api/settings/wipe-cluster", nil)
	response := httptest.NewRecorder()
	wipe(response, request)
	if response.Code != http.StatusConflict || primaryStopped || secondaryStopped {
		t.Fatalf("unexpected offline result: %d %s", response.Code, response.Body)
	}
	online = true
	response = httptest.NewRecorder()
	wipe(response, request)
	if response.Code != http.StatusOK || !primaryStopped || !secondaryStopped {
		t.Fatalf("unexpected online result: %d %s", response.Code, response.Body)
	}
	for _, path := range []string{dir, secondaryDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("node directory still present: %s (%v)", path, err)
		}
		db, err := rdb.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
}

func TestReplicationSwitchPreservesPrimary(t *testing.T) {
	dir := t.TempDir()
	db, err := rdb.OpenExplorer(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	if err := seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	monitor := newReplicaMonitor()
	run := func(h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(method, "/", strings.NewReader(body)))
		return w
	}
	if w := run(monitor.enableReplication(db), "POST", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	source := db.Position().Source
	db.Close()
	db, err = rdb.OpenExplorer(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Role() != "primary" || db.Position().Source != source {
		t.Fatal("primary not restored")
	}
	peerDir := t.TempDir()
	if err := os.CopyFS(peerDir, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	peer, err := rdb.OpenExplorer(peerDir, "secondary")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	stopped := false
	client := &http.Client{Transport: handlerTransport{peerWipe(peer, func() { stopped = true })}}
	if err := monitor.observe(8081, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	preview := run(monitor.disablePreview(db, client), "GET", "")
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), peerDir) || stopped {
		t.Fatal(preview.Body.String())
	}
	disable := monitor.clusterAction(db, nil, client)
	if w := run(disable, "POST", "{}"); w.Code != 409 || stopped || db.Role() != "primary" {
		t.Fatal("unconfirmed deletion")
	}
	if w := run(disable, "POST", preview.Body.String()); w.Code != 200 || !stopped {
		t.Fatal(w.Body.String())
	}
	if _, err := os.Stat(peerDir); !os.IsNotExist(err) {
		t.Fatal("secondary directory still exists")
	}
	db.Close()
	db, err = rdb.OpenExplorer(dir, "primary")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Role() != "" {
		t.Fatal("standalone not restored")
	}
	table, err := db.RawTable("users")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := table.All(ctx)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows %v: %v", rows, err)
	}
	if w := run(monitor.enableReplication(db), "POST", ""); w.Code != 200 || db.Position().Source == source {
		t.Fatal("new lineage not created")
	}
}
