// Package explorer exposes a local browser UI for an embedded database.
package explorer

import (
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"

	rdb "github.com/taako-502/go-rdbms-playground/rdb"
)

//go:embed static/*
var assets embed.FS

type server struct{ db *rdb.DB }

func New(db *rdb.DB) http.Handler {
	s := &server{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tables", s.tables)
	mux.HandleFunc("GET /api/tables/{table}/indexes/{column}", s.index)
	mux.HandleFunc("GET /api/tables/{table}/rows", s.rows)
	mux.HandleFunc("POST /api/tables/{table}/rows", s.insert)
	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServerFS(static))
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "別のサイトからの書き込みは許可されていません。")
	}))
	protected := protection.Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		protected.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, value any, options ...json.Options) {
	b, err := json.Marshal(value, options...)
	if err != nil {
		http.Error(w, "response encoding failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rdb.ErrDuplicate):
		writeError(w, 409, "同じ主キーの行がすでに存在します。主キーを変更してください。")
	case errors.Is(err, rdb.ErrNotFound):
		writeError(w, 404, "テーブルが見つかりません。")
	case errors.Is(err, rdb.ErrRow):
		writeError(w, 400, err.Error())
	case errors.Is(err, rdb.ErrNeedsRecovery):
		writeError(w, 503, "DBの再起動が必要です。行が保存されている可能性があるため、再起動後に主キーで存在確認してください。")
	default:
		writeError(w, 500, "データベース操作に失敗しました。")
	}
}
func (s *server) tables(w http.ResponseWriter, r *http.Request) {
	tables, err := s.db.Tables(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tables": tables})
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	index, err := s.db.InspectIndex(r.Context(), r.PathValue("table"), r.PathValue("column"))
	if errors.Is(err, rdb.ErrNotFound) {
		writeError(w, 404, "インデックスが見つかりません。")
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	// Preserve int64 keys and byte offsets across the JavaScript boundary.
	writeJSON(w, 200, index, json.StringifyNumbers(true))
}

// JSON integers are decimal strings on the HTTP boundary, so JavaScript never
// rounds int64 values through its IEEE-754 Number representation.
func browserRow(row rdb.Row) map[string]any {
	out := make(map[string]any, len(row))
	for k, v := range row {
		if n, ok := v.(int64); ok {
			out[k] = strconv.FormatInt(n, 10)
		} else {
			out[k] = v
		}
	}
	return out
}
func (s *server) rows(w http.ResponseWriter, r *http.Request) {
	table, err := s.db.RawTable(r.PathValue("table"))
	if err != nil {
		fail(w, err)
		return
	}
	rows, err := table.All(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, browserRow(row))
	}
	writeJSON(w, 200, map[string]any{"rows": result})
}
func (s *server) insert(w http.ResponseWriter, r *http.Request) {
	typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "application/json" {
		writeError(w, 415, "JSON形式で送信してください。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, 413, "入力データが大きすぎます（上限1 MiB）。")
		} else {
			writeError(w, 400, "入力を読み込めませんでした。")
		}
		return
	}
	// Keep null distinct from an empty string: JSON null is not supported by RDB.
	var input map[string]*string
	if err = json.Unmarshal(body, &input); err != nil || input == nil {
		writeError(w, 400, "各カラムの値を文字列として指定してください。")
		return
	}
	tables, err := s.db.Tables(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	var schema *rdb.Schema
	for _, t := range tables {
		if t.Schema.Name == r.PathValue("table") {
			schema = &t.Schema
			break
		}
	}
	if schema == nil {
		fail(w, rdb.ErrNotFound)
		return
	}
	if len(input) != len(schema.Columns) {
		writeError(w, 400, "すべてのカラムを過不足なく指定してください。")
		return
	}
	row := make(rdb.Row, len(input))
	for _, c := range schema.Columns {
		value, ok := input[c.Name]
		if !ok || value == nil {
			writeError(w, 400, fmt.Sprintf("%sの値が必要です。NULLは使用できません。", c.Name))
			return
		}
		switch c.Type {
		case rdb.Integer:
			n, err := strconv.ParseInt(*value, 10, 64)
			if err != nil {
				writeError(w, 400, fmt.Sprintf("%sにはint64の範囲の整数を入力してください。", c.Name))
				return
			}
			row[c.Name] = n
		case rdb.Boolean:
			if *value != "true" && *value != "false" {
				writeError(w, 400, fmt.Sprintf("%sはtrueまたはfalseで指定してください。", c.Name))
				return
			}
			row[c.Name] = *value == "true"
		case rdb.String:
			row[c.Name] = *value
		}
	}
	table, err := s.db.RawTable(schema.Name)
	if err != nil {
		fail(w, err)
		return
	}
	if err = table.Insert(r.Context(), row); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"row": browserRow(row)})
}
