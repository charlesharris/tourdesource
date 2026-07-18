package mapper

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/anchor"
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

	// End-to-end anchor resolution (TDS-12) against the real provider output:
	// Invoice#finalize is at lines 2-4 in the fixture model.
	resolved, err := anchor.NewResolver(symbols).Resolve("app/models/invoice.rb::Invoice#finalize")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Kind != anchor.KindSymbol || resolved.StartLine != 2 {
		t.Errorf("anchor resolved to %+v, want a symbol starting at line 2", resolved)
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

// TestReportFileErrorsClamps guards the summary from being buried. A single
// unparseable file (an ERB-templated generator stub, say) can produce dozens of
// cascading parser messages, and a repo can hold many such files; mapping a real
// Rails app surfaced exactly this.
func TestReportFileErrorsClamps(t *testing.T) {
	t.Run("truncates a long message", func(t *testing.T) {
		long := strings.Repeat("unexpected constant path after `class`; ", 30)
		var got []string
		reportFileErrors(
			[]protocol.FileError{{Path: "a.rb", Message: long}},
			func(format string, a ...any) { got = append(got, fmt.Sprintf(format, a...)) },
		)
		if len(got) != 1 {
			t.Fatalf("want 1 warning, got %d: %v", len(got), got)
		}
		if len([]rune(got[0])) > maxFileErrorMessage+len("a.rb: … (truncated)") {
			t.Errorf("warning not truncated: %d runes", len([]rune(got[0])))
		}
		if !strings.Contains(got[0], "truncated") {
			t.Errorf("a cut message must say so, got %q", got[0])
		}
		if !strings.HasPrefix(got[0], "a.rb: ") {
			t.Errorf("warning must name the file, got %q", got[0])
		}
	})

	t.Run("caps the number reported and counts the rest", func(t *testing.T) {
		var errs []protocol.FileError
		for i := 0; i < maxFileErrorsReported+7; i++ {
			errs = append(errs, protocol.FileError{
				Path:    fmt.Sprintf("app/models/m%02d.rb", i),
				Message: "syntax error",
			})
		}
		var got []string
		reportFileErrors(errs, func(format string, a ...any) { got = append(got, fmt.Sprintf(format, a...)) })

		if len(got) != maxFileErrorsReported+1 {
			t.Fatalf("want %d warnings (%d files + 1 summary), got %d",
				maxFileErrorsReported+1, maxFileErrorsReported, len(got))
		}
		if last := got[len(got)-1]; !strings.Contains(last, "and 7 more") {
			t.Errorf("last warning = %q, want a count of the remaining 7", last)
		}
	})

	t.Run("reports deterministically", func(t *testing.T) {
		// Provider batches are collected from a map, so ordering is not stable
		// upstream; successive runs must still warn about the same files.
		errs := []protocol.FileError{
			{Path: "z.rb", Message: "e"}, {Path: "a.rb", Message: "e"}, {Path: "m.rb", Message: "e"},
		}
		var got []string
		reportFileErrors(errs, func(format string, a ...any) { got = append(got, fmt.Sprintf(format, a...)) })
		if !strings.HasPrefix(got[0], "a.rb") || !strings.HasPrefix(got[2], "z.rb") {
			t.Errorf("file errors must be sorted by path, got %v", got)
		}
	})

	t.Run("silent when there is nothing wrong", func(t *testing.T) {
		called := false
		reportFileErrors(nil, func(string, ...any) { called = true })
		if called {
			t.Error("no file errors must produce no warnings")
		}
	})
}
