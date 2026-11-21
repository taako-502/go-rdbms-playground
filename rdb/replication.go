package rdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

var ErrReplicaReadOnly = errors.New("operation disabled in replication mode")

type Position struct {
	Source string
	LSN    uint64
}
type LogEntry struct {
	Reset ResetMode `json:",omitempty"`
	LSN   uint64
	Table string
	Row   json.RawMessage
}

func OpenReplica(dir, role string) (*DB, error) {
	if role != "primary" && role != "secondary" {
		return nil, errors.New("invalid replica role")
	}
	return open(dir, role)
}

// OpenExplorer restores the mode selected in Explorer for a writable node.
func OpenExplorer(dir, role string) (*DB, error) {
	if role == "secondary" {
		return OpenReplica(dir, role)
	}
	if role != "" && role != "primary" {
		return nil, errors.New("invalid replica role")
	}
	return open(dir, "explorer-"+role)
}

const modeFile = "explorer-mode.json"

func (db *DB) Role() string {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.role
}

func (db *DB) SetReplication(ctx context.Context, enabled bool) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return err
	}
	if db.role == "secondary" {
		return ErrReplicaReadOnly
	}
	if enabled == (db.role == "primary") {
		return nil
	}
	mode := "disabling"
	if enabled {
		mode = "primary"
	}
	if err := atomicJSON(filepath.Join(db.dir, modeFile), mode); err != nil {
		return db.fail(err)
	}
	if enabled {
		return db.fail(db.initReplication("primary"))
	}
	return db.fail(db.disableReplication())
}

func (db *DB) disableReplication() error {
	if err := os.RemoveAll(filepath.Join(db.dir, "replication")); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(db.dir, "replica.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db.role = ""
	db.replica = Position{}
	if err := syncDir(db.dir); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(db.dir, modeFile), "standalone")
}

func (db *DB) logPath(n uint64) string {
	return filepath.Join(db.dir, "replication", fmt.Sprintf("%020d.json", n))
}
func (db *DB) initReplication(role string) error {
	path := filepath.Join(db.dir, "replica.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if role == "" {
			return nil
		}
		if role == "secondary" {
			return errors.New("secondary requires an offline primary snapshot")
		}
		if err = os.MkdirAll(filepath.Dir(db.logPath(1)), 0700); err != nil {
			return err
		}
		var id [16]byte
		rand.Read(id[:])
		db.replica.Source = hex.EncodeToString(id[:])
		if err = atomicJSON(path, db.replica); err != nil {
			return err
		}
	} else {
		if err != nil {
			return err
		}
		if role == "" {
			return errors.New("open replicated database with an explicit role")
		}
		if err = json.Unmarshal(b, &db.replica); err != nil {
			return err
		}
		if db.replica.Source == "" {
			return ErrCorrupt
		}
	}
	db.role = role
	// A crash can leave one durable log entry beyond the checkpoint.
	b, err = os.ReadFile(db.logPath(db.replica.LSN + 1))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var entry LogEntry
	if err = json.Unmarshal(b, &entry); err != nil {
		return err
	}
	return db.replay(entry)
}
func (db *DB) logInsert(table string, row Row) error {
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	return db.writeEntry(LogEntry{Table: table, Row: data})
}

func (db *DB) writeEntry(entry LogEntry) error {
	entry.LSN = db.replica.LSN + 1
	if err := atomicJSON(db.logPath(entry.LSN), entry); err != nil {
		return db.fail(err)
	}
	if err := db.hit("log_synced"); err != nil {
		return db.fail(err)
	}
	return db.fail(db.replay(entry))
}
func (db *DB) replay(e LogEntry) error {
	if e.LSN != db.replica.LSN+1 {
		return ErrCorrupt
	}
	if e.Reset != "" {
		if err := db.applyReset(e.Reset); err != nil {
			return err
		}
	} else if err := db.replayRow(e); err != nil {
		return err
	}
	next := Position{db.replica.Source, e.LSN}
	if err := db.hit("before_checkpoint"); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(db.dir, "replica.json"), next); err != nil {
		return err
	}
	db.replica = next
	return nil
}

func (db *DB) replayRow(e LogEntry) error {
	st := db.tables[e.Table]
	if st == nil {
		return ErrCorrupt
	}
	row, err := decodeRow(e.Row, st.def.Schema)
	if err != nil {
		return err
	}
	pk := st.def.Schema.PrimaryKey
	offsets := st.indexes[pk].search(makeKey(row[pk]))
	if len(offsets) > 0 {
		old, err := st.rows.readAt(offsets[0])
		if err != nil {
			return err
		}
		if !maps.Equal(old, row) {
			return ErrCorrupt
		}
	} else if err = db.appendRow(st, row); err != nil {
		return err
	}
	return nil
}
func (db *DB) Position() Position { db.mu.Lock(); defer db.mu.Unlock(); return db.replica }

// Entries returns a bounded batch following the supplied snapshot position.
func (db *DB) Entries(ctx context.Context, p Position) ([]LogEntry, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return nil, err
	}
	if db.role != "primary" || p.Source != db.replica.Source || p.LSN > db.replica.LSN {
		return nil, errors.New("incompatible replication position")
	}
	var result []LogEntry
	for p.LSN < db.replica.LSN && len(result) < 100 {
		p.LSN++
		b, err := os.ReadFile(db.logPath(p.LSN))
		if err != nil {
			return nil, err
		}
		var entry LogEntry
		if err = json.Unmarshal(b, &entry); err != nil {
			return nil, err
		}
		if entry.LSN != p.LSN {
			return nil, ErrCorrupt
		}
		result = append(result, entry)
	}
	return result, nil
}
func (db *DB) ApplyEntries(ctx context.Context, p Position, entries []LogEntry) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.check(ctx); err != nil {
		return err
	}
	if db.role != "secondary" || p != db.replica {
		return errors.New("incompatible replication position")
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := db.replay(entry); err != nil {
			return db.fail(err)
		}
	}
	return nil
}
