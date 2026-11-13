package explorer

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rdb "github.com/taako-502/go-rdbms-playground/rdb"
)

func testDB(t *testing.T) (*rdb.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := rdb.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	err = db.CreateTable(context.Background(), rdb.Schema{Name: "items", Columns: []rdb.Column{{Name: "id", Type: rdb.Integer}, {Name: "title", Type: rdb.String}, {Name: "active", Type: rdb.Boolean}}, PrimaryKey: "id"})
	if err != nil {
		t.Fatal(err)
	}
	return db, dir
}
func send(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestInsertReadAndReopen(t *testing.T) {
	db, dir := testDB(t)
	h := New(db)
	w := send(h, "GET", "/api/tables", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send(h, "GET", "/api/tables/items/rows", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rows":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	const payload = `{"id":"9223372036854775807","title":"<script>alert(1)</script>日本語","active":"false"}`
	w = send(h, "POST", "/api/tables/items/rows", payload)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	check := func(h http.Handler) {
		t.Helper()
		w := send(h, "GET", "/api/tables/items/rows", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var got struct {
			Rows []map[string]any `json:"rows"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Rows) != 1 || got.Rows[0]["id"] != "9223372036854775807" || got.Rows[0]["active"] != false || got.Rows[0]["title"] != "<script>alert(1)</script>日本語" {
			t.Fatal(got)
		}
	}
	check(h)
	w = send(h, "POST", "/api/tables/items/rows", payload)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := rdb.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	check(New(again))
}
func TestInvalidInsert(t *testing.T) {
	db, _ := testDB(t)
	h := New(db)
	for _, payload := range []string{
		`{"id":"9223372036854775808","title":"a","active":"true"}`,
		`{"id":"1.5","title":"a","active":"true"}`,
		`{"id":"1","title":"a","active":"yes"}`,
		`{"id":"1","title":null,"active":"true"}`,
		`{"id":"1","title":"a"}`,
		`{"id":1,"title":"a","active":"true"}`,
		`{"id":"1","title":"a","active":"true","extra":"x"}`,
		`{"id":"1","id":"2","title":"a","active":"true"}`,
		`null`,
	} {
		w := send(h, "POST", "/api/tables/items/rows", payload)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", payload, w.Code, w.Body.String())
		}
	}
	w := send(h, "POST", "/api/tables/items/rows", `{"id":"-9223372036854775808","title":"","active":"true"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = send(h, "POST", "/api/tables/missing/rows", `{"id":"1"}`)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = send(h, "POST", "/api/tables/items/rows", strings.Repeat(" ", 1<<20+1))
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
}
func TestCrossOriginAndStatic(t *testing.T) {
	db, _ := testDB(t)
	h := New(db)
	r := httptest.NewRequest("POST", "http://localhost/api/tables/items/rows", strings.NewReader(`{"id":"1","title":"a","active":"true"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://other.example")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		w := send(h, "GET", path, "")
		if w.Code != 200 || w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal(path, w.Code)
		}
	}
	r = httptest.NewRequest("POST", "/api/tables/items/rows", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "text/plain")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
}
