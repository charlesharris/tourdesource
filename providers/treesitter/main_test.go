package main

import (
	"encoding/json"
	"testing"
)

// decodeResult re-decodes a response's result into v, exercising the same JSON
// shape the core reads off the wire.
func decodeResult(t *testing.T, resp response, v any) {
	t.Helper()
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshaling result: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}
}

func TestCapabilitiesHandshake(t *testing.T) {
	ex := newExtractor()
	defer ex.close()

	resp := handle(ex, []byte(`{"id":1,"op":"capabilities","params":{"core_protocols":["1"]}}`))
	if !resp.OK {
		t.Fatalf("capabilities failed: %+v", resp.Error)
	}
	if resp.ID == nil || *resp.ID != 1 {
		t.Fatalf("response id = %v, want 1", resp.ID)
	}

	var caps capabilitiesResult
	decodeResult(t, resp, &caps)

	if caps.Protocol != protocolVersion {
		t.Errorf("protocol = %q, want %q", caps.Protocol, protocolVersion)
	}
	if caps.Provider != providerName {
		t.Errorf("provider = %q, want %q", caps.Provider, providerName)
	}
	if len(caps.Languages) == 0 {
		t.Error("capabilities advertised no languages")
	}
	// The fallback runs no analyzers, but the field must still serialize as a
	// list rather than null — the core decodes it unconditionally.
	if caps.Analyzers == nil {
		t.Error("analyzers must be an empty list, not null")
	}
}

func TestHandleRejectsUnsupportedOp(t *testing.T) {
	ex := newExtractor()
	defer ex.close()

	// `analyze` is a valid protocol op this provider deliberately does not serve:
	// it must be refused by name, not crash the process.
	resp := handle(ex, []byte(`{"id":7,"op":"analyze","params":{}}`))
	if resp.OK {
		t.Fatal("expected analyze to be refused")
	}
	if resp.ID == nil || *resp.ID != 7 {
		t.Errorf("error response must echo the request id, got %v", resp.ID)
	}
	if resp.Error == nil || resp.Error.Code != "unsupported_op" {
		t.Errorf("error = %+v, want code unsupported_op", resp.Error)
	}
}

func TestHandleRejectsMalformedInput(t *testing.T) {
	ex := newExtractor()
	defer ex.close()

	t.Run("invalid json", func(t *testing.T) {
		resp := handle(ex, []byte(`{"id":1,"op":`))
		if resp.OK || resp.Error == nil || resp.Error.Code != "invalid_params" {
			t.Errorf("got %+v, want an invalid_params error", resp)
		}
	})

	t.Run("invalid structure params", func(t *testing.T) {
		resp := handle(ex, []byte(`{"id":2,"op":"structure","params":{"files":"not-a-list"}}`))
		if resp.OK || resp.Error == nil || resp.Error.Code != "invalid_params" {
			t.Errorf("got %+v, want an invalid_params error", resp)
		}
	})
}

// TestStructureResultSerializesEmptyLists guards the wire contract: the core
// decodes symbols/imports/entrypoints unconditionally, so an empty batch must
// produce `[]`, never `null`.
func TestStructureResultSerializesEmptyLists(t *testing.T) {
	ex := newExtractor()
	defer ex.close()

	resp := handle(ex, []byte(`{"id":3,"op":"structure","params":{"root":".","files":[]}}`))
	if !resp.OK {
		t.Fatalf("structure failed: %+v", resp.Error)
	}
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshaling result: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}
	for _, field := range []string{"symbols", "imports", "entrypoints", "file_errors"} {
		got, ok := raw[field]
		if !ok {
			t.Errorf("result is missing %q", field)
			continue
		}
		if string(got) != "[]" {
			t.Errorf("%s = %s, want []", field, got)
		}
	}
}
