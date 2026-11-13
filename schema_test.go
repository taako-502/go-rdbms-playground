package rdb

import (
	"errors"
	"testing"
)

func testSchema() Schema {
	return Schema{"users", []Column{{"id", Integer}, {"name", String}, {"active", Boolean}}, "id"}
}
func testRow(id int64) Row { return Row{"id": id, "name": "日本語", "active": true} }
func TestSchema(t *testing.T) {
	s := testSchema()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Schema{{"../escape", s.Columns, "id"}, {"users", s.Columns, "missing"}, {"users", []Column{{"id", Integer}, {"id", String}}, "id"}, {"users", []Column{{"id", "float"}}, "id"}} {
		if !errors.Is(bad.Validate(), ErrSchema) {
			t.Fatalf("accepted %#v", bad)
		}
	}
	for _, r := range []Row{{"id": 1}, {"id": nil, "name": "n", "active": true}, {"id": 1.5, "name": "n", "active": true}, {"id": 1, "name": "n", "other": true}} {
		if _, err := s.validateRow(r); !errors.Is(err, ErrRow) {
			t.Fatalf("accepted %#v", r)
		}
	}
	if _, err := s.validateRow(testRow(1)); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidUTF8Rejected(t *testing.T) {
	r := testRow(1)
	r["name"] = string([]byte{0xff})
	if _, err := testSchema().validateRow(r); !errors.Is(err, ErrRow) {
		t.Fatal(err)
	}
}
