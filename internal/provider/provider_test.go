package provider

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charlesharris/tourdesource/internal/protocol"
)

// TestMain doubles as a helper-process host: when TDS_TEST_PROVIDER is set, this
// binary acts as a protocol-speaking provider instead of running the tests. The
// mode selects its behavior. This exercises the real subprocess + pipe path.
func TestMain(m *testing.M) {
	if mode := os.Getenv("TDS_TEST_PROVIDER"); mode != "" {
		runHelperProvider(mode)
		return
	}
	os.Exit(m.Run())
}

func runHelperProvider(mode string) {
	if mode == "crash" {
		os.Exit(1) // die before the handshake
	}
	dec := protocol.NewDecoder(os.Stdin)
	enc := protocol.NewEncoder(os.Stdout)
	for {
		req, err := dec.DecodeRequest()
		if err != nil {
			return // stdin closed
		}
		switch req.Op {
		case protocol.OpCapabilities:
			ver := "1.0.0"
			if mode == "badversion" {
				ver = "2.0.0"
			}
			resp, _ := protocol.NewResponse(req.ID, protocol.Capabilities{
				Protocol: ver, Provider: "tds-test", ProviderVersion: "0.0.1",
				Languages: []string{"ruby"}, Operations: []string{"capabilities", "structure"},
			})
			_ = enc.Encode(resp)
		case protocol.OpStructure:
			if mode == "slow" {
				time.Sleep(5 * time.Second)
			}
			resp, _ := protocol.NewResponse(req.ID, protocol.StructureResult{
				Symbols: []protocol.Symbol{{Path: "a.rb", Kind: "class", Name: "A", Symbol: "A", StartLine: 1, EndLine: 2}},
			})
			_ = enc.Encode(resp)
		default:
			_ = enc.Encode(protocol.NewErrorResponse(req.ID, protocol.CodeUnsupportedOp, "unsupported"))
		}
	}
}

func helperSpec(name, mode string) Spec {
	return Spec{Name: name, Command: []string{os.Args[0]}, Env: []string{"TDS_TEST_PROVIDER=" + mode}}
}

func openHost(t *testing.T, specs []Spec, timeout time.Duration) (*Host, *strings.Builder) {
	t.Helper()
	warnings := &strings.Builder{}
	h := Open(context.Background(), specs, Options{
		Timeout: timeout,
		Stderr:  io.Discard,
		Warnf:   func(f string, a ...any) { fmt.Fprintf(warnings, f, a...) },
	})
	t.Cleanup(h.Close)
	return h, warnings
}

func TestHostLaunchesAndNegotiates(t *testing.T) {
	h, _ := openHost(t, []Spec{helperSpec("ruby", "good")}, 5*time.Second)

	if got := len(h.Providers()); got != 1 {
		t.Fatalf("providers = %d, want 1", got)
	}
	p := h.ForLanguage("ruby")
	if p == nil {
		t.Fatal("no provider for ruby")
	}
	if !protocol.Compatible(p.Caps.Protocol) {
		t.Errorf("negotiated protocol %q not compatible", p.Caps.Protocol)
	}

	res, err := p.Structure(context.Background(), protocol.StructureParams{Root: ".", Files: []string{"a.rb"}})
	if err != nil {
		t.Fatalf("structure: %v", err)
	}
	if len(res.Symbols) != 1 || res.Symbols[0].Symbol != "A" {
		t.Fatalf("structure result = %+v", res)
	}
}

func TestIncompatibleProviderRejected(t *testing.T) {
	h, warnings := openHost(t, []Spec{helperSpec("badv", "badversion")}, 5*time.Second)
	if got := len(h.Providers()); got != 0 {
		t.Fatalf("providers = %d, want 0 (incompatible)", got)
	}
	if !strings.Contains(warnings.String(), "unavailable") {
		t.Errorf("expected an 'unavailable' warning, got %q", warnings.String())
	}
}

func TestMissingProviderIsolated(t *testing.T) {
	// A missing binary must be skipped, and healthy providers still load.
	specs := []Spec{
		{Name: "gone", Command: []string{"/no/such/tds-provider-zzz"}},
		helperSpec("ruby", "good"),
	}
	h, warnings := openHost(t, specs, 5*time.Second)
	if got := len(h.Providers()); got != 1 {
		t.Fatalf("providers = %d, want 1 (missing one isolated)", got)
	}
	if h.ForLanguage("ruby") == nil {
		t.Error("healthy provider should still be available")
	}
	if !strings.Contains(warnings.String(), "gone") {
		t.Errorf("expected a warning naming the missing provider, got %q", warnings.String())
	}
}

func TestCrashingProviderIsolated(t *testing.T) {
	h, _ := openHost(t, []Spec{helperSpec("boom", "crash")}, 5*time.Second)
	if got := len(h.Providers()); got != 0 {
		t.Fatalf("providers = %d, want 0 (crashed at handshake)", got)
	}
}

func TestRequestTimeout(t *testing.T) {
	// Handshake is fast; only structure is slow, so the provider launches but the
	// structure call trips the per-request timeout and marks the provider dead.
	h, _ := openHost(t, []Spec{helperSpec("slow", "slow")}, 500*time.Millisecond)
	p := h.ForLanguage("ruby")
	if p == nil {
		t.Fatal("slow provider should still complete the handshake")
	}
	_, err := p.Structure(context.Background(), protocol.StructureParams{Root: ".", Files: []string{"a.rb"}})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want a timeout", err)
	}
	// Subsequent calls fail fast against the dead provider.
	if _, err := p.Structure(context.Background(), protocol.StructureParams{Root: "."}); err == nil {
		t.Error("expected calls after timeout to fail (provider dead)")
	}
}
