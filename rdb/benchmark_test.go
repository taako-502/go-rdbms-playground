package rdb

import (
	"fmt"
	"testing"
)

// Seed via rowStore to exclude quadratic, sync-per-insert setup from lookup
// measurements. Close/Open builds and persists indexes before timing starts.
func benchmarkDB(b *testing.B, n int, secondary bool) (*DB, *Table[testUser]) {
	b.Helper()
	dir := b.TempDir()
	db, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	ctx := b.Context()
	if err = db.CreateTable(ctx, testSchema()); err != nil {
		b.Fatal(err)
	}
	if secondary {
		if err = db.CreateIndex(ctx, "users", "name"); err != nil {
			b.Fatal(err)
		}
	}
	st := db.tables["users"]
	for i := range n {
		row := testRow(int64(i))
		row["name"] = fmt.Sprintf("user-%d", i)
		if _, err = st.rows.append(row); err != nil {
			b.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		b.Fatal(err)
	}
	db, err = Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	tbl, err := db.Table[testUser]("users")
	if err != nil {
		b.Fatal(err)
	}
	return db, tbl
}
func BenchmarkLookup(b *testing.B) {
	for _, n := range []int{100, 1000} {
		for _, indexed := range []bool{false, true} {
			b.Run(fmt.Sprintf("rows=%d/index=%t", n, indexed), func(b *testing.B) {
				_, tbl := benchmarkDB(b, n, indexed)
				name, err := tbl.Column[string]("name")
				if err != nil {
					b.Fatal(err)
				}
				q := tbl.Where(Equal(name, fmt.Sprintf("user-%d", n/2)))
				ctx := b.Context()
				b.ReportAllocs()
				for b.Loop() {
					rows, err := q.All(ctx)
					if err != nil || len(rows) != 1 {
						b.Fatalf("%v %v", rows, err)
					}
				}
			})
		}
	}
}
func BenchmarkInsert(b *testing.B) {
	for _, n := range []int{100, 1000} {
		for _, secondary := range []bool{false, true} {
			b.Run(fmt.Sprintf("initial_rows=%d/secondary=%t", n, secondary), func(b *testing.B) {
				_, tbl := benchmarkDB(b, n, secondary)
				ctx := b.Context()
				b.ReportAllocs()
				i := n
				for b.Loop() {
					r := testUser{ID: int64(i), Name: fmt.Sprintf("user-%d", i), Active: true}
					i++
					if err := tbl.Insert(ctx, r); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
