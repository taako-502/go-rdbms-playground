package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/taako-502/go-rdbms-playground/internal/explorer"
	"github.com/taako-502/go-rdbms-playground/migrations"
	rdb "github.com/taako-502/go-rdbms-playground/rdb"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	data := flag.String("data", "./data", "database directory")
	port := flag.Int("port", 8080, "localhost HTTP port")
	demo := flag.Bool("demo", true, "apply sample migrations and seed an empty users table")
	role := flag.String("role", "", "replication role: primary or secondary")
	upstream := flag.String("upstream", "", "primary URL for a secondary")
	flag.Parse()
	if (*role == "secondary") != (*upstream != "") {
		return fmt.Errorf("secondary requires -upstream; other roles must omit it")
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var db *rdb.DB
	var err error
	if *role == "" {
		db, err = rdb.Open(*data)
	} else {
		db, err = rdb.OpenReplica(*data, *role)
	}
	if err != nil {
		return err
	}
	defer db.Close()
	if *demo && *role == "" {
		if err = seed(ctx, db); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/", explorer.New(db))
	monitor := newReplicaMonitor()
	mux.HandleFunc("GET /api/replication/status", monitor.status(db, *role, *port))
	if *role == "primary" {
		mux.HandleFunc("GET /replication/log", logHandler(db, monitor))
	}
	if *role == "secondary" {
		workerCtx, cancel := context.WithCancel(ctx)
		finished := make(chan struct{})
		go func() { defer close(finished); follow(workerCtx, db, *upstream, *port) }()
		defer func() { cancel(); <-finished }()
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("Database explorer: http://%s (data: %s)", listener.Addr(), *data)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
func seed(ctx context.Context, db *rdb.DB) error {
	if err := migrations.Apply(ctx, db); err != nil {
		return err
	}
	type user struct {
		ID     int64  `rdb:"id"`
		Name   string `rdb:"name"`
		Age    int64  `rdb:"age"`
		Active bool   `rdb:"active"`
	}
	table, err := db.Table[user]("users")
	if err != nil {
		return err
	}
	rows, err := table.All(ctx)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return nil
	}
	for _, u := range []user{{1, "Alice", 20, true}, {2, "Bob", 28, true}, {3, "Carol", 35, false}} {
		if err = table.Insert(ctx, u); err != nil {
			return err
		}
	}
	return nil
}
