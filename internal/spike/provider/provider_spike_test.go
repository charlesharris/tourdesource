// Package provider is the TDS-3 spike: the Go core spawns the Ruby provider as
// a subprocess and exchanges the draft JSON protocol over stdio. Proves the
// provider seam end to end. Throwaway — real host is TDS-6, protocol is TDS-5.
package provider

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

type symbol struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// call spawns the Ruby provider one-shot: request in on stdin, response on stdout.
func call(t *testing.T, req map[string]any, out any) {
	t.Helper()
	in, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	cmd := exec.Command("ruby", "ruby/provider.rb")
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("provider run: %v\nstderr: %s", err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, stdout.String())
	}
}

// requireRubyProvider skips when ruby or prism isn't available (e.g. a CI runner
// without them), so the spike never fails the build on an unrelated host.
func requireRubyProvider(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby not installed; skipping provider spike")
	}
	if err := exec.Command("ruby", "-e", "require 'prism'").Run(); err != nil {
		t.Skip("ruby prism not available; skipping provider spike")
	}
}

func TestCapabilities(t *testing.T) {
	requireRubyProvider(t)
	var caps struct {
		Protocol  string   `json:"protocol"`
		Languages []string `json:"languages"`
		Ops       []string `json:"operations"`
	}
	call(t, map[string]any{"op": "capabilities"}, &caps)

	if caps.Protocol == "" {
		t.Error("empty protocol version")
	}
	if len(caps.Languages) == 0 || caps.Languages[0] != "ruby" {
		t.Errorf("languages = %v, want [ruby]", caps.Languages)
	}
}

func TestStructureReturnsRealSymbol(t *testing.T) {
	requireRubyProvider(t)
	var resp struct {
		Symbols []symbol `json:"symbols"`
	}
	call(t, map[string]any{
		"op":    "structure",
		"root":  "testdata",
		"files": []string{"app/models/invoice.rb"},
	}, &resp)

	if len(resp.Symbols) == 0 {
		t.Fatal("no symbols returned from a real Rails file")
	}

	// The core cares that qualified symbol paths and line ranges come back
	// correctly — including the `#` (instance) vs `.` (singleton) distinction.
	want := map[string]string{
		"Invoice":            "class",
		"Invoice#finalize":   "method",
		"Invoice#finalized?": "method",
		"Invoice.overdue":    "method",
	}
	got := map[string]string{}
	for _, s := range resp.Symbols {
		got[s.Symbol] = s.Kind
		if s.StartLine <= 0 || s.EndLine < s.StartLine {
			t.Errorf("%s: bad line range %d-%d", s.Symbol, s.StartLine, s.EndLine)
		}
	}
	for sym, kind := range want {
		if got[sym] != kind {
			t.Errorf("symbol %q: got kind %q, want %q (all: %v)", sym, got[sym], kind, got)
		}
	}
}
