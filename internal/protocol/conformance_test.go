package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// strictUnmarshal decodes data into v, rejecting unknown fields so a fixture
// that drifts from the Go types (a typo, a renamed field) fails loudly.
func strictUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestCapabilitiesFixture(t *testing.T) {
	var req Request
	strictUnmarshal(t, readFixture(t, "capabilities.request.json"), &req)
	if req.Op != OpCapabilities {
		t.Fatalf("op = %q, want %q", req.Op, OpCapabilities)
	}
	var params CapabilitiesParams
	strictUnmarshal(t, req.Params, &params)
	if len(params.CoreProtocols) == 0 {
		t.Error("core_protocols empty")
	}

	var resp Response
	strictUnmarshal(t, readFixture(t, "capabilities.response.json"), &resp)
	if !resp.OK {
		t.Fatal("response not ok")
	}
	var caps Capabilities
	strictUnmarshal(t, resp.Result, &caps)
	if !Compatible(caps.Protocol) {
		t.Errorf("advertised protocol %q not compatible", caps.Protocol)
	}
	if len(caps.Analyzers) == 0 {
		t.Error("no analyzers advertised")
	}
	// An unavailable tool must still be describable (name + views), just not runnable.
	for _, a := range caps.Analyzers {
		if a.Name == "" || len(a.Views) == 0 {
			t.Errorf("analyzer missing name/views: %+v", a)
		}
	}
}

func TestStructureFixture(t *testing.T) {
	var req Request
	strictUnmarshal(t, readFixture(t, "structure.request.json"), &req)
	var params StructureParams
	strictUnmarshal(t, req.Params, &params)
	if len(params.Files) == 0 {
		t.Error("structure request has no files")
	}

	var resp Response
	strictUnmarshal(t, readFixture(t, "structure.response.json"), &resp)
	var result StructureResult
	strictUnmarshal(t, resp.Result, &result)

	if len(result.Symbols) == 0 {
		t.Fatal("no symbols")
	}
	for _, s := range result.Symbols {
		if s.Symbol == "" || s.Path == "" {
			t.Errorf("symbol missing path/qualified name: %+v", s)
		}
		if s.StartLine <= 0 || s.EndLine < s.StartLine {
			t.Errorf("%s: bad line range %d-%d", s.Symbol, s.StartLine, s.EndLine)
		}
	}
	// Partial-result semantics: per-file errors ride along with a successful op.
	if len(result.FileErrors) == 0 {
		t.Error("fixture should exercise file_errors")
	}
}

func TestAnalyzeFixture(t *testing.T) {
	var req Request
	strictUnmarshal(t, readFixture(t, "analyze.request.json"), &req)
	var params AnalyzeParams
	strictUnmarshal(t, req.Params, &params)
	if len(params.Config) == 0 {
		t.Error("analyze request should carry opaque config")
	}

	var resp Response
	strictUnmarshal(t, readFixture(t, "analyze.response.json"), &resp)
	var result AnalyzeResult
	strictUnmarshal(t, resp.Result, &result)

	validView := map[string]bool{ViewAnnotations: true, ViewHeatmap: true, ViewPanel: true, ViewBadge: true}
	validSeverity := map[string]bool{SeverityError: true, SeverityWarning: true, SeverityInfo: true}
	sawValue := false
	for _, f := range result.Findings {
		if !validView[f.View] {
			t.Errorf("finding has invalid view %q", f.View)
		}
		if !validSeverity[f.Severity] {
			t.Errorf("finding has invalid severity %q", f.Severity)
		}
		if f.Tool == "" || f.Rule == "" {
			t.Errorf("finding missing tool/rule: %+v", f)
		}
		if f.Value != nil {
			sawValue = true
		}
	}
	if !sawValue {
		t.Error("fixture should include a numeric finding (heatmap/badge value)")
	}
	if len(result.AnalyzerErrors) == 0 {
		t.Error("fixture should exercise analyzer_errors")
	}
}

func TestErrorFixture(t *testing.T) {
	var resp Response
	strictUnmarshal(t, readFixture(t, "error.response.json"), &resp)
	if resp.OK {
		t.Fatal("error response marked ok")
	}
	if resp.Error == nil || resp.Error.Code != CodeUnsupportedOp {
		t.Fatalf("error = %+v, want code %q", resp.Error, CodeUnsupportedOp)
	}
	// ResolveResult on a failed response returns the structured error.
	if err := resp.ResolveResult(&struct{}{}); err == nil {
		t.Error("ResolveResult on a failed response should error")
	}
}

func TestVersionNegotiation(t *testing.T) {
	cases := map[string]bool{
		"1.0.0": true,
		"1.4.2": true, // forward-compatible minor/patch
		"2.0.0": false,
		"0.9.0": false,
	}
	for v, want := range cases {
		if got := Compatible(v); got != want {
			t.Errorf("Compatible(%q) = %v, want %v", v, got, want)
		}
	}
	if MajorOf(Version) != "1" {
		t.Errorf("this build should speak major 1, got %q", Version)
	}
}
