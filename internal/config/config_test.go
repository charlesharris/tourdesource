package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestMissingFileIsZeroConfig — a repo without tds.toml must behave exactly as
// before: defaults, no error.
func TestMissingFileIsZeroConfig(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("missing tds.toml should not error: %v", err)
	}
	if len(c.Providers) != 0 || len(c.Analyze.Enable) != 0 || len(c.Analyze.Disable) != 0 {
		t.Errorf("missing file should yield the zero config, got %+v", c)
	}
}

func TestReadsAnalyzeSelection(t *testing.T) {
	c, err := Load(write(t, `
[analyze]
enable = ["rubocop", "brakeman"]
disable = ["flog"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Analyze.Enable; len(got) != 2 || got[0] != "rubocop" {
		t.Errorf("enable = %v", got)
	}
	if got := c.Analyze.Disable; len(got) != 1 || got[0] != "flog" {
		t.Errorf("disable = %v", got)
	}
}

// TestProviderConfigMarshalsToJSON — a per-provider block is handed to the
// provider verbatim as JSON; tds never interprets it.
func TestProviderConfigMarshalsToJSON(t *testing.T) {
	c, err := Load(write(t, `
[analyze.config.ruby]
rubocop_config = ".rubocop.yml"
min_confidence = 2
`))
	if err != nil {
		t.Fatal(err)
	}
	byProvider, err := c.ProviderConfigJSON()
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := byProvider["ruby"]
	if !ok {
		t.Fatalf("want a ruby config block, got %v", byProvider)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("provider config must be valid JSON: %v", err)
	}
	if decoded["rubocop_config"] != ".rubocop.yml" {
		t.Errorf("config not carried through: %v", decoded)
	}
}

// TestNoProviderConfigIsNil — a provider with no block is absent, not
// present-and-empty, so the provider is not handed an empty object.
func TestNoProviderConfigIsNil(t *testing.T) {
	c, err := Load(write(t, "[analyze]\nenable = [\"rubocop\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c.ProviderConfigJSON(); got != nil {
		t.Errorf("no [analyze.config] should be nil, got %v", got)
	}
}

// TestProviderEntryValidation — a [[providers]] override needs both name and
// command, or it is a silent misconfiguration.
func TestProviderEntryValidation(t *testing.T) {
	_, err := Load(write(t, "[[providers]]\nname = \"ruby\"\n"))
	if err == nil {
		t.Error("a provider entry without a command should be rejected")
	}
}

func TestMalformedTOMLIsReported(t *testing.T) {
	if _, err := Load(write(t, "[analyze\nenable = ")); err == nil {
		t.Error("malformed tds.toml should error, not parse to zero")
	}
}
