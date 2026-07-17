// Package store provides a SQLite-backed persistent store for the repo "map":
// the structural index and analyzer findings that `tds map` and `tds analyze`
// produce. It reuses the wire types from package protocol for the record kinds
// they already define (symbols, imports, entrypoints, findings) and adds two
// store-local kinds (files, git signals) that the protocol does not cover.
//
// The backing driver is the pure-Go modernc.org/sqlite, so the store keeps the
// core CGO-free. A whole map can be dumped to a single JSON object with
// ExportJSON for downstream tooling.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"github.com/charlesharris/tourdesource/internal/protocol"

	_ "modernc.org/sqlite"
)

// schemaVersion is the current schema revision, recorded in meta under the
// schema_version key so future migrations can detect the on-disk shape.
const schemaVersion = "1"

// File is a source file tracked in the map, with its detected language, size in
// bytes, and a computed significance score used to rank the map.
type File struct {
	Path         string
	Language     string
	Size         int64
	Significance float64
}

// GitSignal is the per-file git history summary the map uses to weight files:
// commit churn, the first and last commit touching the file, its age in days,
// and the distinct authors who changed it.
type GitSignal struct {
	Path        string
	Churn       int
	FirstCommit string
	LastCommit  string
	AgeDays     int
	Authors     []string
}

// Store is a handle to an open map database. It is safe to Close once and is
// not intended for concurrent use across goroutines.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the map database at path and runs
// migrations idempotently, so it is safe to call repeatedly on the same file.
// The caller must Close the returned Store.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// migrate creates every table if absent and records the schema version. It is
// idempotent: every statement uses IF NOT EXISTS / OR REPLACE.
func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT
);
CREATE TABLE IF NOT EXISTS files (
	path         TEXT PRIMARY KEY,
	language     TEXT,
	size         INTEGER,
	significance REAL
);
CREATE TABLE IF NOT EXISTS symbols (
	path       TEXT,
	kind       TEXT,
	name       TEXT,
	symbol     TEXT,
	start_line INTEGER,
	end_line   INTEGER,
	body_hash  TEXT
);
CREATE TABLE IF NOT EXISTS imports (
	path   TEXT,
	target TEXT,
	kind   TEXT
);
CREATE TABLE IF NOT EXISTS entrypoints (
	path TEXT,
	kind TEXT,
	name TEXT
);
CREATE TABLE IF NOT EXISTS git_signals (
	path         TEXT PRIMARY KEY,
	churn        INTEGER,
	first_commit TEXT,
	last_commit  TEXT,
	age_days     INTEGER,
	authors      TEXT
);
CREATE TABLE IF NOT EXISTS findings (
	path         TEXT,
	start_line   INTEGER,
	end_line     INTEGER,
	symbol       TEXT,
	severity     TEXT,
	rule         TEXT,
	message      TEXT,
	url          TEXT,
	tool         TEXT,
	tool_version TEXT,
	view         TEXT,
	value        REAL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	if err := s.SetMeta("schema_version", schemaVersion); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return nil
}

// SetMeta upserts a single key/value pair in the meta table.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
	}
	return nil
}

// Meta returns the value stored under key, or an error if it is absent.
func (s *Store) Meta(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", fmt.Errorf("get meta %q: %w", key, err)
	}
	return value, nil
}

