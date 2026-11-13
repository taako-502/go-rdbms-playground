// Package rdb implements a small, single-process embedded relational database.
package rdb

import (
	"errors"
	"fmt"
	"regexp"
)

type Type string

const (
	Integer Type = "integer"
	String  Type = "string"
	Boolean Type = "boolean"
)

var (
	ErrSchema        = errors.New("invalid schema")
	ErrRow           = errors.New("invalid row")
	ErrDuplicate     = errors.New("duplicate primary key")
	ErrNotFound      = errors.New("table or column not found")
	ErrExists        = errors.New("definition already exists")
	ErrClosed        = errors.New("database closed")
	ErrNeedsRecovery = errors.New("database requires close and reopen")
	ErrCorrupt       = errors.New("corrupt database")
	ErrQuery         = errors.New("invalid query")
	ErrInUse         = errors.New("database already open in this process")
)

type Column struct {
	Name string `json:"name"`
	Type Type   `json:"type"`
}
type Schema struct {
	Name       string   `json:"name"`
	Columns    []Column `json:"columns"`
	PrimaryKey string   `json:"primary_key"`
}
type Row map[string]any

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (s Schema) Validate() error {
	if !identifier.MatchString(s.Name) || len(s.Columns) == 0 {
		return fmt.Errorf("%w: table name or columns", ErrSchema)
	}
	seen := map[string]bool{}
	for _, c := range s.Columns {
		if !identifier.MatchString(c.Name) || seen[c.Name] {
			return fmt.Errorf("%w: column %q", ErrSchema, c.Name)
		}
		if c.Type != Integer && c.Type != String && c.Type != Boolean {
			return fmt.Errorf("%w: type %q", ErrSchema, c.Type)
		}
		seen[c.Name] = true
	}
	if !seen[s.PrimaryKey] {
		return fmt.Errorf("%w: primary key must name one column", ErrSchema)
	}
	return nil
}
func (s Schema) column(name string) (Column, error) {
	for _, c := range s.Columns {
		if c.Name == name {
			return c, nil
		}
	}
	return Column{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}
func normalize(t Type, v any) (any, error) {
	switch t {
	case Integer:
		switch n := v.(type) {
		case int64:
			return n, nil
		case int:
			return int64(n), nil
		}
	case String:
		if x, ok := v.(string); ok {
			return x, nil
		}
	case Boolean:
		if x, ok := v.(bool); ok {
			return x, nil
		}
	}
	return nil, fmt.Errorf("%w: expected %s, got %T", ErrRow, t, v)
}
func (s Schema) validateRow(row Row) (Row, error) {
	if len(row) != len(s.Columns) {
		return nil, fmt.Errorf("%w: expected exactly %d columns", ErrRow, len(s.Columns))
	}
	result := make(Row, len(row))
	for _, c := range s.Columns {
		v, ok := row[c.Name]
		if !ok {
			return nil, fmt.Errorf("%w: missing %s", ErrRow, c.Name)
		}
		n, err := normalize(c.Type, v)
		if err != nil {
			return nil, fmt.Errorf("column %s: %w", c.Name, err)
		}
		result[c.Name] = n
	}
	return result, nil
}
