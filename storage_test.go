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

func TestRowsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	s, err := openRows(path, testSchema())
	if err != nil {
		t.Fatal(err)
	}
	rows := []Row{testRow(math.MinInt64), testRow(math.MaxInt64)}
	var offsets []int64
	for _, r := range rows {
		off, err := s.append(r)
		if err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, off)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = openRows(path, testSchema())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for i, off := range offsets {
		got, err := s.readAt(off)
		if err != nil || !reflect.DeepEqual(got, rows[i]) {
			t.Fatalf("%v %v", got, err)
		}
	}
	var got []Row
	if err := s.scan(context.Background(), false, func(r record) error { got = append(got, r.Row); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, got) {
		t.Fatal(got)
	}
}
func TestTailRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	s, err := openRows(path, testSchema())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if _, err = s.append(testRow(1)); err != nil {
		t.Fatal(err)
	}
	info, _ := s.file.Stat()
	if _, err = s.file.WriteString(`{"id":2`); err != nil {
		t.Fatal(err)
	}
	if err = s.scan(context.Background(), true, func(record) error { return nil }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if info.Size() != after.Size() {
		t.Fatal("tail not truncated")
	}
	if _, err = s.file.Seek(0, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = s.file.WriteString("broken\n"); err != nil {
		t.Fatal(err)
	}
	if err = s.scan(context.Background(), true, func(record) error { return nil }); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}
