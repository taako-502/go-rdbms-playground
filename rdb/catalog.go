package rdb

import (
	"cmp"
	"context"
	"slices"
)

// TableInfo is a detached snapshot of a table's schema and indexed columns.
type TableInfo struct {
	Schema  Schema   `json:"schema"`
	Indexes []string `json:"indexes"`
}

// Tables returns table definitions sorted by name. Modifying the returned
// slices does not change the database catalog.
func (db *DB) Tables(ctx context.Context) ([]TableInfo, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return nil, err
	}
	result := make([]TableInfo, 0, len(db.tables))
	for _, st := range db.tables {
		schema := st.def.Schema
		schema.Columns = slices.Clone(schema.Columns)
		result = append(result, TableInfo{Schema: schema, Indexes: slices.Clone(st.def.Indexes)})
	}
	slices.SortFunc(result, func(a, b TableInfo) int { return cmp.Compare(a.Schema.Name, b.Schema.Name) })
	return result, nil
}
