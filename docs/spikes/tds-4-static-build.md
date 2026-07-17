# Spike TDS-4 — static build (CGO-free SQLite + tree-sitter)

**Question:** Can a pure-Go SQLite driver and tree-sitter compile into one static,
cross-compilable `tds` binary?

**Answer:** SQLite yes; tree-sitter no. Putting tree-sitter *inside* the core
would forfeit both the CGO-free core and easy cross-compilation. **Decision:
keep tree-sitter out of the core and ship the tree-sitter fallback as a separate
provider binary** (same shape as the Ruby/JS providers).

## What was tested

- `modernc.org/sqlite` v1.54.0 — pure-Go SQLite driver.
- `github.com/tree-sitter/go-tree-sitter` v0.25.0 + `tree-sitter-go` v0.25.0 grammar.
- Go 1.26, host darwin/arm64.

## Findings

| Check | Result |
|---|---|
| modernc SQLite: open DB, create table, round-trip a row | ✅ works, `CGO_ENABLED=0` |
| Core (`main` + `internal/cli`) build | ✅ CGO-free |
| Core cross-compile darwin→linux/amd64 and darwin/arm64 | ✅ `CGO_ENABLED=0`, exit 0 |
| tree-sitter parse (Go source → tree, extracted a func name) | ✅ works **natively** with CGO |
| tree-sitter build with `CGO_ENABLED=0` | ❌ `build constraints exclude all Go files` — the bindings are CGO-gated |
| tree-sitter cross-compile darwin→linux with `CGO_ENABLED=1` | ❌ fails: no cross C toolchain (`runtime/cgo` asm errors) |

**Root cause:** tree-sitter is a C library; its Go bindings require CGO. CGO
(a) excludes the package entirely when `CGO_ENABLED=0`, and (b) makes
single-host cross-compilation require a target C toolchain. It also means a
CGO binary is never *fully* static on macOS (links libSystem).

## Decision & consequences

**The core `tds` binary stays pure-Go / CGO-free and cross-compiles trivially.**
tree-sitter moves to the **fallback provider**, which becomes a *separate
out-of-process binary* built per-OS on native CI runners — not compiled into
the core. This is consistent with the provider architecture (native Ruby/JS
providers are already separate programs); "compiled into the core" was a
convenience we drop.

Amends the design:
- `docs/design.md` §9.3 — fallback provider is a separate binary, not compiled in.
- `docs/design.md` §10 — core is CGO-free (modernc SQLite); tree-sitter/CGO is
  confined to the fallback-provider binary.
- Implementation plan **TDS-11** — "in-core tree-sitter provider" → "separate
  fallback-provider binary."

**Alternatives considered:**
- *CGO in the core, build on native CI runners.* Simpler short-term, but gives
  up the CGO-free core, complicates every build with a C toolchain, and makes
  pure-static Linux builds fiddly (musl / `-extldflags -static`). Rejected.
- *wazero + WASM grammars* (grammars compiled to `.wasm`, run by a pure-Go WASM
  runtime) — would keep even the fallback CGO-free. More exotic; the query API
  over WASM is more limited. **Parked** as a future option for the fallback
  provider if we want it CGO-free too.

## Reproduction

The kept `internal/spike/staticbuild` package validates the CGO-free SQLite path
(`go test ./internal/spike/...`). The tree-sitter probe was:

```go
parser := ts.NewParser()
parser.SetLanguage(ts.NewLanguage(tsgo.Language()))
tree := parser.Parse([]byte("package main\nfunc Greet() string { return \"hi\" }\n"), nil)
// tree.RootNode().Kind() == "source_file"; extracted func name "Greet"
```

It ran green natively; it is intentionally **not** kept in the core module so the
core stays CGO-free. tree-sitter returns in TDS-11 in its own provider binary.
