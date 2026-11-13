package rdb

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
)

var ErrMigration = errors.New("invalid or conflicting migration")

type Operation struct {
	Kind   string  `json:"kind"`
	Schema *Schema `json:"schema,omitempty"`
	Table  string  `json:"table,omitempty"`
	Column string  `json:"column,omitempty"`
}

func CreateTableOp(s Schema) Operation {
	s.Columns = append([]Column(nil), s.Columns...)
	return Operation{Kind: "create_table", Schema: &s}
}
func CreateIndexOp(table, column string) Operation {
	return Operation{Kind: "create_index", Table: table, Column: column}
}

type Migration struct {
	Version    int64       `json:"version"`
	Operations []Operation `json:"operations"`
}
type appliedMigration struct {
	Version  int64  `json:"version"`
	Checksum string `json:"checksum"`
}

func migrationHash(m Migration) string {
	// Validated migrations contain ASCII identifiers and nonempty slices, preserving
	// the original JSON representation used by persistent migration checksums.
	b, _ := json.Marshal(m, json.Deterministic(true))
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (db *DB) loadHistory() error {
	b, err := os.ReadFile(filepath.Join(db.dir, "migrations.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &db.history); err != nil {
		return fmt.Errorf("%w: migration history: %v", ErrCorrupt, err)
	}
	var previous int64
	for _, h := range db.history {
		if h.Version <= previous || len(h.Checksum) != 64 {
			return fmt.Errorf("%w: migration history", ErrCorrupt)
		}
		previous = h.Version
	}
	return nil
}

// Migrate accepts the complete migration list, orders it by version, and checks
// the checksums of previously applied versions. Never edit applied migrations.
func (db *DB) Migrate(ctx context.Context, migrations ...Migration) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return err
	}
	plan := append([]Migration(nil), migrations...)
	slices.SortFunc(plan, func(a, b Migration) int { return cmp.Compare(a.Version, b.Version) })
	hashes := map[int64]string{}
	var previous int64
	for _, m := range plan {
		if m.Version <= previous || len(m.Operations) == 0 {
			return fmt.Errorf("%w: versions must be positive and unique, operations nonempty", ErrMigration)
		}
		previous = m.Version
		for _, op := range m.Operations {
			switch op.Kind {
			case "create_table":
				if op.Schema == nil || op.Table != "" || op.Column != "" {
					return ErrMigration
				}
				if err := op.Schema.Validate(); err != nil {
					return err
				}
			case "create_index":
				if op.Schema != nil || !identifier.MatchString(op.Table) || !identifier.MatchString(op.Column) {
					return ErrMigration
				}
			default:
				return ErrMigration
			}
		}
		hashes[m.Version] = migrationHash(m)
	}
	applied := map[int64]bool{}
	var highest int64
	for _, h := range db.history {
		if hashes[h.Version] != h.Checksum {
			return fmt.Errorf("%w: applied version %d missing or changed", ErrMigration, h.Version)
		}
		applied[h.Version] = true
		highest = h.Version
	}
	for _, m := range plan {
		if !applied[m.Version] && m.Version < highest {
			return fmt.Errorf("%w: cannot insert version %d before applied versions", ErrMigration, m.Version)
		}
	}
	for _, m := range plan {
		if applied[m.Version] {
			continue
		}
		for _, op := range m.Operations {
			if err := db.check(ctx); err != nil {
				return err
			}
			switch op.Kind {
			case "create_table":
				if st := db.tables[op.Schema.Name]; st != nil {
					if !reflect.DeepEqual(st.def.Schema, *op.Schema) {
						return fmt.Errorf("%w: table %s", ErrMigration, op.Schema.Name)
					}
				} else if err := db.createTable(ctx, *op.Schema); err != nil {
					return err
				}
			case "create_index":
				st := db.tables[op.Table]
				if st == nil {
					return fmt.Errorf("%w: table %s missing", ErrMigration, op.Table)
				}
				if _, err := st.def.Schema.column(op.Column); err != nil {
					return err
				}
				if st.indexes[op.Column] == nil {
					if err := db.createIndex(ctx, op.Table, op.Column); err != nil {
						return err
					}
				}
			}
			if err := db.hit("migration_changed"); err != nil {
				return db.fail(err)
			}
		}
		next := append(append([]appliedMigration(nil), db.history...), appliedMigration{m.Version, hashes[m.Version]})
		if err := atomicJSONHook(filepath.Join(db.dir, "migrations.json"), next, func(p string) error { return db.hit("migration_history_" + p) }); err != nil {
			return db.fail(err)
		}
		db.history = next
	}
	return nil
}
