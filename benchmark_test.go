package rdb

import (
	"context"
	"fmt"
	"testing"
)

// Seed via rowStore to exclude quadratic, sync-per-insert setup from lookup
// measurements. Close/Open builds and persists indexes before timing starts.
func benchmarkDB(b *testing.B, n int, secondary bool) (*DB, *Table) {
	b.Helper()
	dir := b.TempDir()
	db, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	if err = db.CreateTable(ctx, testSchema()); err != nil {
		b.Fatal(err)
	}
	if secondary {
		if err = db.CreateIndex(ctx, "users", "name"); err != nil {
			b.Fatal(err)
		}
	}
	st := db.tables["users"]
	for i := 0; i < n; i++ {
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
	tbl, err := db.Table("users")
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
				q := tbl.Where(Equal("name", fmt.Sprintf("user-%d", n/2)))
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
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
				ctx := context.Background()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					r := testRow(int64(n + i))
					r["name"] = fmt.Sprintf("user-%d", n+i)
					if err := tbl.Insert(ctx, r); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
