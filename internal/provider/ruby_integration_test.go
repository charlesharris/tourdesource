package provider

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/charlesharris/tourdesource/internal/protocol"
)

// TestRubyProviderIntegration drives the REAL tds-provider-ruby (TDS-10) through
// the provider host over protocol v1: launch + capabilities handshake, then a
// second structure request on the same resident process. Skips when ruby/prism
// aren't available so it never fails an unrelated host.
func TestRubyProviderIntegration(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby not installed")
	}
	if err := exec.Command("ruby", "-e", "require 'prism'").Run(); err != nil {
		t.Skip("ruby prism not available")
	}

	exe, err := filepath.Abs(filepath.Join("..", "..", "providers", "ruby", "exe", "tds-provider-ruby"))
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Abs(filepath.Join("..", "..", "providers", "ruby", "test", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}

	p, err := Launch(context.Background(),
		Spec{Name: "ruby", Command: []string{"ruby", exe}},
		LaunchOptions{Timeout: 15 * time.Second, Stderr: io.Discard})
	if err != nil {
		t.Fatalf("launch ruby provider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if langs := p.Caps.Languages; len(langs) == 0 || langs[0] != "ruby" {
		t.Fatalf("capabilities.languages = %v, want [ruby]", langs)
	}

	res, err := p.Structure(context.Background(), protocol.StructureParams{
		Root:  fixtures,
		Files: []string{"app/models/invoice.rb"},
	})
	if err != nil {
		t.Fatalf("structure: %v", err)
	}

	var finalize *protocol.Symbol
	for i := range res.Symbols {
		if res.Symbols[i].Symbol == "Invoice#finalize" {
			finalize = &res.Symbols[i]
		}
	}
	if finalize == nil {
		t.Fatalf("Invoice#finalize not found; symbols = %+v", res.Symbols)
	}
	if finalize.BodyHash == "" {
		t.Error("expected a body_hash on the resolved symbol")
	}

	var model bool
	for _, e := range res.Entrypoints {
		if e.Kind == "rails-model" {
			model = true
		}
	}
	if !model {
		t.Errorf("expected a rails-model entrypoint; got %+v", res.Entrypoints)
	}
}
