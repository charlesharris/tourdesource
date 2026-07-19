// Package config reads a repository's tds.toml — the per-repo overrides for
// which providers run, which analyzers are enabled, and any opaque
// provider-interpreted settings (TDS-27).
//
// Every field is optional. A repository with no tds.toml, or a tds.toml that
// sets only some of it, behaves exactly as it did before: sensible defaults,
// no surprises. The file only ever narrows or redirects, never a prerequisite.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Filename is the per-repo config file, read from the repository root.
const Filename = "tds.toml"

// Config is a parsed tds.toml. The zero value is the valid "no config" state.
type Config struct {
	// Providers override how a language's provider is launched.
	Providers []Provider `toml:"providers"`
	// Analyze configures `tds analyze`.
	Analyze Analyze `toml:"analyze"`
}

// Provider redirects a language's provider to a specific executable — useful
// before a global install, in tests, or to pin a project's own build.
type Provider struct {
	Name    string   `toml:"name"`
	Command []string `toml:"command"`
	Env     []string `toml:"env"`
}

// Analyze is the [analyze] table.
type Analyze struct {
	// Enable is an allowlist of analyzer names. Empty means "every analyzer the
	// providers advertise"; a non-empty list restricts to exactly those.
	Enable []string `toml:"enable"`
	// Disable removes analyzers, applied after Enable. Its purpose is "run
	// everything except this one" without having to name all the others.
	Disable []string `toml:"disable"`
	// Config carries opaque, provider-interpreted settings keyed by provider
	// name (`[analyze.config.<provider>]`). tds passes each provider its own
	// block verbatim and never interprets it — a rubocop path, a brakeman
	// confidence level, whatever the provider documents.
	Config map[string]map[string]any `toml:"config"`
}

// Load reads <root>/tds.toml. A missing file is not an error — it returns the
// zero Config, which every caller treats as "use defaults".
func Load(root string) (*Config, error) {
	path := filepath.Join(root, Filename)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := c.validate(path); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate(path string) error {
	for _, p := range c.Providers {
		if p.Name == "" || len(p.Command) == 0 {
			return fmt.Errorf("%s: a [[providers]] entry needs both name and command", path)
		}
	}
	return nil
}

// ProviderConfigJSON returns a provider's opaque analyze config as JSON, keyed
// by provider name, ready to hand to the provider over the protocol. Providers
// with no config block are absent from the map rather than present-and-empty.
func (c *Config) ProviderConfigJSON() (map[string]json.RawMessage, error) {
	if len(c.Analyze.Config) == 0 {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(c.Analyze.Config))
	for name, block := range c.Analyze.Config {
		raw, err := json.Marshal(block)
		if err != nil {
			return nil, fmt.Errorf("encoding [analyze.config.%s]: %w", name, err)
		}
		out[name] = raw
	}
	return out, nil
}
