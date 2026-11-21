package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/taako-502/go-rdbms-playground/rdb"
)

func logHandler(db *rdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		after, err := strconv.ParseUint(q.Get("after"), 10, 64)
		if err != nil {
			http.Error(w, "invalid after", 400)
			return
		}
		entries, err := db.Entries(r.Context(), rdb.Position{Source: q.Get("source"), LSN: after})
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(entries)
	}
}
func pull(ctx context.Context, db *rdb.DB, upstream string, client *http.Client) (int, error) {
	p := db.Position()
	endpoint := fmt.Sprintf("%s/replication/log?source=%s&after=%d", strings.TrimRight(upstream, "/"), url.QueryEscape(p.Source), p.LSN)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return 0, err
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return 0, fmt.Errorf("primary returned %s", res.Status)
	}
	var entries []rdb.LogEntry
	if err = json.NewDecoder(res.Body).Decode(&entries); err != nil {
		return 0, err
	}
	return len(entries), db.ApplyEntries(ctx, p, entries)
}
func follow(ctx context.Context, db *rdb.DB, upstream string) {
	client := &http.Client{Timeout: 15 * time.Second}
	for ctx.Err() == nil {
		n, err := pull(ctx, db, upstream, client)
		if err != nil {
			log.Printf("replication: %v", err)
		} else if n > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
