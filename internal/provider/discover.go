package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// builtinProviders maps a logical language name to the provider executable the
// core looks for on PATH. Extended by TDS-27's full config.
//
// Order is significant: Host.ForLanguage takes the first provider claiming a
// language, and the tree-sitter fallback claims many — including ruby and
// javascript. Keeping it last means a native provider always wins and the
// fallback only covers what nothing else does.
var builtinProviders = []struct {
	Name string
	Bin  string
}{
	{"ruby", "tds-provider-ruby"},
	{"js", "tds-provider-js"},
	{"treesitter", "tds-provider-treesitter"},
}

// fileConfig is the subset of tds.toml this task reads. Full config is TDS-27.
type fileConfig struct {
	Providers []providerConfig `toml:"providers"`
}

type providerConfig struct {
	Name    string   `toml:"name"`
	Command []string `toml:"command"`
	Env     []string `toml:"env"`
}

// Discover resolves the providers for a repo rooted at root:
//   - explicit [[providers]] entries in <root>/tds.toml (take precedence), then
//   - built-in provider binaries found on PATH.
//
// It never spawns anything; Host.Open launches and handshakes the returned specs.
func Discover(root string) ([]Spec, error) {
	var specs []Spec
	seen := map[string]bool{}

	cfgPath := filepath.Join(root, "tds.toml")
	if b, err := os.ReadFile(cfgPath); err == nil {
		var cfg fileConfig
		if err := toml.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", cfgPath, err)
		}
		for _, pc := range cfg.Providers {
			if pc.Name == "" || len(pc.Command) == 0 {
				return nil, fmt.Errorf("%s: provider entry needs name and command", cfgPath)
			}
			specs = append(specs, Spec{Name: pc.Name, Command: pc.Command, Env: pc.Env})
			seen[pc.Name] = true
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", cfgPath, err)
	}

	for _, b := range builtinProviders {
		if seen[b.Name] {
			continue
		}
		// Override hook: TDS_PROVIDER_RUBY / TDS_PROVIDER_JS point at a provider
		// executable directly (useful before a global install, and in tests).
		if p := os.Getenv("TDS_PROVIDER_" + strings.ToUpper(b.Name)); p != "" {
			specs = append(specs, Spec{Name: b.Name, Command: []string{p}})
			seen[b.Name] = true
			continue
		}
		if path, err := exec.LookPath(b.Bin); err == nil {
			specs = append(specs, Spec{Name: b.Name, Command: []string{path}})
			seen[b.Name] = true
		}
	}

	return specs, nil
}
