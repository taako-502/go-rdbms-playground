package rdb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type record struct {
	Offset int64
	Row    Row
}
type rowStore struct {
	file   *os.File
	schema Schema
}

func openRows(path string, s Schema) (*rowStore, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return &rowStore{f, s}, nil
}
func (s *rowStore) close() error { return s.file.Close() }
func (s *rowStore) append(row Row) (int64, error) {
	b, err := json.Marshal(row, json.Deterministic(true))
	if err != nil {
		return 0, err
	}
	b = append(b, '\n')
	off, err := s.file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	n, err := s.file.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = s.file.Sync()
	}
	return off, err
}
func decodeRow(data []byte, schema Schema) (Row, error) {
	var raw map[string]jsontext.Value
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if len(raw) != len(schema.Columns) {
		return nil, fmt.Errorf("%w: column count", ErrCorrupt)
	}
	row := Row{}
	for _, c := range schema.Columns {
		b, ok := raw[c.Name]
		if !ok || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return nil, fmt.Errorf("%w: missing or null %s", ErrCorrupt, c.Name)
		}
		var err error
		switch c.Type {
		case Integer:
			var v int64
			err = json.Unmarshal(b, &v)
			row[c.Name] = v
		case String:
			var v string
			err = json.Unmarshal(b, &v)
			row[c.Name] = v
		case Boolean:
			var v bool
			err = json.Unmarshal(b, &v)
			row[c.Name] = v
		}
		if err != nil {
			return nil, fmt.Errorf("%w: column %s: %v", ErrCorrupt, c.Name, err)
		}
	}
	return row, nil
}
func (s *rowStore) readAt(off int64) (Row, error) {
	if off < 0 {
		return nil, fmt.Errorf("%w: negative offset", ErrCorrupt)
	}
	reader := bufio.NewReader(io.NewSectionReader(s.file, off, 1<<63-1-off))
	b, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return decodeRow(b, s.schema)
}
func (s *rowStore) scan(ctx context.Context, recoverTail bool, visit func(record) error) error {
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(s.file)
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := reader.ReadBytes('\n')
		if err == io.EOF {
			if len(b) == 0 {
				return nil
			}
			if !recoverTail {
				return fmt.Errorf("%w: incomplete tail", ErrCorrupt)
			}
			if err := s.file.Truncate(offset); err != nil {
				return err
			}
			return s.file.Sync()
		}
		if err != nil {
			return err
		}
		row, err := decodeRow(b, s.schema)
		if err != nil {
			return fmt.Errorf("offset %d: %w", offset, err)
		}
		if err := visit(record{offset, row}); err != nil {
			return err
		}
		offset += int64(len(b))
	}
}

// atomicJSON syncs content before rename and the parent directory afterwards.
func atomicJSON(path string, value any) error { return atomicJSONHook(path, value, nil) }
func atomicJSONHook(path string, value any, hook func(string) error) error {
	b, err := json.Marshal(value, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if hook != nil {
		if err = hook("temp_synced"); err != nil {
			return err
		}
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	if hook != nil {
		if err = hook("renamed"); err != nil {
			return err
		}
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
