// Command tds-provider-treesitter is the tds fallback structure provider: a
// resident process speaking the tds provider protocol v1 (JSONL over stdio) that
// extracts symbols and imports using tree-sitter grammars.
//
// It is a *separate binary* rather than part of the core because tree-sitter is
// a C library and its Go bindings require CGO, which would forfeit the core's
// CGO-free build and trivial cross-compilation. See
// docs/spikes/tds-4-static-build.md.
//
// Its contract is best-effort: it yields symbols for the languages it has
// grammars for and degrades to nothing otherwise. It never fails a batch — an
// unreadable file, an unsupported language, or a syntax error is reported as a
// per-file error alongside whatever symbols were still recoverable.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

const (
	protocolVersion = "1.0.0"
	providerName    = "tds-provider-treesitter"
	providerVersion = "0.1.0"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", providerName, err)
		os.Exit(1)
	}
}

// run reads one request object per line until stdin closes, writing one
// response object per line to stdout. stderr is reserved for logs.
func run(stdin *os.File, stdout *os.File) error {
	sc := bufio.NewScanner(stdin)
	// Requests carry file batches, which can exceed bufio's default 64KiB line cap.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	out := bufio.NewWriter(stdout)
	defer out.Flush()

	ex := newExtractor()
	defer ex.close()

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		resp := handle(ex, line)
		b, err := json.Marshal(resp)
		if err != nil {
			return fmt.Errorf("encoding response: %w", err)
		}
		if _, err := out.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("writing response: %w", err)
		}
		if err := out.Flush(); err != nil {
			return fmt.Errorf("flushing response: %w", err)
		}
	}
	return sc.Err()
}

// handle decodes and dispatches one request line to a response envelope.
func handle(ex *extractor, line []byte) response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(nil, "invalid_params", "invalid JSON: "+err.Error())
	}
	switch req.Op {
	case "capabilities":
		return okResponse(req.ID, capabilities())
	case "structure":
		var params structureParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorResponse(req.ID, "invalid_params", "invalid structure params: "+err.Error())
		}
		return okResponse(req.ID, ex.structure(params))
	default:
		return errorResponse(req.ID, "unsupported_op", fmt.Sprintf("unsupported op: %q", req.Op))
	}
}

func capabilities() capabilitiesResult {
	return capabilitiesResult{
		Protocol:        protocolVersion,
		Provider:        providerName,
		ProviderVersion: providerVersion,
		Languages:       supportedLanguages(),
		Operations:      []string{"capabilities", "structure"},
		// The fallback runs no analyzers; analysis is the native providers' job.
		Analyzers: []analyzerInfo{},
	}
}

// --- protocol wire types ---
//
// Deliberately duplicated from the core's internal/protocol rather than
// imported: a provider is an independent program that must build without the
// core's module (and its dependency set), exactly as the Ruby provider does.
// docs/protocol.md is the shared contract; internal/protocol/testdata holds the
// conformance fixtures both sides are checked against.

type request struct {
	ID     *int            `json:"id"`
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	ID     *int       `json:"id"`
	OK     bool       `json:"ok"`
	Result any        `json:"result,omitempty"`
	Error  *wireError `json:"error,omitempty"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type capabilitiesResult struct {
	Protocol        string         `json:"protocol"`
	Provider        string         `json:"provider"`
	ProviderVersion string         `json:"provider_version"`
	Languages       []string       `json:"languages"`
	Operations      []string       `json:"operations"`
	Analyzers       []analyzerInfo `json:"analyzers"`
}

type analyzerInfo struct {
	Name        string   `json:"name"`
	Tool        string   `json:"tool"`
	Available   bool     `json:"available"`
	ToolVersion string   `json:"tool_version,omitempty"`
	Views       []string `json:"views"`
}

type structureParams struct {
	Root   string   `json:"root"`
	Commit string   `json:"commit,omitempty"`
	Files  []string `json:"files"`
}

type structureResult struct {
	Symbols     []symbol     `json:"symbols"`
	Imports     []importEdge `json:"imports"`
	Entrypoints []entrypoint `json:"entrypoints"`
	FileErrors  []fileError  `json:"file_errors"`
}

type symbol struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	BodyHash  string `json:"body_hash,omitempty"`
}

type importEdge struct {
	Path   string `json:"path"`
	Target string `json:"target"`
	Kind   string `json:"kind,omitempty"`
}

type entrypoint struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

type fileError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func okResponse(id *int, result any) response {
	return response{ID: id, OK: true, Result: result}
}

func errorResponse(id *int, code, message string) response {
	return response{ID: id, OK: false, Error: &wireError{Code: code, Message: message}}
}
