package provider

import (
	"context"
	"io"
	"time"
)

// Host manages the set of providers available for a repo. A provider that fails
// to launch (missing binary, incompatible protocol, bad handshake) is skipped
// with a warning — never fatal.
type Host struct {
	providers []*Provider
}

// Options configure Host.Open.
type Options struct {
	Timeout time.Duration                    // per-provider handshake/request budget
	Stderr  io.Writer                        // provider stderr sink (os.Stderr if nil)
	Warnf   func(format string, args ...any) // sink for "provider unavailable" warnings
}

// Open launches every spec, isolating failures. The returned Host owns the live
// providers; call Close when done.
func Open(ctx context.Context, specs []Spec, opts Options) *Host {
	h := &Host{}
	for _, spec := range specs {
		p, err := Launch(ctx, spec, LaunchOptions{Timeout: opts.Timeout, Stderr: opts.Stderr})
		if err != nil {
			if opts.Warnf != nil {
				opts.Warnf("provider %q unavailable, skipping: %v", spec.Name, err)
			}
			continue
		}
		h.providers = append(h.providers, p)
	}
	return h
}

// Providers returns the live providers, in discovery order.
func (h *Host) Providers() []*Provider { return h.providers }

// ForLanguage returns the first live provider that handles lang, or nil. The
// caller falls back to the tree-sitter provider (or line-range anchors) on nil.
func (h *Host) ForLanguage(lang string) *Provider {
	for _, p := range h.providers {
		for _, l := range p.Caps.Languages {
			if l == lang {
				return p
			}
		}
	}
	return nil
}

// Close shuts every provider down.
func (h *Host) Close() {
	for _, p := range h.providers {
		_ = p.Close()
	}
	h.providers = nil
}
