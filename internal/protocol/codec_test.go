package protocol

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodecRoundTrip encodes several messages as JSONL and decodes them back,
// confirming one-message-per-line framing and id/field preservation.
func TestCodecRoundTrip(t *testing.T) {
	req, err := NewRequest(7, OpStructure, StructureParams{Root: "/repo", Files: []string{"a.rb"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := NewResponse(7, StructureResult{Symbols: []Symbol{{
		Path: "a.rb", Kind: "class", Name: "A", Symbol: "A", StartLine: 1, EndLine: 3,
	}}})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(req); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(resp); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(buf.String(), "\n"); lines != 2 {
		t.Fatalf("expected 2 framed lines, got %d", lines)
	}

	dec := NewDecoder(&buf)
	gotReq, err := dec.DecodeRequest()
	if err != nil {
		t.Fatal(err)
	}
	if gotReq.ID != 7 || gotReq.Op != OpStructure {
		t.Fatalf("request round-trip mismatch: %+v", gotReq)
	}
	var params StructureParams
	if err := gotReq.Bind(&params); err != nil {
		t.Fatal(err)
	}
	if len(params.Files) != 1 || params.Files[0] != "a.rb" {
		t.Fatalf("params round-trip mismatch: %+v", params)
	}

	gotResp, err := dec.DecodeResponse()
	if err != nil {
		t.Fatal(err)
	}
	var result StructureResult
	if err := gotResp.ResolveResult(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Symbols) != 1 || result.Symbols[0].Symbol != "A" {
		t.Fatalf("result round-trip mismatch: %+v", result)
	}
}

// TestDecoderSkipsBlankLinesAndEOF ensures blank lines are ignored and EOF is
// surfaced cleanly.
func TestDecoderSkipsBlankLinesAndEOF(t *testing.T) {
	in := "\n\n" + `{"id":1,"op":"capabilities"}` + "\n\n"
	dec := NewDecoder(strings.NewReader(in))
	req, err := dec.DecodeRequest()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Op != OpCapabilities {
		t.Fatalf("op = %q", req.Op)
	}
	if _, err := dec.DecodeRequest(); err != io.EOF {
		t.Fatalf("expected io.EOF at end, got %v", err)
	}
}

// TestFileMediatedResult resolves a response whose payload lives in a file
// (result_file) rather than inline — the large-payload path.
func TestFileMediatedResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, []byte(`{"symbols":[{"path":"b.rb","kind":"method","name":"go","symbol":"B#go","start_line":4,"end_line":6}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := &Response{ID: 9, OK: true, ResultFile: path}
	var result StructureResult
	if err := resp.ResolveResult(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Symbols) != 1 || result.Symbols[0].Symbol != "B#go" {
		t.Fatalf("file-mediated result mismatch: %+v", result)
	}
}

// TestErrorResponseHelper checks the failure constructor and Error type.
func TestErrorResponseHelper(t *testing.T) {
	resp := NewErrorResponse(3, CodeInvalidParams, "missing root")
	if resp.OK {
		t.Fatal("error response marked ok")
	}
	if got := resp.Error.Error(); !strings.Contains(got, CodeInvalidParams) || !strings.Contains(got, "missing root") {
		t.Fatalf("Error() = %q", got)
	}
}
