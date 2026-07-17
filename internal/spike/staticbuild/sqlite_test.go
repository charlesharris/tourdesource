// Package staticbuild is a throwaway spike (TDS-4) validating that a pure-Go
// SQLite driver and tree-sitter can both live in the tds build. See
// docs/spikes/tds-4-static-build.md for the findings.
package staticbuild

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestPureGoSQLiteRoundTrips proves modernc.org/sqlite (no CGO) can open a DB,
// create a table, and round-trip a row — the store substrate for `tds map`.
func TestPureGoSQLiteRoundTrips(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE symbols (path TEXT, name TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO symbols VALUES (?, ?)`, "app/models/invoice.rb", "Invoice"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var path, name string
	if err := db.QueryRow(`SELECT path, name FROM symbols`).Scan(&path, &name); err != nil {
		t.Fatalf("select: %v", err)
	}
	if path != "app/models/invoice.rb" || name != "Invoice" {
		t.Fatalf("round-trip mismatch: got (%q, %q)", path, name)
	}
}
