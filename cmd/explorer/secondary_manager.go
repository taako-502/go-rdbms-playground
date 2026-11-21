package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taako-502/go-rdbms-playground/rdb"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type secondaryManager struct {
	ctx         context.Context
	workers     *sync.WaitGroup
	primary     *rdb.DB
	monitor     *replicaMonitor
	primaryPort int
}

func (m secondaryManager) directory(port int) string {
	root := m.primary.Directory()
	if filepath.Base(root) == "primary" {
		root = filepath.Dir(root)
	}
	return filepath.Join(root, fmt.Sprintf("secondary-%d", port))
}

func (m secondaryManager) restore() error {
	for _, port := range m.monitor.ports() {
		marker, err := os.ReadFile(filepath.Join(m.monitor.peerDir, strconv.Itoa(port)))
		if err != nil {
			return err
		}
		if string(marker) == "managed\n" {
			if err := m.start(m.ctx, port, false, false); err != nil {
				log.Printf("Secondary %d: %v", port, err)
			}
		}
	}
	return nil
}

func (m secondaryManager) add(w http.ResponseWriter, r *http.Request) {
	var config struct{ Port int }
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil || config.Port < 1 || config.Port > 65535 {
		clusterResponse(w, 400, "ポートは1〜65535の整数で指定してください。")
		return
	}
	m.monitor.operations.Lock()
	defer m.monitor.operations.Unlock()
	if m.primary.Role() != "primary" {
		clusterResponse(w, 409, "Primaryで実行してください。")
		return
	}
	port := config.Port
	marker, err := os.ReadFile(filepath.Join(m.monitor.peerDir, strconv.Itoa(port)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		clusterResponse(w, 500, err.Error())
		return
	}
	if err == nil && string(marker) != "managed\n" {
		clusterResponse(w, 409, "このポートには手動起動のSecondaryが登録されています。")
		return
	}
	create := errors.Is(err, os.ErrNotExist)
	if err := m.start(r.Context(), port, create, true); err != nil {
		clusterResponse(w, 409, err.Error())
		return
	}
	clusterResponse(w, 201, "")
}

func (m secondaryManager) start(ctx context.Context, port int, create, allowRecreate bool) error {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	launched := false
	defer func() {
		if !launched {
			listener.Close()
		}
	}()
	dir := m.directory(port)
	fresh := create
	if !create && allowRecreate {
		_, err := os.Lstat(dir)
		fresh = errors.Is(err, os.ErrNotExist)
	}
	if fresh {
		if err := m.primary.SnapshotSecondary(ctx, dir); err != nil {
			return err
		}
		defer func() {
			if !launched {
				os.RemoveAll(dir)
			}
		}()
	} else if _, err := os.Stat(filepath.Join(dir, "replica.json")); err != nil {
		return err
	}
	db, err := rdb.OpenReplica(dir, "secondary")
	if err != nil {
		return err
	}
	defer func() {
		if !launched {
			db.Close()
		}
	}()
	if db.Position().Source != m.primary.Position().Source {
		return errors.New("複製元IDが一致しません。")
	}
	if create {
		path := filepath.Join(m.monitor.peerDir, strconv.Itoa(port))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer func() {
			if !launched {
				os.Remove(path)
			}
		}()
		_, err = file.WriteString("managed\n")
		if err == nil {
			err = file.Sync()
		}
		if err = errors.Join(err, file.Close()); err != nil {
			return err
		}
		if err = syncPeerDirectory(m.monitor.peerDir); err != nil {
			return err
		}
		m.monitor.mu.Lock()
		m.monitor.peers[port] = observation{}
		m.monitor.mu.Unlock()
	}
	child, stop := context.WithCancel(m.ctx)
	launched = true
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer db.Close()
		defer stop()
		if err := serveNode(child, stop, db, listener, fmt.Sprintf("http://127.0.0.1:%d", m.primaryPort)); err != nil {
			log.Printf("Secondary %d: %v", port, err)
		}
	}()
	return nil
}
