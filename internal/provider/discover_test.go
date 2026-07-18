package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverReadsTOML(t *testing.T) {
	dir := t.TempDir()
	toml := `
[[providers]]
name = "ruby"
command = ["ruby", "provider.rb"]

[[providers]]
name = "custom"
command = ["/opt/tds/custom-provider"]
env = ["FOO=bar"]
`
	if err := os.WriteFile(filepath.Join(dir, "tds.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	specs, err := Discover(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byName := map[string]Spec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	ruby, ok := byName["ruby"]
	if !ok || len(ruby.Command) != 2 || ruby.Command[0] != "ruby" {
		t.Fatalf("ruby spec = %+v", ruby)
	}
	custom, ok := byName["custom"]
	if !ok || len(custom.Env) != 1 || custom.Env[0] != "FOO=bar" {
		t.Fatalf("custom spec = %+v", custom)
	}
}

func TestDiscoverNoConfigNoBuiltins(t *testing.T) {
	// An empty dir with no configured providers and (assuming) no tds-provider-*
	// binaries on PATH yields no specs — and no error.
	specs, err := Discover(t.TempDir())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	known := map[string]bool{}
	for _, b := range builtinProviders {
		known[b.Name] = true
	}
	for _, s := range specs {
		// If the CI/host happens to have a builtin on PATH, it must be a builtin name.
		if !known[s.Name] {
			t.Errorf("unexpected discovered spec %+v", s)
		}
	}
}

// TestTreesitterFallbackIsDiscoveredLast pins the precedence rule that makes the
// fallback a fallback: it claims ruby and javascript too, so if it were
// discovered before a native provider, Host.ForLanguage would hand it those
// files and the richer native analysis (Rails entrypoints, require edges) would
// silently never run.
func TestTreesitterFallbackIsDiscoveredLast(t *testing.T) {
	idx := -1
	for i, b := range builtinProviders {
		if b.Name == "treesitter" {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatal("the tree-sitter fallback is not registered as a builtin provider")
	}
	if idx != len(builtinProviders)-1 {
		t.Errorf("treesitter is at index %d of %d builtins; it must be last so native providers win",
			idx, len(builtinProviders))
	}
}

func TestDiscoverBadTOML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tds.toml"), []byte("[[providers]]\nname = 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(dir); err == nil {
		t.Fatal("expected an error on malformed tds.toml")
	}
}
