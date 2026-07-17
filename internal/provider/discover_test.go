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
	for _, s := range specs {
		// If the CI/host happens to have a builtin on PATH, it must be a builtin name.
		if s.Name != "ruby" && s.Name != "js" {
			t.Errorf("unexpected discovered spec %+v", s)
		}
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
