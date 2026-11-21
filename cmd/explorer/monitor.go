package main

import (
	"encoding/json"
	"net/http"
	"net/url"
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
	operations sync.Mutex
	peerDir    string
	mu         sync.Mutex
	peers      map[int]observation
}

func newReplicaMonitor() *replicaMonitor { return &replicaMonitor{peers: make(map[int]observation)} }
func (m *replicaMonitor) observe(port int, lsn uint64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.peers[port]; !exists {
		if err := m.persistPeer(port); err != nil {
			return err
		}
	}
	m.peers[port] = observation{lsn, now}
	return nil
}

type nodeStatus struct {
	Role     string
	Port     int
	LSN      string
	Lag      string
	LastSeen *time.Time
}

func (m *replicaMonitor) status(db *rdb.DB, port int, upstream string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		role := db.Role()
		pos := db.Position()
		nodes := []nodeStatus{{Role: role, Port: port, LSN: strconv.FormatUint(pos.LSN, 10)}}
		for port, peer := range m.peers {
			if peer.seen.IsZero() {
				nodes = append(nodes, nodeStatus{Role: "secondary", Port: port, LSN: "—", LastSeen: &peer.seen})
				continue
			}
			nodes = append(nodes, nodeStatus{Role: "secondary", Port: port, LSN: strconv.FormatUint(peer.lsn, 10), Lag: strconv.FormatUint(pos.LSN-peer.lsn, 10), LastSeen: &peer.seen})
		}
		m.mu.Unlock()
		sort.Slice(nodes[1:], func(i, j int) bool { return nodes[i+1].Port < nodes[j+1].Port })
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(struct {
			Role       string
			Nodes      []nodeStatus
			PrimaryURL string `json:",omitempty"`
		}{role, nodes, upstream})
	}
}

func primaryLink(upstream string) string {
	u, err := url.Parse(upstream)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}
