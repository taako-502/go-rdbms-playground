package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/taako-502/go-rdbms-playground/rdb"
)

type observation struct {
	lsn  uint64
	seen time.Time
}
type replicaMonitor struct {
	mu    sync.Mutex
	peers map[int]observation
}

func newReplicaMonitor() *replicaMonitor { return &replicaMonitor{peers: make(map[int]observation)} }
func (m *replicaMonitor) observe(port int, lsn uint64, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers[port] = observation{lsn, now}
}

type nodeStatus struct {
	Role     string
	Port     int
	LSN      string
	Lag      string
	LastSeen *time.Time
}

func (m *replicaMonitor) status(db *rdb.DB, role string, port int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		pos := db.Position()
		nodes := []nodeStatus{{Role: role, Port: port, LSN: strconv.FormatUint(pos.LSN, 10)}}
		for port, peer := range m.peers {
			nodes = append(nodes, nodeStatus{Role: "secondary", Port: port, LSN: strconv.FormatUint(peer.lsn, 10), Lag: strconv.FormatUint(pos.LSN-peer.lsn, 10), LastSeen: &peer.seen})
		}
		m.mu.Unlock()
		sort.Slice(nodes[1:], func(i, j int) bool { return nodes[i+1].Port < nodes[j+1].Port })
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(struct {
			Role  string
			Nodes []nodeStatus
		}{role, nodes})
	}
}
