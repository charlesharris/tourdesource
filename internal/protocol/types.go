package protocol

import "encoding/json"

// --- capabilities ---

// CapabilitiesParams is the core's half of the version handshake: the protocol
// majors it can speak. The provider replies with a concrete Capabilities.
type CapabilitiesParams struct {
	CoreProtocols []string `json:"core_protocols"` // e.g. ["1"]
}

// Capabilities is a provider's self-description, returned from the capabilities
// op. Protocol is the concrete version the provider will speak.
type Capabilities struct {
	Protocol        string         `json:"protocol"`
	Provider        string         `json:"provider"`
	ProviderVersion string         `json:"provider_version"`
	Languages       []string       `json:"languages"`
	Operations      []string       `json:"operations"`
	Analyzers       []AnalyzerInfo `json:"analyzers"`
}

// AnalyzerInfo describes one analyzer a provider offers and whether its
// underlying tool is installed on this host.
type AnalyzerInfo struct {
	Name        string   `json:"name"`
	Tool        string   `json:"tool"`
	Available   bool     `json:"available"`
	ToolVersion string   `json:"tool_version,omitempty"`
	Views       []string `json:"views"`
}

// --- structure ---

// StructureParams asks a provider to extract structure for a batch of files at a
// pinned commit. Files are batched so the provider amortizes startup/IPC.
type StructureParams struct {
	Root   string   `json:"root"`
	Commit string   `json:"commit,omitempty"`
	Files  []string `json:"files"`
}

// StructureResult is the structural index for the requested files. Per-file
// failures are reported in FileErrors without failing the whole batch.
type StructureResult struct {
	Symbols     []Symbol     `json:"symbols"`
	Imports     []Import     `json:"imports,omitempty"`
	Entrypoints []Entrypoint `json:"entrypoints,omitempty"`
	FileErrors  []FileError  `json:"file_errors,omitempty"`
}

// Symbol is a resolved code symbol with a normalized qualified path (e.g.
// "Invoice#finalize", "Invoice.overdue", "Billing::Invoice"). BodyHash is a hash
// of the symbol's normalized text, used for drift detection (design §5.3).
type Symbol struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"` // class | module | method | function | ...
	Name      string `json:"name"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	BodyHash  string `json:"body_hash,omitempty"`
}

// Import is a dependency edge from a file to another file or module.
type Import struct {
	Path   string `json:"path"`
	Target string `json:"target"`
	Kind   string `json:"kind,omitempty"`
}

// Entrypoint is a language-specific root (Rails route/controller, React entry
// component, CLI main, ...).
type Entrypoint struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// FileError is a per-file failure inside an otherwise successful batch.
type FileError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// --- analyze ---

// AnalyzeParams asks a provider to run its analyzers over a batch of files.
// Analyzers optionally restricts which analyzers run; Config is opaque,
// provider-interpreted configuration (from tds.toml).
type AnalyzeParams struct {
	Root      string          `json:"root"`
	Commit    string          `json:"commit,omitempty"`
	Files     []string        `json:"files"`
	Analyzers []string        `json:"analyzers,omitempty"`
	Config    json.RawMessage `json:"config,omitempty"`
}

// AnalyzeResult is the normalized findings from the run. Per-analyzer failures
// are reported in AnalyzerErrors without failing the whole batch.
type AnalyzeResult struct {
	Findings       []Finding       `json:"findings"`
	AnalyzerErrors []AnalyzerError `json:"analyzer_errors,omitempty"`
}

// Finding is one normalized analyzer result mapped to a location (and, where the
// core can resolve it, a symbol). Value carries the numeric for heatmap/badge
// views (coverage %, complexity score); it is nil for annotation/panel findings.
type Finding struct {
	Path        string   `json:"path"`
	StartLine   int      `json:"start_line"`
	EndLine     int      `json:"end_line"`
	Symbol      string   `json:"symbol,omitempty"`
	Severity    string   `json:"severity"`
	Rule        string   `json:"rule"`
	Message     string   `json:"message"`
	URL         string   `json:"url,omitempty"`
	Tool        string   `json:"tool"`
	ToolVersion string   `json:"tool_version,omitempty"`
	View        string   `json:"view"`
	Value       *float64 `json:"value,omitempty"`
}

// AnalyzerError is a per-analyzer failure inside an otherwise successful run.
type AnalyzerError struct {
	Analyzer string `json:"analyzer"`
	Message  string `json:"message"`
}
