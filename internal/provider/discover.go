package provider

import (
	"os"
	"os/exec"
	"strings"

	"github.com/charlesharris/tourdesource/internal/config"
)

// builtinProviders maps a logical language name to the provider executable the
// core looks for on PATH.
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

// Discover resolves the providers for a repo rooted at root:
//   - explicit [[providers]] entries in <root>/tds.toml (take precedence), then
//   - built-in provider binaries found on PATH.
//
// It never spawns anything; Host.Open launches and handshakes the returned specs.
func Discover(root string) ([]Spec, error) {
	var specs []Spec
	seen := map[string]bool{}

	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	for _, pc := range cfg.Providers {
		specs = append(specs, Spec{Name: pc.Name, Command: pc.Command, Env: pc.Env})
		seen[pc.Name] = true
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
