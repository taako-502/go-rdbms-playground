package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/taako-502/go-rdbms-playground/rdb"
)

const peerWipePath = "/internal/replication/wipe"
const sourceHeader = "X-Replica-Source"

func (m *replicaMonitor) loadPeers(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	peerDir := filepath.Join(dir, "replication", "peers")
	if err := os.MkdirAll(peerDir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(peerDir)
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
	m.peerDir = peerDir
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
			w.Header().Set("X-Replica-Directory", url.QueryEscape(db.Directory()))
			w.Header().Set(sourceHeader, db.Position().Source)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if expected := r.Header.Get("X-Replica-Directory"); expected != "" && expected != url.QueryEscape(db.Directory()) {
			clusterResponse(w, 409, "保存先が変更されました。")
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

func callPeer(ctx context.Context, client *http.Client, method string, port int, source string, expectedDir ...string) (string, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d%s", port, peerWipePath)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set(sourceHeader, source)
	if len(expectedDir) > 0 {
		req.Header.Set("X-Replica-Directory", url.QueryEscape(expectedDir[0]))
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent || res.Header.Get(sourceHeader) != source {
		return "", fmt.Errorf("Secondary %d の応答を確認できません (%s)", port, res.Status)
	}
	return url.QueryUnescape(res.Header.Get("X-Replica-Directory"))
}

func (m *replicaMonitor) wipeCluster(db *rdb.DB, stop func(), client *http.Client) http.HandlerFunc {
	return m.clusterAction(db, stop, client)
}

type peerTarget struct {
	Port      int
	Directory string
}
type removalPlan struct {
	Source  string
	Targets []peerTarget
}

func (m *replicaMonitor) ports() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	ports := make([]int, 0, len(m.peers))
	for port := range m.peers {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func (m *replicaMonitor) plan(ctx context.Context, db *rdb.DB, client *http.Client) (removalPlan, error) {
	plan := removalPlan{Source: db.Position().Source, Targets: []peerTarget{}}
	for _, port := range m.ports() {
		dir, err := callPeer(ctx, client, http.MethodGet, port, plan.Source)
		if err != nil || dir == "" {
			return plan, fmt.Errorf("Secondary %d の保存先を確認できません。対象を起動して再実行してください。削除は開始していません。", port)
		}
		plan.Targets = append(plan.Targets, peerTarget{port, dir})
	}
	return plan, nil
}

func (m *replicaMonitor) disablePreview(db *rdb.DB, client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.operations.Lock()
		defer m.operations.Unlock()
		if db.Role() != "primary" {
			clusterResponse(w, 409, "Primaryで実行してください。")
			return
		}
		plan, err := m.plan(r.Context(), db, client)
		if err != nil {
			clusterResponse(w, 409, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(plan)
	}
}

func (m *replicaMonitor) enableReplication(db *rdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.operations.Lock()
		defer m.operations.Unlock()
		if err := db.SetReplication(r.Context(), true); err != nil {
			clusterResponse(w, 500, err.Error())
			return
		}
		if m.peerDir == "" {
			if err := m.loadPeers(db.Directory()); err != nil {
				clusterResponse(w, 500, err.Error())
				return
			}
		}
		clusterResponse(w, 200, "")
	}
}

func (m *replicaMonitor) clusterAction(db *rdb.DB, stop func(), client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !m.operations.TryLock() {
			clusterResponse(w, http.StatusConflict, "複製処理中です。もう一度実行してください。")
			return
		}
		defer m.operations.Unlock()
		if db.Role() != "primary" {
			clusterResponse(w, 409, "Primaryで実行してください。")
			return
		}
		plan, err := m.plan(r.Context(), db, client)
		if err != nil {
			clusterResponse(w, 409, err.Error())
			return
		}
		if stop == nil {
			var confirmed removalPlan
			if err := json.NewDecoder(r.Body).Decode(&confirmed); err != nil || confirmed.Source != plan.Source || !slices.Equal(confirmed.Targets, plan.Targets) {
				clusterResponse(w, 409, "対象が変わりました。もう一度確認してください。")
				return
			}
		}
		ctx := context.WithoutCancel(r.Context())
		wiped := make([]int, 0, len(plan.Targets))
		for _, target := range plan.Targets {
			port := target.Port
			if _, err := callPeer(ctx, client, http.MethodPost, port, plan.Source); err != nil {
				clusterResponse(w, http.StatusConflict, fmt.Sprintf("消去確認済み %v。Secondary %d の消去を確認できません。Primaryは保持されています。", wiped, port))
				return
			}
			wiped = append(wiped, port)
			if err := m.forgetPeer(port); err != nil {
				clusterResponse(w, http.StatusInternalServerError, fmt.Sprintf("消去確認済み %v。対象一覧の更新に失敗しました。", wiped))
				return
			}
		}
		if stop == nil {
			if err := db.SetReplication(ctx, false); err != nil {
				clusterResponse(w, 500, err.Error())
				return
			}
			m.peerDir = ""
			clusterResponse(w, 200, "")
			return
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

// removeSecondary previews and removes only the registered peer at the requested port.
func (m *replicaMonitor) removeSecondary(db *rdb.DB, client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.operations.Lock()
		defer m.operations.Unlock()
		port, err := strconv.Atoi(r.PathValue("port"))
		if err != nil || db.Role() != "primary" || !slices.Contains(m.ports(), port) {
			clusterResponse(w, 404, "登録済みのSecondaryが見つかりません。")
			return
		}
		source := db.Position().Source
		dir, err := callPeer(r.Context(), client, http.MethodGet, port, source)
		if err != nil || dir == "" {
			clusterResponse(w, 409, "対象のSecondaryに接続できません。起動してから再実行してください。")
			return
		}
		target := removalPlan{Source: source, Targets: []peerTarget{{port, dir}}}
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(target)
			return
		}
		var confirmed removalPlan
		if err := json.NewDecoder(r.Body).Decode(&confirmed); err != nil || confirmed.Source != source || !slices.Equal(confirmed.Targets, target.Targets) {
			clusterResponse(w, 409, "削除対象を再確認してください。")
			return
		}
		if _, err := callPeer(context.WithoutCancel(r.Context()), client, http.MethodPost, port, source, dir); err != nil {
			clusterResponse(w, 409, "Secondaryの消去を確認できません。")
			return
		}
		if err := m.forgetPeer(port); err != nil {
			clusterResponse(w, 500, "Secondaryを消去しましたが、登録の削除に失敗しました。")
			return
		}
		clusterResponse(w, 200, "")
	}
}

// Forward the confirmed local identity to the Primary. Its individual removal
// handler wipes this node through peerWipe and updates the persistent registry.
func removeSelf(db *rdb.DB, port int, primary string, client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		plan := removalPlan{Source: db.Position().Source, Targets: []peerTarget{{port, db.Directory()}}}
		endpoint, err := url.JoinPath(primary, "api/settings/replication/secondaries", strconv.Itoa(port))
		if err != nil || primary == "" {
			clusterResponse(w, 409, "PrimaryのURLを確認できません。")
			return
		}
		data, err := json.Marshal(plan)
		if err != nil {
			clusterResponse(w, 500, err.Error())
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodDelete, endpoint, bytes.NewReader(data))
		if err != nil {
			clusterResponse(w, 500, err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			clusterResponse(w, 502, "Primaryで削除の完了を確認できません。")
			return
		}
		defer res.Body.Close()
		var result struct{ Error *string }
		if err := json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&result); err != nil || result.Error == nil {
			clusterResponse(w, 502, "Primaryの応答を確認できません。")
			return
		}
		if res.StatusCode != http.StatusOK || *result.Error != "" {
			message := *result.Error
			if message == "" {
				message = "Primaryで削除できませんでした。"
			}
			clusterResponse(w, 409, message)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"primaryURL": primary})
	}
}
