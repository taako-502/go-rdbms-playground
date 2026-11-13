package rdb

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type testUser struct {
	ID     int64  `rdb:"id"`
	Name   string `rdb:"name"`
	Active bool   `rdb:"active"`
}

func TestTypedQuery(t *testing.T) {
	ctx := context.Background()
	db, _ := newTestDB(t)
	users, err := db.Table[testUser]("users")
	if err != nil {
		t.Fatal(err)
	}
	name, err := users.Column[string]("name")
	if err != nil {
		t.Fatal(err)
	}
	active, err := users.Column[bool]("active")
	if err != nil {
		t.Fatal(err)
	}
	id, err := users.Column[int64]("id")
	if err != nil {
		t.Fatal(err)
	}
	want := []testUser{{math.MinInt64, "日本語<&>", false}, {math.MaxInt64, "日本語<&>", true}}
	for _, u := range want {
		if err = users.Insert(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err = users.Insert(ctx, want[0]); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	for _, indexed := range []bool{false, true} {
		if indexed {
			if err = db.CreateIndex(ctx, "users", "name"); err != nil {
				t.Fatal(err)
			}
		}
		got, err := users.Where(Equal(name, "日本語<&>")).All(ctx)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%v %v", got, err)
		}
	}
	got, err := users.Where(Equal(active, false)).All(ctx)
	if err != nil || len(got) != 1 || got[0] != want[0] {
		t.Fatalf("%v %v", got, err)
	}
	got, err = users.Where(Equal(id, 0)).All(ctx)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err = users.Where(Equal(id, 0)).Where(Equal(id, 1)).All(ctx); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = users.All(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = users.Insert(canceled, testUser{ID: 2}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = users.Insert(ctx, testUser{ID: 2, Name: string([]byte{0xff})}); !errors.Is(err, ErrRow) {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = users.All(ctx); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	db, err = Open(db.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users, err = db.Table[testUser]("users")
	if err != nil {
		t.Fatal(err)
	}
	got, err = users.All(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %v", got, err)
	}
}
func TestTypedSchemaValidation(t *testing.T) {
	db, _ := newTestDB(t)
	check := func(err error) {
		t.Helper()
		if !errors.Is(err, ErrSchema) {
			t.Fatal(err)
		}
	}
	_, err := db.Table[*testUser]("users")
	check(err)
	_, err = db.Table[Row]("users")
	check(err)
	_, err = db.Table[struct {
		ID int64 `rdb:"id"`
	}]("users")
	check(err)
	_, err = db.Table[struct {
		ID     int    `rdb:"id"`
		Name   string `rdb:"name"`
		Active bool   `rdb:"active"`
	}]("users")
	check(err)
	_, err = db.Table[struct {
		ID     int64  `rdb:"id"`
		Name   string `rdb:"id"`
		Active bool   `rdb:"active"`
	}]("users")
	check(err)
	_, err = db.Table[struct {
		id     int64  `rdb:"id"`
		Name   string `rdb:"name"`
		Active bool   `rdb:"active"`
	}]("users")
	check(err)
	_, err = db.Table[struct{ testUser }]("users")
	check(err)
	_, err = db.Table[struct {
		ID     int64  `rdb:""`
		Name   string `rdb:"name"`
		Active bool   `rdb:"active"`
	}]("users")
	check(err)
	users, err := db.Table[testUser]("users")
	if err != nil {
		t.Fatal(err)
	}
	_, err = users.Column[string]("id")
	check(err)
	if _, err = users.Column[int64]("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = db.Table[testUser]("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
func TestNamedScalarsAndIgnoredField(t *testing.T) {
	type ID int64
	type Name string
	type Active bool
	type model struct {
		ID      ID     `rdb:"id"`
		Name    Name   `rdb:"name"`
		Active  Active `rdb:"active"`
		Ignored []int  `rdb:"-"`
	}
	db, _ := newTestDB(t)
	users, err := db.Table[model]("users")
	if err != nil {
		t.Fatal(err)
	}
	id, err := users.Column[ID]("id")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = users.Column[int64]("id"); !errors.Is(err, ErrSchema) {
		t.Fatal(err)
	}
	if err = users.Insert(context.Background(), model{ID: 7, Name: "seven", Active: true, Ignored: []int{1}}); err != nil {
		t.Fatal(err)
	}
	got, err := users.Where(Equal(id, ID(7))).All(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "seven" || got[0].Ignored != nil {
		t.Fatalf("%v %v", got, err)
	}
}
func TestFieldOwnershipAndZeroValues(t *testing.T) {
	ctx := context.Background()
	db, _ := newTestDB(t)
	users, err := db.Table[testUser]("users")
	if err != nil {
		t.Fatal(err)
	}
	s := testSchema()
	s.Name = "others"
	if err = db.CreateTable(ctx, s); err != nil {
		t.Fatal(err)
	}
	other, err := db.Table[testUser]("others")
	if err != nil {
		t.Fatal(err)
	}
	id, err := other.Column[int64]("id")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = users.Where(Equal(id, 1)).All(ctx); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
	db2, _ := newTestDB(t)
	foreign, err := db2.Table[testUser]("users")
	if err != nil {
		t.Fatal(err)
	}
	id, err = foreign.Column[int64]("id")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = users.Where(Equal(id, 1)).All(ctx); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
	same, err := db.Table[testUser]("users")
	if err != nil {
		t.Fatal(err)
	}
	id, err = same.Column[int64]("id")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = users.Where(Equal(id, 1)).All(ctx); err != nil {
		t.Fatal(err)
	}
	var field Field[testUser, int64]
	if _, err = users.Where(Equal(field, 1)).All(ctx); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
	var table Table[testUser]
	if err = table.Insert(ctx, testUser{}); !errors.Is(err, ErrSchema) {
		t.Fatal(err)
	}
	if _, err = table.All(ctx); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
	var query Query[testUser]
	if _, err = query.All(ctx); !errors.Is(err, ErrQuery) {
		t.Fatal(err)
	}
}
func TestLegacyDataAndMigrationCompatibility(t *testing.T) {
	dir := t.TempDir()
	files, err := os.ReadDir("testdata/legacy")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join("testdata/legacy", f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, f.Name()), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	history, err := os.ReadFile(filepath.Join(dir, "migrations.json"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = db.Migrate(ctx, Migration{Version: 1, Operations: []Operation{CreateTableOp(testSchema()), CreateIndexOp("users", "active")}}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "migrations.json"))
	if err != nil || string(history) != string(after) {
		t.Fatalf("history changed: %v", err)
	}
	users, err := db.Table[testUser]("users")
	if err != nil {
		t.Fatal(err)
	}
	id, err := users.Column[int64]("id")
	if err != nil {
		t.Fatal(err)
	}
	got, err := users.Where(Equal(id, math.MaxInt64)).All(ctx)
	if err != nil || len(got) != 1 || got[0].Name != "日本語<&>" || got[0].Active {
		t.Fatalf("%v %v", got, err)
	}
	if err = users.Insert(ctx, testUser{ID: 1, Name: "new", Active: true}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	users, err = db.Table[testUser]("users")
	if err != nil {
		t.Fatal(err)
	}
	if got, err = users.All(ctx); err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}
