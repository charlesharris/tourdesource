package mapper

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

// TestBuildOnMiniRailsRepo drives the whole pipeline on a real temporary git
// repo containing a tiny Rails app, with the Ruby provider wired in via the
// TDS_PROVIDER_RUBY override. It asserts the map is populated end to end:
// files, git signals, symbols, and Rails entrypoints, plus the JSON export.
func TestBuildOnMiniRailsRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby not installed")
	}
	if err := exec.Command("ruby", "-e", "require 'prism'").Run(); err != nil {
		t.Skip("ruby prism not available")
	}

	root := t.TempDir()
	write(t, root, "app/models/invoice.rb", "class Invoice < ApplicationRecord\n  def finalize\n    save!\n  end\nend\n")
	write(t, root, "app/controllers/webhooks_controller.rb", "class WebhooksController < ApplicationController\n  def create\n    head :ok\n  end\nend\n")
	write(t, root, "README.md", "# Mini app\n")
	initRepo(t, root)

	rubyExe, err := filepath.Abs(filepath.Join("..", "..", "providers", "ruby", "exe", "tds-provider-ruby"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TDS_PROVIDER_RUBY", rubyExe)

	res, err := Build(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if res.Files < 3 {
		t.Errorf("files = %d, want >= 3", res.Files)
	}
	if len(res.Providers) == 0 || res.Providers[0] != "ruby" {
		t.Errorf("providers = %v, want [ruby]", res.Providers)
	}
	if res.Symbols == 0 {
		t.Fatal("no symbols extracted")
	}
	if res.Commit == "" {
		t.Error("commit not resolved for a git repo")
	}

	// Inspect the actual store, not just the counts.
	st, err := store.Open(res.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	symbols, err := st.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	if !hasSymbol(symbols, "Invoice#finalize") {
		t.Errorf("Invoice#finalize not in store; got %d symbols", len(symbols))
	}

	entrypoints, err := st.Entrypoints()
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntrypointKind(entrypoints, "rails-model") || !hasEntrypointKind(entrypoints, "rails-controller") {
		t.Errorf("missing Rails entrypoints; got %+v", entrypoints)
	}

	signals, err := st.GitSignals()
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) == 0 {
		t.Error("no git signals stored for a committed repo")
	}

	// map.json exists and is valid JSON with the expected top-level keys.
	data, err := os.ReadFile(res.JSONPath)
	if err != nil {
		t.Fatalf("read map.json: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("map.json invalid: %v", err)
	}
	for _, key := range []string{"meta", "files", "symbols", "entrypoints", "git_signals"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("map.json missing key %q", key)
		}
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initRepo(t *testing.T, root string) {
	t.Helper()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=2024-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-01T00:00:00Z",
	)
	for _, args := range [][]string{
		{"-c", "init.defaultBranch=main", "init"},
		{"add", "-A"},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func hasSymbol(symbols []protocol.Symbol, qualified string) bool {
	for _, s := range symbols {
		if s.Symbol == qualified {
			return true
		}
	}
	return false
}

func hasEntrypointKind(entrypoints []protocol.Entrypoint, kind string) bool {
	for _, e := range entrypoints {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
