package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taako-502/go-rdbms-playground/migrations"
	"github.com/taako-502/go-rdbms-playground/rdb"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClusterRouting(t *testing.T) {
	var nodes [2]http.Handler
	const token = "local-test"
	c := clusterRouter{token: token}
	for i, name := range []string{"first", "second"} {
		db, e := rdb.Open(t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { db.Close() })
		if e = migrations.Apply(context.Background(), db); e != nil {
			t.Fatal(e)
		}
		c.tables, e = db.Tables(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		c.cfg.Shards = append(c.cfg.Shards, member{name, "http://" + name})
		nodes[i] = (&shardNode{i, token}).wrap(db, http.NotFoundHandler())
	}
	offline := false
	c.client = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		i := 0
		if r.URL.Host == "second" {
			i = 1
			if offline {
				return nil, errors.New("offline")
			}
		}
		w := httptest.NewRecorder()
		nodes[i].ServeHTTP(w, r)
		return w.Result(), nil
	})}
	router := c.handler()
	call := func(h http.Handler, method, path, body string, status int) string {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != status {
			t.Fatalf("%s %s: got %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	row := `{"id":"9007199254740993","age":"20","name":"Alice","active":"true"}`
	preview := call(router, "GET", "/preview/users?value=9007199254740993", "", 200)
	if saved := call(router, "POST", "/rows/users", row, 200); saved != preview {
		t.Fatalf("preview %s saved %s", preview, saved)
	}
	call(router, "POST", "/rows/users", row, 409)
	var dest route
	if e := json.Unmarshal([]byte(preview), &dest); e != nil {
		t.Fatal(e)
	}
	var groups [2][]map[string]any
	search := func(path string) {
		t.Helper()
		if e := json.Unmarshal([]byte(call(router, "GET", path, "", 200)), &groups); e != nil {
			t.Fatal(e)
		}
	}
	search("/rows/users?column=id&value=9007199254740993")
	if len(groups[dest.Slot]) != 1 || groups[dest.Slot][0]["id"] != "9007199254740993" || groups[1-dest.Slot] != nil {
		t.Fatal(groups)
	}
	search("/rows/users?column=age&value=20")
	if len(groups[dest.Slot]) != 1 || groups[1-dest.Slot] == nil || len(groups[1-dest.Slot]) != 0 {
		t.Fatal(groups)
	}
	call(nodes[dest.Slot], "POST", "/api/tables/users/rows", row, 403)
	call(nodes[dest.Slot], "POST", "/internal/tables/users", row, 403)
	req := httptest.NewRequest("POST", "/internal/tables/users", strings.NewReader(row))
	req.Header.Set("X-Router-Key", token)
	w := httptest.NewRecorder()
	nodes[1-dest.Slot].ServeHTTP(w, req)
	if w.Code != 409 {
		b, _ := io.ReadAll(w.Result().Body)
		t.Fatalf("wrong shard: %d %s", w.Code, b)
	}
	offline = true
	call(router, "GET", "/rows/users", "", 502)
}
