# tds-provider-treesitter

The **fallback structure provider** for [tour-de-source](../../) (`tds`). A
separate program the `tds` core launches and drives over the [provider protocol
v1](../../docs/protocol.md) (JSONL over stdio).

Where the Ruby and JS providers know their ecosystems deeply, this one knows
many languages shallowly. It is what keeps `tds map` useful on a repo nothing
else covers.

## Why it is a separate binary

tree-sitter is a C library and its Go bindings require CGO, which would forfeit
both the core's CGO-free build and its trivial cross-compilation. It therefore
lives in **its own Go module** — the core never imports it — and builds natively
per-OS. See [spike TDS-4](../../docs/spikes/tds-4-static-build.md).

## What it does

- **`capabilities`** — advertises protocol version and the languages it has
  grammars for: `c`, `cpp`, `go`, `java`, `javascript`, `python`, `ruby`, `rust`,
  `typescript`.
- **`structure`** — per language:
  - **symbols** — classes/modules/methods/functions with normalized qualified
    paths, line ranges, and a `body_hash` for drift detection;
  - **imports** — `import` / `use` / `#include` / `require` edges.
- **`analyze`** — not served. Analyzers are the native providers' job.

It claims `ruby`, `javascript` and `typescript` even though those have native
providers: `Discover` registers this provider **last**, so a native provider
always wins and the fallback only covers what nothing else does — including the
case where the native provider is missing or failed to launch.

### Normalization

Qualified names match what the native providers emit, so an anchor keeps
resolving if a repo gains or loses a native provider:

| Language | Example |
|---|---|
| Ruby | `Billing::Invoice#finalize`, `Billing::Invoice.overdue` |
| Go | `Invoice.Finalize` (qualified by receiver type, not lexical scope) |
| Python / Java / JS / TS | `Invoice.finalize` |
| Rust | `billing::Invoice::finalize` (`impl` names its members without appearing itself) |
| C++ | `billing::Invoice::finalize` (out-of-line definitions are not double-qualified) |

Entrypoint detection is deliberately **not** attempted: framework conventions
are exactly the knowledge a native provider has and this one doesn't.

## Degradation contract

The provider never fails a batch. Every failure is per-file, reported in
`file_errors` alongside whatever symbols were still recoverable:

| Input | Behaviour |
|---|---|
| Language with no grammar (`.txt`, binary) | silently skipped — not an error |
| Unreadable / missing file | `file_errors` entry |
| Syntax error | `file_errors` entry, **plus** the symbols tree-sitter recovered |
| File over 4 MiB | skipped with a `file_errors` entry (parsing allocates several times the source size) |

## Build & run

```sh
make provider-treesitter     # from the repo root, into ./bin
./bin/tds-provider-treesitter   # reads JSONL requests on stdin, writes responses on stdout
```

Requires a C toolchain (`CGO_ENABLED=1`). The core finds it on `PATH` as
`tds-provider-treesitter`, or via `TDS_PROVIDER_TREESITTER=/path/to/binary`.

## Test

```sh
go test ./...
```
