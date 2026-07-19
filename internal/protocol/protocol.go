// Package protocol defines the tds provider protocol v1: the versioned JSON
// contract the core uses to drive language providers (Ruby, JS, tree-sitter
// fallback) as out-of-process programs.
//
// Transport is newline-delimited JSON (JSONL) over the provider's stdio: the
// core writes one request object per line to the provider's stdin and reads one
// response object per line from its stdout. stderr is reserved for provider
// logs and must never carry protocol traffic. Requests and responses are
// correlated by a monotonic id, so a provider may pipeline work.
//
// Large results (e.g. tens of thousands of symbols) may be file-mediated: the
// provider writes the result JSON to a file and returns its path in
// result_file instead of inlining it in result. See docs/protocol.md.
package protocol

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Version is the protocol version this build of the core speaks (semver).
// Version 1.1.0 added AnalyzerInfo.Incremental. The field is additive and
// defaults to false, so a 1.0.0 provider keeps working — it simply never gets
// its findings cached.
const Version = "1.1.0"

// SupportedMajors lists protocol major versions the core can talk. A provider is
// compatible if its advertised protocol's major is in this set.
var SupportedMajors = []string{"1"}

// Operations.
const (
	OpCapabilities = "capabilities"
	OpStructure    = "structure"
	OpAnalyze      = "analyze"
)

// View kinds a finding can feed (see design §8).
const (
	ViewAnnotations = "annotations"
	ViewHeatmap     = "heatmap"
	ViewPanel       = "panel"
	ViewBadge       = "badge"
)

// Finding severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// Error codes returned in Response.Error.Code.
const (
	CodeUnsupportedOp        = "unsupported_op"
	CodeInvalidParams        = "invalid_params"
	CodeIncompatibleProtocol = "incompatible_protocol"
	CodeInternal             = "internal"
)

// MajorOf returns the major component of a semver string ("1.2.3" -> "1").
func MajorOf(version string) string {
	return strings.SplitN(version, ".", 2)[0]
}

// Compatible reports whether the core can speak a provider's advertised protocol
// version (major-version match). This is the version-negotiation check the core
// runs on the capabilities handshake.
func Compatible(providerProtocol string) bool {
	major := MajorOf(providerProtocol)
	for _, m := range SupportedMajors {
		if m == major {
			return true
		}
	}
	return false
}

// Request is one protocol message from core to provider.
type Request struct {
	ID     int             `json:"id"`
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is one protocol message from provider to core. Exactly one of Result,
// ResultFile, or Error is meaningful: Error when OK is false; otherwise the
// result is inline in Result unless file-mediated via ResultFile.
type Response struct {
	ID         int             `json:"id"`
	OK         bool            `json:"ok"`
	Result     json.RawMessage `json:"result,omitempty"`
	ResultFile string          `json:"result_file,omitempty"`
	Error      *Error          `json:"error,omitempty"`
}

// Error is a structured, operation-level failure (distinct from per-file /
// per-analyzer partial errors carried inside a successful result).
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// NewRequest builds a request with typed params marshaled into Params.
func NewRequest(id int, op string, params any) (*Request, error) {
	r := &Request{ID: id, Op: op}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		r.Params = raw
	}
	return r, nil
}

// Bind unmarshals the request's params into v.
func (r *Request) Bind(v any) error {
	if len(r.Params) == 0 {
		return nil
	}
	return json.Unmarshal(r.Params, v)
}

// NewResponse builds a successful response with an inline result.
func NewResponse(id int, result any) (*Response, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &Response{ID: id, OK: true, Result: raw}, nil
}

// NewErrorResponse builds a failed response.
func NewErrorResponse(id int, code, message string) *Response {
	return &Response{ID: id, OK: false, Error: &Error{Code: code, Message: message}}
}

// ResolveResult decodes a successful response's payload into v, transparently
// reading ResultFile when the result was file-mediated. Returns the structured
// Error if the response is a failure.
func (r *Response) ResolveResult(v any) error {
	if !r.OK {
		if r.Error != nil {
			return r.Error
		}
		return fmt.Errorf("response %d not ok and carried no error", r.ID)
	}
	data := []byte(r.Result)
	if r.ResultFile != "" {
		b, err := os.ReadFile(r.ResultFile)
		if err != nil {
			return fmt.Errorf("reading result_file: %w", err)
		}
		data = b
	}
	if len(data) == 0 {
		return fmt.Errorf("response %d ok but carried no result", r.ID)
	}
	return json.Unmarshal(data, v)
}
