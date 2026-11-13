package rdb

import (
	"context"
	"fmt"
)

type Predicate struct {
	column string
	value  any
}

func Equal(column string, value any) Predicate { return Predicate{column, value} }

// Query is immutable. Invalid conditions are reported by All.
type Query struct {
	table     *Table
	predicate *Predicate
	err       error
}

func (t *Table) Where(p Predicate) Query { return Query{table: t, predicate: &p} }
func (q Query) Where(p Predicate) Query {
	q.err = fmt.Errorf("%w: Where may only be called once", ErrQuery)
	return q
}
func (t *Table) All(ctx context.Context) ([]Row, error) { return (Query{table: t}).All(ctx) }
func (q Query) All(ctx context.Context) ([]Row, error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.table == nil {
		return nil, ErrQuery
	}
	db := q.table.db
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return nil, err
	}
	st := db.tables[q.table.name]
	var value any
	if q.predicate != nil {
		c, err := st.def.Schema.column(q.predicate.column)
		if err != nil {
			return nil, err
		}
		value, err = normalize(c.Type, q.predicate.value)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrQuery, err)
		}
	}
	result := make([]Row, 0)
	if q.predicate != nil {
		if idx := st.indexes[q.predicate.column]; idx != nil {
			for _, off := range idx.search(makeKey(value)) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				row, err := st.rows.readAt(off)
				if err != nil {
					return nil, err
				}
				result = append(result, row)
			}
			return result, nil
		}
	}
	err := st.rows.scan(ctx, false, func(r record) error {
		if q.predicate == nil || r.Row[q.predicate.column] == value {
			result = append(result, r.Row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
