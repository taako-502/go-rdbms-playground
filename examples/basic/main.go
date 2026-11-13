package main

import (
	"context"
	"errors"
	"fmt"
	rdb "github.com/taako-502/go-rdbms-playground/rdb"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir, err := os.MkdirTemp("", "go-rdbms-example-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ctx := context.Background()
	db, err := rdb.Open(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	err = db.Migrate(ctx,
		rdb.Migration{Version: 1, Operations: []rdb.Operation{rdb.CreateTableOp(rdb.Schema{Name: "users", Columns: []rdb.Column{{Name: "id", Type: rdb.Integer}, {Name: "name", Type: rdb.String}, {Name: "age", Type: rdb.Integer}}, PrimaryKey: "id"})}},
		rdb.Migration{Version: 2, Operations: []rdb.Operation{rdb.CreateIndexOp("users", "age")}},
	)
	if err != nil {
		return err
	}
	users, err := db.Table("users")
	if err != nil {
		return err
	}
	for _, row := range []rdb.Row{{"id": 1, "name": "Alice", "age": 20}, {"id": 2, "name": "Bob", "age": 20}, {"id": 3, "name": "Carol", "age": 30}} {
		if err = users.Insert(ctx, row); err != nil {
			return err
		}
	}
	rows, err := users.Where(rdb.Equal("age", 20)).All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("age=20: %d rows\n", len(rows))
	if err = users.Insert(ctx, rdb.Row{"id": 1, "name": "Duplicate", "age": 99}); !errors.Is(err, rdb.ErrDuplicate) {
		return fmt.Errorf("expected duplicate error, got %v", err)
	}
	fmt.Println("duplicate primary key: rejected")
	if err = db.Close(); err != nil {
		return err
	}
	reopened, err := rdb.Open(dir)
	if err != nil {
		return err
	}
	defer reopened.Close()
	users, err = reopened.Table("users")
	if err != nil {
		return err
	}
	rows, err = users.All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("after reopen: %d rows\n", len(rows))
	return nil
}
