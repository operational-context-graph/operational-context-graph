// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

// Package migrations embeds the SQL migration files for the telemetry-store schema
// and exposes a Goose Provider pre-configured for Doris / MySQL wire protocol.
package migrations

import (
	"bytes"
	"database/sql"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/mfridman/interpolate"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var embeddedFS embed.FS

// NewProvider returns a Goose provider with ${VAR:-default} substitution applied to SQL files.
// Set REPLICATION_NUM and BUCKETS in the environment before calling when non-default values are needed.
// The caller must ensure the ocg database exists before calling (goose creates its version table
// before running any migration).
func NewProvider(db *sql.DB) (*goose.Provider, error) {
	p, err := goose.NewProvider(
		goose.DialectMySQL,
		db,
		&envInterpolateFS{embeddedFS},
		goose.WithTableName("ocg.goose_db_version"),
	)
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return p, nil
}

// envInterpolateFS wraps an fs.FS and performs ${VAR:-default} substitution on SQL file content.
type envInterpolateFS struct {
	inner fs.FS
}

func (e *envInterpolateFS) Open(name string) (fs.File, error) {
	f, err := e.inner.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		return f, nil
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	env := interpolate.NewSliceEnv(os.Environ())
	expanded, err := interpolate.Interpolate(env, string(raw))
	if err != nil {
		return nil, fmt.Errorf("interpolate %s: %w", name, err)
	}
	return &memFile{
		name:    info.Name(),
		modTime: info.ModTime(),
		r:       bytes.NewReader([]byte(expanded)),
	}, nil
}

// memFile is an in-memory fs.File backed by a bytes.Reader.
type memFile struct {
	name    string
	modTime time.Time
	r       *bytes.Reader
}

func (m *memFile) Read(p []byte) (int, error)  { return m.r.Read(p) }
func (m *memFile) Close() error                { return nil }
func (m *memFile) Stat() (fs.FileInfo, error)  { return m, nil }
func (m *memFile) Name() string                { return m.name }
func (m *memFile) Size() int64                 { return int64(m.r.Len()) + int64(m.r.Size()) - int64(m.r.Len()) }
func (m *memFile) Mode() fs.FileMode           { return 0o444 }
func (m *memFile) ModTime() time.Time          { return m.modTime }
func (m *memFile) IsDir() bool                 { return false }
func (m *memFile) Sys() any                    { return nil }
