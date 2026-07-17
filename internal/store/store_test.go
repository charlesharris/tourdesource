package store

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/protocol"
)

func ptr(f float64) *float64 { return &f }

func sampleFiles() []File {
	return []File{
		{Path: "app/models/invoice.rb", Language: "ruby", Size: 1024, Significance: 0.9},
		{Path: "app/models/user.rb", Language: "ruby", Size: 512, Significance: 0.4},
	}
}

func sampleSymbols() []protocol.Symbol {
	return []protocol.Symbol{
		{Path: "app/models/invoice.rb", Kind: "class", Name: "Invoice", Symbol: "Invoice", StartLine: 1, EndLine: 40, BodyHash: "abc123"},
		{Path: "app/models/invoice.rb", Kind: "method", Name: "finalize", Symbol: "Invoice#finalize", StartLine: 10, EndLine: 20},
	}
}

func sampleImports() []protocol.Import {
	return []protocol.Import{
		{Path: "app/models/invoice.rb", Target: "app/models/user.rb", Kind: "require"},
	}
}

func sampleEntrypoints() []protocol.Entrypoint {
	return []protocol.Entrypoint{
		{Path: "config/routes.rb", Kind: "route", Name: "invoices#index"},
	}
}

func sampleGitSignals() []GitSignal {
	return []GitSignal{
		{Path: "app/models/invoice.rb", Churn: 12, FirstCommit: "aaa", LastCommit: "zzz", AgeDays: 300, Authors: []string{"alice", "bob"}},
	}
}

func sampleFindings() []protocol.Finding {
	return []protocol.Finding{
		{Path: "app/models/invoice.rb", StartLine: 10, EndLine: 20, Symbol: "Invoice#finalize", Severity: "warning", Rule: "complexity", Message: "too complex", Tool: "rubocop", ToolVersion: "1.0", View: "heatmap", Value: ptr(17.5)},
		{Path: "app/models/user.rb", StartLine: 1, EndLine: 1, Severity: "info", Rule: "style", Message: "annotation", Tool: "rubocop", View: "annotation"},
	}
}

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "map.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestRoundTrip(t *testing.T) {
	s, _ := newStore(t)

	if err := s.PutFiles(sampleFiles()); err != nil {
		t.Fatalf("PutFiles: %v", err)
	}
	if err := s.PutSymbols(sampleSymbols()); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}
	if err := s.PutImports(sampleImports()); err != nil {
		t.Fatalf("PutImports: %v", err)
	}
	if err := s.PutEntrypoints(sampleEntrypoints()); err != nil {
		t.Fatalf("PutEntrypoints: %v", err)
	}
	if err := s.PutGitSignals(sampleGitSignals()); err != nil {
		t.Fatalf("PutGitSignals: %v", err)
	}
	if err := s.PutFindings(sampleFindings()); err != nil {
		t.Fatalf("PutFindings: %v", err)
	}

	files, err := s.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("Files: got %d, want 2", len(files))
	}

	symbols, err := s.Symbols()
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(symbols) != 2 {
		t.Fatalf("Symbols: got %d, want 2", len(symbols))
	}
	if symbols[0].Symbol != "Invoice" || symbols[0].BodyHash != "abc123" {
		t.Errorf("Symbols[0] = %+v, unexpected", symbols[0])
	}

	imports, err := s.Imports()
	if err != nil {
		t.Fatalf("Imports: %v", err)
	}
	if len(imports) != 1 || imports[0].Target != "app/models/user.rb" {
		t.Errorf("Imports = %+v, unexpected", imports)
	}

	entrypoints, err := s.Entrypoints()
	if err != nil {
		t.Fatalf("Entrypoints: %v", err)
	}
	if len(entrypoints) != 1 || entrypoints[0].Name != "invoices#index" {
		t.Errorf("Entrypoints = %+v, unexpected", entrypoints)
	}

	signals, err := s.GitSignals()
	if err != nil {
		t.Fatalf("GitSignals: %v", err)
	}
	if len(signals) != 1 {
		t.Fatalf("GitSignals: got %d, want 1", len(signals))
	}
	if got := signals[0].Authors; len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("GitSignals authors = %v, want [alice bob]", got)
	}

	findings, err := s.Findings()
	if err != nil {
		t.Fatalf("Findings: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("Findings: got %d, want 2", len(findings))
	}
	// Assert the nullable Value round-trips: one non-nil, one nil.
	var withValue, withoutValue *protocol.Finding
	for i := range findings {
		f := &findings[i]
		if f.Rule == "complexity" {
			withValue = f
		}
		if f.Rule == "style" {
			withoutValue = f
		}
	}
	if withValue == nil || withValue.Value == nil {
		t.Fatalf("complexity finding lost its Value: %+v", withValue)
	}
	if *withValue.Value != 17.5 {
		t.Errorf("complexity Value = %v, want 17.5", *withValue.Value)
	}
	if withoutValue == nil || withoutValue.Value != nil {
		t.Errorf("style finding should have nil Value, got %+v", withoutValue)
	}
}

func TestOpenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "map.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.PutFiles(sampleFiles()); err != nil {
		t.Fatalf("PutFiles: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-open the same path: migrations must not fail and data must survive.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()

	files, err := s2.Files()
	if err != nil {
		t.Fatalf("Files after reopen: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("Files after reopen: got %d, want 2", len(files))
	}

	version, err := s2.Meta("schema_version")
	if err != nil {
		t.Fatalf("Meta schema_version: %v", err)
	}
	if version != schemaVersion {
		t.Errorf("schema_version = %q, want %q", version, schemaVersion)
	}
}

func TestMeta(t *testing.T) {
	s, _ := newStore(t)
	if err := s.SetMeta("root", "/repo"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	// Overwrite the same key.
	if err := s.SetMeta("root", "/other"); err != nil {
		t.Fatalf("SetMeta overwrite: %v", err)
	}
	got, err := s.Meta("root")
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if got != "/other" {
		t.Errorf("Meta root = %q, want %q", got, "/other")
	}
}

func TestExportJSON(t *testing.T) {
	s, _ := newStore(t)
	if err := s.PutSymbols(sampleSymbols()); err != nil {
		t.Fatalf("PutSymbols: %v", err)
	}
	if err := s.PutFindings(sampleFindings()); err != nil {
		t.Fatalf("PutFindings: %v", err)
	}

	var buf bytes.Buffer
	if err := s.ExportJSON(&buf); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	// Valid JSON with all the top-level keys.
	var export map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &export); err != nil {
		t.Fatalf("ExportJSON produced invalid JSON: %v", err)
	}
	for _, key := range []string{"meta", "files", "symbols", "imports", "entrypoints", "git_signals", "findings"} {
		if _, ok := export[key]; !ok {
			t.Errorf("ExportJSON missing key %q", key)
		}
	}
	// Contains an inserted symbol.
	if !strings.Contains(buf.String(), "Invoice#finalize") {
		t.Errorf("ExportJSON does not contain inserted symbol; got:\n%s", buf.String())
	}
}
