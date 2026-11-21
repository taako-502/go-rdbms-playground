package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/taako-502/go-rdbms-playground/rdb"
)

const peerWipePath = "/internal/replication/wipe"
const sourceHeader = "X-Replica-Source"

func (m *replicaMonitor) loadPeers(dir string) error {
	m.peerDir = filepath.Join(dir, "replication", "peers")
	if err := os.MkdirAll(m.peerDir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(m.peerDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		port, err := strconv.Atoi(entry.Name())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid registered secondary port: %s", entry.Name())
		}
		m.peers[port] = observation{}
	}
	return nil
}

func (m *replicaMonitor) persistPeer(port int) error {
	if m.peerDir == "" {
		return nil
	}
	file, err := os.OpenFile(filepath.Join(m.peerDir, strconv.Itoa(port)), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncPeerDirectory(m.peerDir)
}

func syncPeerDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func (m *replicaMonitor) forgetPeer(port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peerDir != "" {
		if err := os.Remove(filepath.Join(m.peerDir, strconv.Itoa(port))); err != nil {
			return err
		}
		if err := syncPeerDirectory(m.peerDir); err != nil {
			return err
		}
	}
	delete(m.peers, port)
	return nil
}

func clusterResponse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func peerWipe(db *rdb.DB, stop func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if source := r.Header.Get(sourceHeader); source == "" || source != db.Position().Source {
			clusterResponse(w, http.StatusConflict, "複製元IDが一致しません。")
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set(sourceHeader, db.Position().Source)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err := db.Wipe(r.Context()); err != nil {
			clusterResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set(sourceHeader, db.Position().Source)
		w.WriteHeader(http.StatusNoContent)
		stop()
	}
}

func callPeer(ctx context.Context, client *http.Client, method string, port int, source string) error {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d%s", port, peerWipePath)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set(sourceHeader, source)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent || res.Header.Get(sourceHeader) != source {
		return fmt.Errorf("Secondary %d の応答を確認できません (%s)", port, res.Status)
	}
	return nil
}

func (m *replicaMonitor) wipeCluster(db *rdb.DB, stop func(), client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !m.operations.TryLock() {
			clusterResponse(w, http.StatusConflict, "複製処理中です。もう一度実行してください。")
			return
		}
		defer m.operations.Unlock()
		m.mu.Lock()
		ports := make([]int, 0, len(m.peers))
		for port := range m.peers {
			ports = append(ports, port)
		}
		m.mu.Unlock()
		sort.Ints(ports)
		source := db.Position().Source
		for _, port := range ports {
			if err := callPeer(r.Context(), client, http.MethodGet, port, source); err != nil {
				clusterResponse(w, http.StatusConflict, fmt.Sprintf("Secondary %d を確認できません。対象を起動してから再実行してください。削除は開始していません。", port))
				return
			}
		}
		ctx := context.WithoutCancel(r.Context())
		wiped := make([]int, 0, len(ports))
		for _, port := range ports {
			if err := callPeer(ctx, client, http.MethodPost, port, source); err != nil {
				clusterResponse(w, http.StatusConflict, fmt.Sprintf("消去確認済み %v。Secondary %d の消去を確認できません。Primaryは保持されています。", wiped, port))
				return
			}
			wiped = append(wiped, port)
			if err := m.forgetPeer(port); err != nil {
				clusterResponse(w, http.StatusInternalServerError, fmt.Sprintf("消去確認済み %v。対象一覧の更新に失敗しました。", wiped))
				return
			}
		}
		if err := db.Wipe(ctx); err != nil {
			clusterResponse(w, http.StatusInternalServerError, fmt.Sprintf("消去確認済み %v。Primaryの初期化に失敗しました。", wiped))
			return
		}
		clusterResponse(w, http.StatusOK, "")
		stop()
	}
}

func localPeerClient() *http.Client {
	return &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
