package rdb

import (
	"context"
	"slices"
)

// IndexSnapshot describes an index and holds a detached copy of its B-tree.
type IndexSnapshot struct {
	Column  Column     `json:"column"`
	Degree  int        `json:"degree"`
	Primary bool       `json:"primary"`
	Root    *IndexNode `json:"root"`
}

type IndexNode struct {
	Entries  []IndexEntry `json:"entries"`
	Children []*IndexNode `json:"children,omitempty"`
}

type IndexEntry struct {
	Key     any     `json:"key"`
	Offsets []int64 `json:"offsets"`
}

// InspectIndex returns the current tree without exposing mutable database state.
func (db *DB) InspectIndex(ctx context.Context, table, column string) (IndexSnapshot, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return IndexSnapshot{}, err
	}
	st := db.tables[table]
	if st == nil {
		return IndexSnapshot{}, ErrNotFound
	}
	idx := st.indexes[column]
	if idx == nil {
		return IndexSnapshot{}, ErrNotFound
	}
	col, err := st.def.Schema.column(column)
	if err != nil {
		return IndexSnapshot{}, err
	}
	return IndexSnapshot{
		Column:  col,
		Degree:  treeDegree,
		Primary: column == st.def.Schema.PrimaryKey,
		Root:    snapshotNode(idx.Root),
	}, nil
}

func snapshotNode(n *node) *IndexNode {
	result := &IndexNode{
		Entries:  make([]IndexEntry, 0, len(n.Entries)),
		Children: make([]*IndexNode, 0, len(n.Children)),
	}
	for _, e := range n.Entries {
		var value any
		switch e.Key.Type {
		case Integer:
			value = e.Key.Int
		case String:
			value = e.Key.Text
		case Boolean:
			value = e.Key.Bool
		}
		result.Entries = append(result.Entries, IndexEntry{Key: value, Offsets: slices.Clone(e.Offsets)})
	}
	for _, child := range n.Children {
		result.Children = append(result.Children, snapshotNode(child))
	}
	return result
}
