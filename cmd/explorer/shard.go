package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	"github.com/taako-502/go-rdbms-playground/migrations"
	"github.com/taako-502/go-rdbms-playground/rdb"
)

const placement = "fnv1a64-mod2-v1"

type member struct {
	ID  string
	URL string
}
type clusterConfig struct {
	Placement string
	Shards    []member
}
type shardNode struct {
	index int
	token string
}
type route struct {
	Hash string
	Slot int
}

func parseCell(t rdb.Type, raw string) (any, error) {
	switch t {
	case rdb.Integer:
		return strconv.ParseInt(raw, 10, 64)
	case rdb.Boolean:
		if raw != "true" && raw != "false" {
			return nil, errors.New("true または false を指定してください")
		}
		return raw == "true", nil
	case rdb.String:
		return raw, nil
	}
	return nil, errors.New("unsupported type")
}
func locate(t rdb.Type, raw string) (route, error) {
	v, e := parseCell(t, raw)
	if e != nil {
		return route{}, e
	}
	var b []byte
	switch x := v.(type) {
	case int64:
		b = binary.BigEndian.AppendUint64(nil, uint64(x))
	case string:
		b = []byte(x)
	case bool:
		b = []byte{0}
		if x {
			b[0] = 1
		}
	}
	h := fnv.New64a()
	h.Write(b)
	n := h.Sum64()
	return route{strconv.FormatUint(n, 10), int(n % 2)}, nil
}
func field(s rdb.Schema, name string) (rdb.Type, error) {
	for _, c := range s.Columns {
		if c.Name == name {
			return c.Type, nil
		}
	}
	return "", errors.New("unknown column")
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func (n *shardNode) wrap(db *rdb.DB, ui http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", ui)
	mux.HandleFunc("GET /api/tables", func(w http.ResponseWriter, r *http.Request) {
		tables, e := db.Tables(r.Context())
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		reply(w, map[string]any{"tables": tables, "shard": n.index})
	})
	mux.HandleFunc("POST /api/tables/{table}/rows", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "ルーターから追加してください", 403)
	})
	mux.HandleFunc("/internal/tables/{table}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Router-Key") != n.token {
			http.Error(w, "router required", 403)
			return
		}
		tables, e := db.Tables(r.Context())
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		for _, t := range tables {
			if t.Schema.Name == r.PathValue("table") {
				n.rows(db, t.Schema, w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
	return mux
}
func (n *shardNode) rows(db *rdb.DB, s rdb.Schema, w http.ResponseWriter, r *http.Request) {
	t, e := db.RawTable(s.Name)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	switch r.Method {
	case "POST":
		var input map[string]*string
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if e != nil || json.Unmarshal(body, &input) != nil || len(input) != len(s.Columns) {
			http.Error(w, "invalid row", 400)
			return
		}
		row := rdb.Row{}
		for _, c := range s.Columns {
			p := input[c.Name]
			if p == nil {
				http.Error(w, "missing "+c.Name, 400)
				return
			}
			v, e := parseCell(c.Type, *p)
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			row[c.Name] = v
			if c.Name == s.PrimaryKey {
				dest, _ := locate(c.Type, *p)
				if dest.Slot != n.index {
					http.Error(w, "wrong shard", 409)
					return
				}
			}
		}
		if e = t.Insert(r.Context(), row); e != nil {
			status := 500
			if errors.Is(e, rdb.ErrDuplicate) {
				status = 409
			}
			http.Error(w, e.Error(), status)
			return
		}
		reply(w, map[string]int{"slot": n.index})
	case "GET":
		var rows []rdb.Row
		q := r.URL.Query()
		if col := q.Get("column"); col != "" {
			typ, e := field(s, col)
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			v, e := parseCell(typ, q.Get("value"))
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			rows, e = t.Where(rdb.RawEqual(col, v)).All(r.Context())
			if e != nil {
				http.Error(w, e.Error(), 500)
				return
			}
		} else {
			rows, e = t.All(r.Context())
			if e != nil {
				http.Error(w, e.Error(), 500)
				return
			}
		}
		for _, row := range rows {
			for k, v := range row {
				if i, ok := v.(int64); ok {
					row[k] = strconv.FormatInt(i, 10)
				}
			}
		}
		reply(w, rows)
	default:
		http.Error(w, "method", 405)
	}
}

func runCluster(dir string, port int, init, demo bool) error {
	if port < 1 || port > 65533 {
		return errors.New("router port must be 1..65533")
	}
	cfg := clusterConfig{Placement: placement}
	for i := 0; i < 2; i++ {
		cfg.Shards = append(cfg.Shards, member{fmt.Sprintf("shard-%d", i), fmt.Sprintf("http://127.0.0.1:%d", port+1+i)})
	}
	path := filepath.Join(dir, "cluster.json")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if init {
		if e := os.Mkdir(dir, 0700); e != nil {
			return fmt.Errorf("create fresh cluster directory: %w", e)
		}
		for _, m := range cfg.Shards {
			db, e := rdb.Open(filepath.Join(dir, m.ID))
			if e != nil {
				return e
			}
			e = migrations.Apply(ctx, db)
			closeErr := db.Close()
			if e != nil {
				return e
			}
			if e := os.WriteFile(filepath.Join(dir, m.ID, "shard-id"), []byte(m.ID), 0600); e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		if e := os.WriteFile(path, data, 0600); e != nil {
			return e
		}
	}
	data, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var saved clusterConfig
	if json.Unmarshal(data, &saved) != nil || !reflect.DeepEqual(cfg, saved) {
		return errors.New("cluster configuration does not match layout or ports")
	}
	token := rand.Text()
	router := clusterRouter{cfg: cfg, token: token, client: &http.Client{Timeout: 10 * time.Second}}
	listeners := make([]net.Listener, 0, 3)
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		l, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+i))
		if e != nil {
			return e
		}
		listeners = append(listeners, l)
	}
	var dbs []*rdb.DB
	defer func() {
		for _, db := range dbs {
			db.Close()
		}
	}()
	for i, m := range cfg.Shards {
		p := filepath.Join(dir, m.ID)
		id, e := os.ReadFile(filepath.Join(p, "shard-id"))
		if e != nil {
			return e
		}
		if string(id) != m.ID {
			return fmt.Errorf("unexpected identity in %s", p)
		}
		db, e := rdb.OpenExplorer(p, "")
		if e != nil {
			return e
		}
		dbs = append(dbs, db)
		tables, e := db.Tables(ctx)
		if e != nil {
			return e
		}
		if i == 0 {
			router.tables = tables
		} else if !reflect.DeepEqual(router.tables, tables) {
			return errors.New("shard schemas differ")
		}
	}
	done := make(chan error, 2)
	for i, db := range dbs {
		nodeCtx, stopNode := context.WithCancel(ctx)
		go func() {
			defer stopNode()
			err := serveNode(nodeCtx, stopNode, db, listeners[i+1], "", &shardNode{i, token})
			if err != nil {
				log.Printf("shard-%d: %v", i, err)
			}
			done <- err
		}()
	}
	if init && demo {
		for _, sample := range []map[string]string{{"id": "1", "name": "Alice", "age": "20", "active": "true"}, {"id": "2", "name": "Bob", "age": "28", "active": "true"}, {"id": "3", "name": "Carol", "age": "35", "active": "false"}} {
			b, _ := json.Marshal(sample)
			dest, _ := locate(rdb.Integer, sample["id"])
			out := router.call(ctx, dest.Slot, "POST", "/internal/tables/users", b)
			if out.Err != nil {
				stop()
				<-done
				<-done
				return out.Err
			}
		}
	}
	server := &http.Server{Handler: router.handler(), ReadHeaderTimeout: 5 * time.Second}
	routerDone := make(chan error, 1)
	go func() { routerDone <- server.Serve(listeners[0]) }()
	fmt.Printf("Shard router: http://%s\n", listeners[0].Addr())
	select {
	case e = <-routerDone:
	case <-ctx.Done():
		server.Close()
		e = <-routerDone
	}
	stop()
	server.Close()
	<-done
	<-done
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