// PutFiles inserts (replacing any existing row with the same path) the given
// files in a single transaction.
func (s *Store) PutFiles(files []File) error {
	return s.inTx("put files", func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT OR REPLACE INTO files (path, language, size, significance) VALUES (?, ?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, f := range files {
			if _, err := stmt.Exec(f.Path, f.Language, f.Size, f.Significance); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutSymbols inserts the given symbols in a single transaction.
func (s *Store) PutSymbols(symbols []protocol.Symbol) error {
	return s.inTx("put symbols", func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT INTO symbols (path, kind, name, symbol, start_line, end_line, body_hash) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, sym := range symbols {
			if _, err := stmt.Exec(sym.Path, sym.Kind, sym.Name, sym.Symbol, sym.StartLine, sym.EndLine, sym.BodyHash); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutImports inserts the given import edges in a single transaction.
func (s *Store) PutImports(imports []protocol.Import) error {
	return s.inTx("put imports", func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT INTO imports (path, target, kind) VALUES (?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, im := range imports {
			if _, err := stmt.Exec(im.Path, im.Target, im.Kind); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutEntrypoints inserts the given entrypoints in a single transaction.
func (s *Store) PutEntrypoints(entrypoints []protocol.Entrypoint) error {
	return s.inTx("put entrypoints", func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT INTO entrypoints (path, kind, name) VALUES (?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, e := range entrypoints {
			if _, err := stmt.Exec(e.Path, e.Kind, e.Name); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutGitSignals inserts (replacing any existing row with the same path) the
// given git signals in a single transaction. Authors are stored as a JSON array.
func (s *Store) PutGitSignals(signals []GitSignal) error {
	return s.inTx("put git signals", func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT OR REPLACE INTO git_signals (path, churn, first_commit, last_commit, age_days, authors) VALUES (?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, g := range signals {
			authors, err := json.Marshal(g.Authors)
			if err != nil {
				return fmt.Errorf("marshal authors for %q: %w", g.Path, err)
			}
			if _, err := stmt.Exec(g.Path, g.Churn, g.FirstCommit, g.LastCommit, g.AgeDays, string(authors)); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutFindings inserts the given findings in a single transaction. A nil
// Finding.Value is stored as SQL NULL.
func (s *Store) PutFindings(findings []protocol.Finding) error {
	return s.inTx("put findings", func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT INTO findings (path, start_line, end_line, symbol, severity, rule, message, url, tool, tool_version, view, value) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, f := range findings {
			var value any
			if f.Value != nil {
				value = *f.Value
			}
			if _, err := stmt.Exec(f.Path, f.StartLine, f.EndLine, f.Symbol, f.Severity, f.Rule, f.Message, f.URL, f.Tool, f.ToolVersion, f.View, value); err != nil {
				return err
			}
		}
		return nil
	})
}

// inTx runs fn inside a transaction, committing on success and rolling back on
// error. what labels the operation in wrapped errors.
func (s *Store) inTx(what string, fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("%s: begin: %w", what, err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", what, err)
	}
	return nil
}

// Files returns every tracked file.
func (s *Store) Files() ([]File, error) {
	rows, err := s.db.Query(`SELECT path, language, size, significance FROM files`)
	if err != nil {
		return nil, fmt.Errorf("query files: %w", err)
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.Language, &f.Size, &f.Significance); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Symbols returns every stored symbol.
func (s *Store) Symbols() ([]protocol.Symbol, error) {
	rows, err := s.db.Query(`SELECT path, kind, name, symbol, start_line, end_line, body_hash FROM symbols`)
	if err != nil {
		return nil, fmt.Errorf("query symbols: %w", err)
	}
	defer rows.Close()
	var out []protocol.Symbol
	for rows.Next() {
		var sym protocol.Symbol
		if err := rows.Scan(&sym.Path, &sym.Kind, &sym.Name, &sym.Symbol, &sym.StartLine, &sym.EndLine, &sym.BodyHash); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

// Imports returns every stored import edge.
func (s *Store) Imports() ([]protocol.Import, error) {
	rows, err := s.db.Query(`SELECT path, target, kind FROM imports`)
	if err != nil {
		return nil, fmt.Errorf("query imports: %w", err)
	}
	defer rows.Close()
	var out []protocol.Import
	for rows.Next() {
		var im protocol.Import
		if err := rows.Scan(&im.Path, &im.Target, &im.Kind); err != nil {
			return nil, fmt.Errorf("scan import: %w", err)
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

// Entrypoints returns every stored entrypoint.
func (s *Store) Entrypoints() ([]protocol.Entrypoint, error) {
	rows, err := s.db.Query(`SELECT path, kind, name FROM entrypoints`)
	if err != nil {
		return nil, fmt.Errorf("query entrypoints: %w", err)
	}
	defer rows.Close()
	var out []protocol.Entrypoint
	for rows.Next() {
		var e protocol.Entrypoint
		if err := rows.Scan(&e.Path, &e.Kind, &e.Name); err != nil {
			return nil, fmt.Errorf("scan entrypoint: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GitSignals returns every stored git signal, decoding the authors JSON array.
func (s *Store) GitSignals() ([]GitSignal, error) {
	rows, err := s.db.Query(`SELECT path, churn, first_commit, last_commit, age_days, authors FROM git_signals`)
	if err != nil {
		return nil, fmt.Errorf("query git signals: %w", err)
	}
	defer rows.Close()
	var out []GitSignal
	for rows.Next() {
		var (
			g       GitSignal
			authors sql.NullString
		)
		if err := rows.Scan(&g.Path, &g.Churn, &g.FirstCommit, &g.LastCommit, &g.AgeDays, &authors); err != nil {
			return nil, fmt.Errorf("scan git signal: %w", err)
		}
		if authors.Valid && authors.String != "" {
			if err := json.Unmarshal([]byte(authors.String), &g.Authors); err != nil {
				return nil, fmt.Errorf("unmarshal authors for %q: %w", g.Path, err)
			}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Findings returns every stored finding, restoring a nil Value for SQL NULL.
func (s *Store) Findings() ([]protocol.Finding, error) {
	rows, err := s.db.Query(`SELECT path, start_line, end_line, symbol, severity, rule, message, url, tool, tool_version, view, value FROM findings`)
	if err != nil {
		return nil, fmt.Errorf("query findings: %w", err)
	}
	defer rows.Close()
	var out []protocol.Finding
	for rows.Next() {
		var (
			f     protocol.Finding
			value sql.NullFloat64
		)
		if err := rows.Scan(&f.Path, &f.StartLine, &f.EndLine, &f.Symbol, &f.Severity, &f.Rule, &f.Message, &f.URL, &f.Tool, &f.ToolVersion, &f.View, &value); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		if value.Valid {
			v := value.Float64
			f.Value = &v
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// mapExport is the JSON shape ExportJSON emits: the full map as one object.
type mapExport struct {
	Meta        map[string]string     `json:"meta"`
	Files       []File                `json:"files"`
	Symbols     []protocol.Symbol     `json:"symbols"`
	Imports     []protocol.Import     `json:"imports"`
	Entrypoints []protocol.Entrypoint `json:"entrypoints"`
	GitSignals  []GitSignal           `json:"git_signals"`
	Findings    []protocol.Finding    `json:"findings"`
}

// ExportJSON writes the entire map to w as a single indented JSON object with
// meta, files, symbols, imports, entrypoints, git_signals, and findings keys.
func (s *Store) ExportJSON(w io.Writer) error {
	meta, err := s.allMeta()
	if err != nil {
		return err
	}
	files, err := s.Files()
	if err != nil {
		return err
	}
	symbols, err := s.Symbols()
	if err != nil {
		return err
	}
	imports, err := s.Imports()
	if err != nil {
		return err
	}
	entrypoints, err := s.Entrypoints()
	if err != nil {
		return err
	}
	gitSignals, err := s.GitSignals()
	if err != nil {
		return err
	}
	findings, err := s.Findings()
	if err != nil {
		return err
	}
	export := mapExport{
		Meta:        meta,
		Files:       files,
		Symbols:     symbols,
		Imports:     imports,
		Entrypoints: entrypoints,
		GitSignals:  gitSignals,
		Findings:    findings,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(export); err != nil {
		return fmt.Errorf("export json: %w", err)
	}
	return nil
}

// allMeta returns every key/value pair in the meta table.
func (s *Store) allMeta() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return nil, fmt.Errorf("query meta: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan meta: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}
