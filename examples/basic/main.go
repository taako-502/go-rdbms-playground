package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/taako-502/go-rdbms-playground/rdb"
)

type User struct {
	ID   int64  `rdb:"id"`
	Name string `rdb:"name"`
	Age  int64  `rdb:"age"`
}

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
	users, err := db.Table[User]("users")
	if err != nil {
		return err
	}
	age, err := users.Column[int64]("age")
	if err != nil {
		return err
	}
	for _, user := range []User{{ID: 1, Name: "Alice", Age: 20}, {ID: 2, Name: "Bob", Age: 20}, {ID: 3, Name: "Carol", Age: 30}} {
		if err = users.Insert(ctx, user); err != nil {
			return err
		}
	}
	rows, err := users.Where(rdb.Equal(age, 20)).All(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("age=20: %d rows\n", len(rows))
	for _, user := range rows {
		fmt.Printf("  %s (%d)\n", user.Name, user.Age)
	}
	if err = users.Insert(ctx, User{ID: 1, Name: "Duplicate", Age: 99}); !errors.Is(err, rdb.ErrDuplicate) {
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
	users, err = reopened.Table[User]("users")
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
