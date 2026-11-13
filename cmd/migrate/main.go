package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/taako-502/go-rdbms-playground/migrations"
	"github.com/taako-502/go-rdbms-playground/rdb"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	data := flags.String("data", "./data", "database directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: migrate [-data directory]")
	}
	db, err := rdb.Open(*data)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrations.Apply(context.Background(), db); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	fmt.Printf("マイグレーション適用完了: %s（適用済みはスキップ）\n", *data)
	return nil
}
