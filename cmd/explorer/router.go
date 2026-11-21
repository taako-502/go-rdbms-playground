package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/taako-502/go-rdbms-playground/rdb"
)

//go:embed router.html
var routerPage []byte

type clusterRouter struct {
	cfg    clusterConfig
	tables []rdb.TableInfo
	token  string
	client *http.Client
}

func (c *clusterRouter) handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(routerPage)
	})
	m.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"tables": c.tables, "shards": c.cfg.Shards})
	})
	for _, pattern := range []string{"GET /preview/{table}", "GET /rows/{table}", "POST /rows/{table}"} {
		m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			for _, t := range c.tables {
				if t.Schema.Name == r.PathValue("table") {
					c.handleTable(t.Schema, w, r)
					return
				}
			}
			http.NotFound(w, r)
		})
	}
	return http.NewCrossOriginProtection().Handler(m)
}

type shardResult struct {
	Slot   int
	Rows   []map[string]any
	Status int
	Err    error
}

func (c *clusterRouter) call(ctx context.Context, i int, method, path string, body []byte) shardResult {
	out := shardResult{Slot: i, Status: 502}
	req, e := http.NewRequestWithContext(ctx, method, c.cfg.Shards[i].URL+path, bytes.NewReader(body))
	if e != nil {
		out.Err = e
		return out
	}
	req.Header.Set("X-Router-Key", c.token)
	res, e := c.client.Do(req)
	if e != nil {
		out.Err = e
		return out
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		out.Status = res.StatusCode
		out.Err = fmt.Errorf("%s: %s", c.cfg.Shards[i].ID, msg)
		return out
	}
	if method == "GET" {
		out.Err = json.NewDecoder(res.Body).Decode(&out.Rows)
	}
	return out
}
func (c *clusterRouter) handleTable(s rdb.Schema, w http.ResponseWriter, r *http.Request) {
	typ, _ := field(s, s.PrimaryKey)
	q := r.URL.Query()
	if r.Pattern == "GET /preview/{table}" {
		dest, e := locate(typ, q.Get("value"))
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		reply(w, dest)
		return
	}
	path := "/internal/tables/" + s.Name
	if r.Method == "POST" {
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		var row map[string]*string
		if e != nil || json.Unmarshal(b, &row) != nil || row[s.PrimaryKey] == nil {
			http.Error(w, "主キーを指定してください", 400)
			return
		}
		dest, e := locate(typ, *row[s.PrimaryKey])
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		result := c.call(r.Context(), dest.Slot, "POST", path, b)
		if result.Err != nil {
			http.Error(w, result.Err.Error(), result.Status)
			return
		}
		reply(w, dest)
		return
	}
	targets := []int{0, 1}
	col := q.Get("column")
	if col != "" {
		kind, e := field(s, col)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		if _, e = parseCell(kind, q.Get("value")); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		if col == s.PrimaryKey {
			dest, _ := locate(typ, q.Get("value"))
			targets = []int{dest.Slot}
		}
	}
	path += "?" + url.Values{"column": {col}, "value": {q.Get("value")}}.Encode()
	ch := make(chan shardResult, len(targets))
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	for _, i := range targets {
		go func() { ch <- c.call(ctx, i, "GET", path, nil) }()
	}
	results := [2]*[]map[string]any{}
	for range targets {
		out := <-ch
		if out.Err != nil {
			http.Error(w, out.Err.Error(), out.Status)
			return
		}
		results[out.Slot] = &out.Rows
	}
	reply(w, results)
}
