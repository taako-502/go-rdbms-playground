package main

import (
	"net/http"
	"net/http/httptest"
	"os"
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
