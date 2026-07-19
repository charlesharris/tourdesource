# tds provider protocol v1

The contract the `tds` core uses to drive language **providers** (Ruby, JS,
tree-sitter fallback) as out-of-process programs. Canonical Go types live in
`internal/protocol`; conformance fixtures in `internal/protocol/testdata`.
See design.md §9. **Current version: `1.1.0`.**

## Transport & framing

- A provider is a program the core launches and keeps resident. The core writes
  **requests** to the provider's **stdin** and reads **responses** from its
  **stdout**.
- Framing is **newline-delimited JSON (JSONL)**: exactly one JSON object per
  line, `\n`-terminated. (`encoding/json` escapes embedded newlines, so any
  payload stays single-line.)
- **stderr is for provider logs only** — never protocol traffic. Mixing them
  corrupts the stream.
- Every request has a monotonic integer `id`; the matching response echoes it.
  Ids let the core pipeline multiple in-flight requests.

## Version negotiation

The first exchange is `capabilities`. The core sends the protocol **majors** it
speaks (`core_protocols`); the provider replies with the concrete `protocol` it
will speak. The core accepts the provider iff that version's **major** is one it
supports (`SupportedMajors`). Same major ⇒ compatible (minor/patch are additive);
different major ⇒ the core refuses the provider and falls back.

## Messages

### Request (core → provider)

```json
{ "id": 2, "op": "structure", "params": { ... } }
```

### Response (provider → core)

```json
{ "id": 2, "ok": true, "result": { ... } }          // inline result
{ "id": 2, "ok": true, "result_file": "/tmp/r.json" } // file-mediated result
{ "id": 4, "ok": false, "error": { "code": "...", "message": "..." } }
```

**File-mediated results:** for large payloads (e.g. tens of thousands of
symbols) the provider may write the result JSON to a file and return its path in
`result_file` instead of inlining `result`. The core reads whichever is present.

## Operations

### `capabilities`

Request params:

```json
{ "core_protocols": ["1"] }
```

Result:

```json
{
  "protocol": "1.1.0",
  "provider": "tds-provider-ruby",
  "provider_version": "0.1.0",
  "languages": ["ruby"],
  "operations": ["capabilities", "structure", "analyze"],
  "analyzers": [
    { "name": "rubocop", "tool": "rubocop", "available": true,
      "tool_version": "1.65.0", "views": ["annotations", "panel"] }
  ]
}
```

An analyzer whose tool isn't installed is still advertised with
`"available": false` (no `tool_version`) so the core can report what *would* run.

### `structure`

Request params — a **batch** of files at a pinned commit:

```json
{ "root": "/repo", "commit": "0c2cdd9",
  "files": ["app/models/invoice.rb", "app/controllers/webhooks_controller.rb"] }
```

Result:

```json
{
  "symbols": [
    { "path": "app/models/invoice.rb", "kind": "class", "name": "Invoice",
      "symbol": "Invoice", "start_line": 2, "end_line": 18, "body_hash": "sha256:…" },
    { "path": "app/models/invoice.rb", "kind": "method", "name": "finalize",
      "symbol": "Invoice#finalize", "start_line": 6, "end_line": 9 }
  ],
  "imports":     [ { "path": "…", "target": "…", "kind": "reference" } ],
  "entrypoints": [ { "path": "…", "kind": "rails-controller", "name": "WebhooksController#create" } ],
  "file_errors": [ { "path": "app/broken.rb", "message": "syntax error near line 3" } ]
}
```

- **`symbol`** is the normalized qualified path — `Class#method` (instance),
  `Class.method` (singleton), `Module::Class`. **Normalization is the provider's
  job**, keeping the core language-neutral.
- **`body_hash`** hashes the symbol's normalized text; the core uses it for drift
  detection (`tds check`, design §5.3).

### `analyze`

Request params:

```json
{ "root": "/repo", "commit": "0c2cdd9", "files": ["app/models/invoice.rb"],
  "analyzers": ["rubocop", "simplecov"],
  "config": { "rubocop": { "config_path": ".rubocop.yml" } } }
```

`analyzers` optionally restricts the run; `config` is opaque, provider-interpreted
(sourced from `tds.toml`).

Result:

```json
{
  "findings": [
    { "path": "app/models/invoice.rb", "start_line": 7, "end_line": 7,
      "symbol": "Invoice#finalize", "severity": "warning", "rule": "Style/GuardClause",
      "message": "…", "url": "https://…", "tool": "rubocop", "tool_version": "1.65.0",
      "view": "annotations" },
    { "path": "app/models/invoice.rb", "start_line": 1, "end_line": 18,
      "symbol": "Invoice", "severity": "info", "rule": "coverage.line",
      "message": "62% line coverage", "tool": "simplecov", "tool_version": "0.22.0",
      "view": "heatmap", "value": 62.0 }
  ],
  "analyzer_errors": [ { "analyzer": "brakeman", "message": "brakeman not installed" } ]
}
```

- **`view`** ∈ `annotations | heatmap | panel | badge`.
- **`severity`** ∈ `error | warning | info`.
- **`value`** is the numeric for `heatmap`/`badge` (coverage %, complexity score);
  omitted for `annotations`/`panel`.
- The core resolves each finding's line range to a `symbol` via the map where the
  provider hasn't already.

## Error & partial-result semantics

Two distinct channels:

1. **Operation-level failure** — `ok: false` with `error.code` ∈
   `unsupported_op | invalid_params | incompatible_protocol | internal`. The whole
   op failed.
2. **Partial results** — `ok: true`, but the result carries `file_errors`
   (structure) or `analyzer_errors` (analyze). A single bad file or a missing tool
   never fails the batch; it's reported alongside the good results.

## Conformance

`internal/protocol/testdata/*.json` are the canonical examples, strict-decoded
against the Go types in `conformance_test.go` (unknown fields rejected), so a
provider can validate its output against them and any schema drift fails the
build.
