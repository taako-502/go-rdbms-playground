package rdb

import (
	"context"
	"fmt"
	"reflect"
)

// Scalar is the set of persisted column types, including user-defined types
// with one of these underlying types. Integers are explicitly 64-bit.
type Scalar interface{ ~int64 | ~string | ~bool }

type modelField struct {
	name     string
	position int
	typ      reflect.Type
}

// Table binds a flat Go struct to an existing table. Construct it with DB.Table.
type Table[T any] struct {
	raw    *RawTable
	fields []modelField
}

// Table checks the model against the persisted schema once at binding time.
// Fields use rdb:"column" tags, default to the Go field name, or opt out with
// rdb:"-". Pointers, embedded fields, and unsupported scalar types are rejected.
func (db *DB) Table[T any](name string) (*Table[T], error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(context.Background()); err != nil {
		return nil, err
	}
	st := db.tables[name]
	if st == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	fields, err := bindModel[T](st.def.Schema)
	if err != nil {
		return nil, err
	}
	return &Table[T]{raw: &RawTable{db: db, name: name}, fields: fields}, nil
}
func scalarType(typ reflect.Type) (Type, bool) {
	switch typ.Kind() {
	case reflect.Int64:
		return Integer, true
	case reflect.String:
		return String, true
	case reflect.Bool:
		return Boolean, true
	}
	return "", false
}
func bindModel[T any](schema Schema) ([]modelField, error) {
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: model must be a non-pointer struct", ErrSchema)
	}
	fields := make([]modelField, 0, typ.NumField())
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, tagged := f.Tag.Lookup("rdb")
		if tagged && name == "-" {
			continue
		}
		if !tagged {
			name = f.Name
		}
		if !f.IsExported() || f.Anonymous || !identifier.MatchString(name) || seen[name] {
			return nil, fmt.Errorf("%w: invalid or duplicate model field %s", ErrSchema, f.Name)
		}
		column, err := schema.column(name)
		if err != nil {
			return nil, fmt.Errorf("%w: model field %s: %v", ErrSchema, f.Name, err)
		}
		kind, ok := scalarType(f.Type)
		if !ok || kind != column.Type {
			return nil, fmt.Errorf("%w: model field %s does not match %s", ErrSchema, f.Name, column.Type)
		}
		seen[name] = true
		fields = append(fields, modelField{name, i, f.Type})
	}
	if len(fields) != len(schema.Columns) {
		return nil, fmt.Errorf("%w: model must map every column exactly once", ErrSchema)
	}
	return fields, nil
}
func scalarValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Int64:
		return v.Int()
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return v.Bool()
	}
	panic("unvalidated model field")
}
func (t *Table[T]) valid() bool { return t != nil && t.raw != nil }

// Insert accepts only the model type associated with this table.
func (t *Table[T]) Insert(ctx context.Context, value T) error {
	if !t.valid() {
		return fmt.Errorf("%w: unbound table", ErrSchema)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	v := reflect.ValueOf(value)
	row := make(Row, len(t.fields))
	for _, f := range t.fields {
		row[f.name] = scalarValue(v.Field(f.position))
	}
	return t.raw.Insert(ctx, row)
}

// Field carries both the table's model type and the exact Go column type.
// Its zero value is invalid; obtain fields through Table.Column.
type Field[T any, V Scalar] struct {
	table *Table[T]
	name  string
}

// Column validates the name and exact Go type, including named scalar types.
func (t *Table[T]) Column[V Scalar](name string) (Field[T, V], error) {
	if !t.valid() {
		return Field[T, V]{}, fmt.Errorf("%w: unbound table", ErrSchema)
	}
	for _, f := range t.fields {
		if f.name == name {
			if f.typ != reflect.TypeFor[V]() {
				return Field[T, V]{}, fmt.Errorf("%w: column %s requires %s", ErrSchema, name, f.typ)
			}
			return Field[T, V]{table: t, name: name}, nil
		}
	}
	return Field[T, V]{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Predicate is tied to the model type, so predicates for another model cannot
// be passed to Where. The actual DB/table identity is also checked at runtime.
type Predicate[T any] struct {
	table *Table[T]
	raw   RawPredicate
}

func Equal[T any, V Scalar](field Field[T, V], value V) Predicate[T] {
	return Predicate[T]{table: field.table, raw: RawEqual(field.name, scalarValue(reflect.ValueOf(value)))}
}

type Query[T any] struct {
	table *Table[T]
	raw   RawQuery
	err   error
}

func (t *Table[T]) Where(p Predicate[T]) Query[T] {
	q := Query[T]{table: t}
	if !t.valid() || !p.table.valid() {
		q.err = fmt.Errorf("%w: unbound table or field", ErrQuery)
		return q
	}
	if t.raw.db != p.table.raw.db || t.raw.name != p.table.raw.name {
		q.err = fmt.Errorf("%w: field belongs to another table", ErrQuery)
		return q
	}
	q.raw = t.raw.Where(p.raw)
	return q
}
func (q Query[T]) Where(_ Predicate[T]) Query[T] {
	q.err = fmt.Errorf("%w: Where may only be called once", ErrQuery)
	return q
}
func (t *Table[T]) All(ctx context.Context) ([]T, error) {
	if !t.valid() {
		return nil, fmt.Errorf("%w: unbound table", ErrQuery)
	}
	return (Query[T]{table: t, raw: RawQuery{table: t.raw}}).All(ctx)
}
func (q Query[T]) All(ctx context.Context) ([]T, error) {
	if q.err != nil {
		return nil, q.err
	}
	if !q.table.valid() {
		return nil, fmt.Errorf("%w: unbound query", ErrQuery)
	}
	rows, err := q.raw.All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]T, 0, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var value T
		v := reflect.ValueOf(&value).Elem()
		for _, f := range q.table.fields {
			field := v.Field(f.position)
			switch field.Kind() {
			case reflect.Int64:
				field.SetInt(row[f.name].(int64))
			case reflect.String:
				field.SetString(row[f.name].(string))
			case reflect.Bool:
				field.SetBool(row[f.name].(bool))
			}
		}
		result = append(result, value)
	}
	return result, nil
}
