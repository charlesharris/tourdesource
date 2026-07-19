# tour-de-source — Design Document

**Status:** Draft for review
**Author:** Charles Harris (with Claude)
**Date:** 2026-07-17
**CLI name:** `tds`

---

## 1. Summary

**tour-de-source** (`tds`) analyzes a source repository and produces a **shareable, interactive "tour"** of the project. A tour is guided narration anchored to real code — an author writes (with heavy AI assistance) an ordered walk through the codebase, and `tds` compiles it into a self-contained static site that a reader can follow on-rails *or* wander off of to browse the whole repo freely.

The product's north star is that a tour is **shareable both as content and as an experience**: the source of a tour is diffable Markdown you can review in a PR; the output is a frozen artifact you can email, host anywhere, or publish as an artifact and hand to someone.

Target use cases, in eventual priority order: **onboarding, code review, demos, interviews.** **v1 is designed around onboarding / repo orientation**; the others are later authoring/rendering modes over the same core.

---

## 2. Goals and non-goals

### Goals (v1)

- Turn a repo into a high-quality **onboarding tour** with minimal human effort — AI drafts, human curates in ~20 minutes.
- Produce a **self-contained static bundle** that is correct forever about its own snapshot and works with no server and no network.
- Anchors that survive normal code churn (**symbol-first**, line-range fallback).
- A viewer that supports **guided narration + free browsing** of the whole repo.
- A **pluggable analysis pipeline**: per-language **providers** (native programs behind a JSON protocol) run existing tools (linters, security scanners, type checkers, coverage/complexity) and surface each tool's output as a toggleable **view** layered over the tour.
- Ship the core as a **single Go binary** that dispatches to language providers — deep analysis stays native to each language. `tds build` additionally requires **Hugo extended ≥ 0.128** on PATH: the site is rendered from Hugo templates, so Hugo is a build dependency of the core, not an optional extra.
- A clean, inspectable **pipeline** where the analysis and build stages have no AI or network dependency (only drafting talks to Claude).
- Use the author's **Claude subscription** for drafting (no API key management).

### Non-goals (v1)

- Deep semantic analysis (full call graphs, dataflow, type inference). We do lightweight, multi-language structural analysis only.
- Live / auto-updating tours that track a moving branch. Tours are immutable, SHA-pinned artifacts by design.
- A hosted service, accounts, or collaboration backend. Sharing is "hand someone a bundle."
- An in-browser tour *editor*. Authoring is file-based (Markdown) in v1.
- Language coverage beyond the v1 set with full symbol support (others degrade gracefully).

---

## 3. Core concepts and vocabulary

- **Tour** — an ordered, mostly-linear walk through a repo, pinned to a single commit SHA. Has a title, intro, metadata, and a list of chapters.
- **Chapter** — a named, ordered group of stops (e.g. "Follow one request end to end").
- **Stop** — the atomic unit: prose + one or more **anchors** into code, with an optional **focus** (a highlighted sub-range within the anchored symbol). Stops are the things the viewer navigates between.
- **Side-quest / detour** — an optional, collapsible branch hanging off a stop (e.g. a role-specific deep dive). Keeps the spine linear while allowing depth.
- **Anchor** — a durable reference to a location in code. **Symbol-first** (`path/to/file.rb::Class#method`), resolved at build time by a language provider (native parser; tree-sitter fallback), with a **line-range fallback** (`path/to/file.rb:40-52`) where no provider resolves it.
- **Repo map** — the deterministic structural index `tds` builds: symbols, imports/dependencies, file tree, and git signals. Grounds both anchor resolution and AI drafting.
- **Core** — the single Go binary: dispatch, language detection, store, draft/tmux orchestration, site generation (data contract + embedded Hugo theme), tour format, anchor resolution. Language-agnostic; never parses code itself.
- **Provider** — an out-of-process program that, given `{repo, commit, files, config}`, returns **structure** (symbols/imports/entrypoints) and/or **findings** per a versioned JSON protocol. Native providers are best-in-class per language (a Ruby gem using prism + rubocop/brakeman; a Node package using the TS compiler + eslint/tsc). A built-in **tree-sitter fallback provider** (compiled into the core) covers everything else.
- **Analyzer** — a single external tool (rubocop, brakeman, eslint, tsc, coverage, complexity…) that a provider runs to produce findings. Each analyzer maps to one or more **views**.
- **Finding** — a normalized result from an analyzer: an anchor (file + line range, resolved to a symbol where possible), severity, rule/code, message, and optional URL.
- **View** — a toggleable lens over the tour/repo derived from findings: inline **annotations**, a line/file **heatmap**, a browsable **panel**, or a per-symbol **badge**. Views render in the viewer and can be referenced from stops.
- **Bundle** — the compiled static-site output: viewer + tour manifest + embedded repo-at-SHA + selected views.
- **Template** — an opinionated tour skeleton (v1: `onboarding`) that drafting fills in. First-class extension point for future modes (`review`, `demo`).

---

## 4. The tour format (source of truth)

Tours are authored as **Markdown** (`*.tour.md`) with YAML frontmatter. Markdown is the source of truth because it is diffable, PR-reviewable, and *is itself* shareable content. The build compiles it to a JSON **manifest** the viewer consumes; the manifest is a build artifact, never hand-edited.

### 4.1 File shape

```markdown
---
title: "A tour of Acme's billing service"
template: onboarding
repo: .
commit: auto          # resolved to a concrete SHA at build time
audience: "new backend engineers"
---

# Chapter: The 30-second version

Acme Billing turns usage events into invoices. Everything flows through
one queue and one state machine.

::stop{anchor="app/models/invoice.rb::Invoice" focus="def finalize"}
`Invoice` is the aggregate root. The whole domain is really "get an
Invoice from draft to finalized without double-charging."
::

# Chapter: Follow one invoice end to end

::stop{anchor="app/controllers/webhooks_controller.rb::WebhooksController#create"}
Usage events arrive here. Note we never touch the DB directly — we enqueue.
::

::stop{anchor="app/jobs/finalize_invoice_job.rb::FinalizeInvoiceJob#perform"}
The job is where the state machine actually turns.

::detour{title="If you're debugging a stuck invoice"}
Stuck invoices are almost always here — the lock in `with_lock` below.
::stop{anchor="app/models/invoice.rb::Invoice#with_lock"}
...
::
::
::
```

### 4.2 Anchor grammar

- **Symbol anchor:** `path::SymbolPath` where `SymbolPath` uses language-natural separators the resolver normalizes (`Class#method` / `Class.method` / `Module::Class` / bare `function_name`).
- **Focus** (optional): a nested symbol, a substring to highlight, or an explicit line sub-range within the resolved symbol.
- **Line-range fallback:** `path:START-END` — always valid, used where no grammar applies or a symbol won't resolve.

### 4.3 Directive summary

| Directive | Meaning |
|---|---|
| `# Chapter: <name>` | starts a chapter |
| `::stop{anchor=… focus=…} … ::` | a stop: attrs + prose body |
| `::detour{title=…} … ::` | a collapsible side-quest; may contain stops |
| frontmatter | tour metadata + template + commit pin |

Findings from `tds analyze` attach to a stop automatically via its anchored symbol (rendered as a **badge**, §8) — no directive needed. A stop may *optionally* deep-link a view with `view="<id>"` (e.g. open the security panel filtered to this symbol) to tie narration to instrumentation.

---

## 5. Anchoring and the staleness model

Staleness is the failure mode that kills tools like this, so it is a first-class design concern.

### 5.1 Two independent axes

1. **Internal correctness** — is the tour true about the code it ships with? **Always yes, by construction.** The bundle embeds the repo *at the pinned SHA* and anchors are resolved against that exact snapshot at build time. A shared bundle can never lie about itself.
2. **Freshness** — how far has the live branch moved since the tour was cut? A *maintenance* concern for the author, never a correctness problem for the reader.

Embedding the whole repo at a pinned SHA is therefore **load-bearing**, not a nicety: it's what makes bundles immutable and self-true.

### 5.2 Symbol-first durability

Because anchors resolve by symbol, ordinary edits (lines shifting, code above changing) don't break anything — the symbol is re-found wherever it now lives. Only genuine **renames and deletes** break an anchor. This is the payoff of resolving anchors by symbol via native parsers (prism for Ruby, the TypeScript compiler for JS/TS), with tree-sitter as the fallback resolver.

### 5.3 `tds check` — the author's drift tool

Run against a live checkout, `tds check` re-resolves every anchor at HEAD and classifies each:

- **clean** — symbol resolves unchanged.
- **moved** — symbol found, but its body changed materially (heuristic: hash of the symbol's normalized text differs) → prose may need a look.
- **broken** — symbol gone (renamed/deleted) → author must fix or regenerate.

`check` produces a report and a non-zero exit on `broken`, so it can gate CI for repos that keep a canonical tour in-tree. Readers never see any of this.

---

## 6. Architecture and pipeline

Discrete, inspectable stages. The **core** (one Go binary) orchestrates; `map` and `analyze` **delegate to language providers** over the JSON protocol (§9). **Only `draft` touches Claude or the network.** `map`, `analyze`, `build`, and `check` are deterministic and offline — providers run local tools but make no network calls — which makes the whole system testable and lets someone hand-author a tour with zero AI in the loop.

```
                        ┌── ruby provider (gem): prism + rubocop/brakeman/…
   repo ─▶ tds map ─────┤── js provider (node): TS compiler + eslint/tsc/…
   repo ─▶ tds analyze ─┤── tree-sitter fallback (built into core)
           (via         └──────────────┐
            providers)                 ▼
                        store (SQLite + JSON): symbols, imports,
                        git signals, findings
                                  │
                                  ▼
                        tds draft ◀── Claude via tmux ──▶ *.tour.md drafts
                                  │  (human curates the .tour.md)
                                  ▼
   *.tour.md ─────────────▶ tds build ─▶ static bundle: viewer + manifest
                                  ▲          + repo@SHA + selected views
   checkout ─▶ tds check ─▶ drift report
```

### 6.1 `tds map` — deterministic repo map

Builds the structural index. **No AI, no network.** The core walks the tree, computes git signals, **detects languages**, and calls each language provider's `structure` operation; providers return symbols/imports/entrypoints from their native parsers, which the core merges into the store.

Inputs: a repo (working tree at a chosen commit).
Outputs: `.tds/map.sqlite` plus a `.tds/map.json` export (portability/debugging).

Contents:
- **Symbols** *(from providers)* — kind, name, qualified symbol path, file, line range, and a normalized-body hash (for drift detection). Native parsers (prism, TS compiler) where available; tree-sitter fallback otherwise.
- **Imports / dependency edges** *(from providers)* — file-to-file and module-level where cheaply available.
- **Entrypoints** *(from providers)* — language-specific roots (Rails routes/controllers, React entry/route components, CLI mains) plus README presence.
- **File tree** *(core)* — every file, size, detected language, and a "significance" hint.
- **Git signals** *(core)* — churn, age, last-touched, primary authors (via `git log`). Feeds ordering heuristics and "landmarks."

`map` is deep enough to ground anchors and drafting, and no deeper. **Explicitly out of scope:** call graphs, dataflow, type resolution.

**Storage:** SQLite is the store (queryable, scales, and the natural substrate for a future Datasette-style `tds serve`); JSON export exists for portability and diffing.

### 6.2 `tds analyze` — pluggable tool analysis

Calls each language provider's `analyze` operation; providers run their **analyzers** (existing tools) and return **findings**, which the core writes into the same store. **No AI, no network** — just local tool invocations inside the providers. Depends on `map` (findings are mapped onto symbols). See §9 for the provider protocol and built-in analyzers.

- **Opt-in and configurable.** Which analyzers run is controlled by `tds.toml` (enable/disable/configure per repo). `tds analyze` with no config runs every available analyzer for the repo's detected languages.
- **Availability-gated.** A provider reports which of its analyzers' tools are installed (and their versions). A missing tool is skipped with a note, never an error.
- **Failure-isolated.** A crashing or misconfigured analyzer — or an entire provider that fails to launch — degrades to "no findings from that source" and is reported; it never breaks the stage or the other analyzers.
- **Cached per tool.** Findings are keyed by (tool, tool version, file hash) so re-runs only re-analyze changed files.
- **SHA-pinned and attributed.** Findings record the tool + version and the commit, so every view is reproducible and its provenance is shown in the bundle.

Findings serve three consumers: **views** in the viewer (§8), **grounding** for `draft` (complexity hotspots, lint-heavy or security-flagged code are strong stop candidates), and the author's own read of the repo's health.

### 6.3 `tds draft` — AI-assisted drafting via tmux

Produces `*.tour.md` drafts for a human to curate. This is the only AI stage.

**Orchestration (auth-preserving, file-mediated):**

1. `tds` ensures a tmux pane running `claude --dangerously-skip-permissions` in the repo dir (create or attach). This keeps generation on the author's **Claude subscription** — no API key.
2. **Wait for readiness.** `tds` polls `capture-pane` for the TUI's readiness marker before sending anything — keystrokes typed before the input box exists are silently lost (spike TDS-2).
3. `tds` writes a prompt to a file (e.g. `.tds/draft/PROMPT.md`) and any context it wants pinned.
4. `tds` sends Claude a short instruction via `tmux send-keys`: *"Read `.tds/draft/PROMPT.md`, write your answer to `.tds/draft/OUTPUT.json`, then create `.tds/draft/<nonce>.done`."*
5. **Completion is detected on the filesystem, not the pane:** `tds` polls for the `<nonce>.done` marker (written after the payload), then reads/parses `OUTPUT.json`. Scraping Claude's alternate-screen TUI for a printed sentinel is fragile; the `.done` marker is TUI-agnostic and avoids reading a half-written file (spike TDS-2).
6. Data crosses the boundary **through the filesystem**, never by scraping the TUI. `--dangerously-skip-permissions` lets Claude read/write those files without prompts. tmux is purely the input transport that preserves auth and gives the author an **observable** session they can watch or intervene in.

Rationale for not using `claude -p`: the tmux route is more robust and observable and sidesteps any billing/credential ambiguity; the file-mediated contract removes TUI-parsing fragility.

**Two-pass generation with hard anchor validation:**

- **Pass 1 — outline.** Feed the repo map + README + entrypoints. Ask for the chapter/stop skeleton (see §7 template) as structured data, with **proposed anchors drawn from real symbol IDs in the map**. Constraining anchors to existing symbols is the primary anti-hallucination lever.
- **Pass 2 — narrate.** For each stop, hand Claude the actual source of the anchored symbol + neighbors; ask for tight prose. Small, parallelizable units → better writing.
- **Validation gate.** Before writing any `.tour.md`, `tds` resolves every proposed anchor against the map. Non-resolving anchors are dropped or flagged. The emitted draft is **structurally guaranteed** to point at real code even where prose still needs editing.

### 6.4 `tds build` — compile to a static bundle

**No AI, no network.** Deterministic given (`*.tour.md`, repo@SHA).

Steps:
1. Resolve `commit: auto` to a concrete SHA; check out / read that snapshot.
2. Parse the `.tour.md`(s); resolve every anchor to concrete line ranges via the map.
3. **Syntax-highlight at build time** (a Go highlighter compiled into the core, e.g. chroma — independent of the providers) → ship pre-rendered token HTML. No highlighter JS, works with JS disabled for reading, zero runtime highlight cost.
4. Embed the **whole repo** so readers can wander anywhere. File blobs are **lazy-loaded**: a small manifest loads instantly; individual files fetch on demand.
5. Embed **selected views** — findings from the store for the enabled analyzers, plus each view's render metadata (annotation/heatmap/panel/badge). Which views ship is configurable; default is all views with findings.
6. Emit the bundle.

**Output form:** a **multi-page static site** (`public/`) rendered by Hugo from the embedded theme — overview, architecture map, explorer, a page per source file, the tour, and a symbol index. `relativeURLs = true`, so it works served from any subpath or opened from `file://`. There is no single-file variant (decision 19a).

### 6.5 `tds check` — drift report

See §5.3. Deterministic, offline, runs against a live checkout.

### 6.6 (later) `tds serve` — live mode

Datasette-style local server over `map.sqlite` — richer queries and cross-repo views. Out of scope for v1; the SQLite decision keeps the door open.

---

## 7. The onboarding tour template (content model)

The product's value is *sequencing and judgment*, not "here are your files." We ship an **opinionated onboarding skeleton** that drafting fills in, so output quality doesn't depend on the LLM inventing structure each time. Templates are **first-class** — future `review` and `demo` modes are new templates, giving the "use cases as modes" idea a real home.

**Onboarding skeleton (default):**

1. **The 30-second version** — what this is, what it does, the one diagram. Grounded in README + entrypoints.
2. **Follow one operation end to end** — the single most illuminating vertical slice through the system. The money chapter; a coherent trace teaches more than any architecture overview.
3. **The major landmarks** — 4–6 key modules/boundaries: why each exists, what to know. Ordered using git signals + entrypoint centrality.
4. **Where things live / conventions** — navigation, naming patterns, where tests are, how to run it.
5. **Side-quests** — role-specific detours (frontend / backend / ops / "I'm here to fix a bug").

When drafting fills this reliably and grounds each stop in real anchored symbols, human curation collapses to "fix and prune."

---

## 8. The viewer

A static, dependency-light two-pane app.

- **Left: narrative rail.** Chapters and stops as scrollable prose. Side-quests render as collapsible blocks.
- **Right: code pane.** Shows the anchored file with the focus range highlighted.

**Navigation:** **scrollytelling + keyboard stepping.** Scrolling the narrative drives the code pane to the active stop; `←/→` step stop-to-stop (first-class, so the demo/presenter use case isn't painted out). Reader-first (v1 = solo onboarding) but presenter-capable.

**Signature feature — on-rails ⟷ free browse.** Because the whole repo is embedded, the code pane doubles as a **file browser**. A reader can click off into any file, read around, then hit **"return to tour"** to snap back to where they left the guided path. The dual mode is what makes this better than a doc full of code links.

**Views — the analysis overlay.** Findings from `tds analyze` render as toggleable **views** the reader can layer over any file, on-rails or while free-browsing. v1 ships the full set:

- **Annotations** — inline/gutter markers on code lines (e.g. rubocop offenses, brakeman warnings, tsc/sorbet type errors). Click for the rule, message, and link.
- **Heatmap** — line/file shading for continuous signals (test coverage, complexity, churn). Turns "where's the risk?" into a glance.
- **Panel** — a browsable, filterable table of findings for a view (by file, severity, rule); jump from a row to the code.
- **Badge** — per-symbol summary chips shown on stops (e.g. "3 lint offenses · 62% covered · complexity 41"), so a stop carries its instrumentation inline.

A **view switcher** toggles views independently; multiple can be active. Each view shows its **provenance** (tool + version + commit). A stop may **reference a view** (e.g. deep-link into the security panel filtered to its symbol), tying narration to instrumentation.

**Other viewer properties:**
- Deep links to any stop or view state (shareable URL fragment).
- Chapter/stop outline for jump navigation.
- Pre-highlighted code (no runtime highlighter).
- Degrades to readable static content with JS off (views are progressive enhancement).

---

## 9. The provider system (core + native providers)

Analysis is factored as a language-agnostic **core** (one Go binary) plus **providers** — separate programs that do deep, native analysis behind a versioned JSON protocol. This is the LSP pattern applied to code tours: the core never parses code itself; it detects languages, dispatches to providers, and merges their results. "Each language is its own tool" — but as a *provider behind a contract*, not a fork of the whole product.

### 9.1 Why out-of-process, native providers

The best analysis of a language lives *in* that language: a Ruby provider can use **prism** (Ruby's official parser) for structure and call **rubocop/brakeman** as libraries; a Node provider can use the **TypeScript compiler API** + **react-docgen** and run **eslint/tsc** as libraries. Running providers out-of-process keeps the core a single static binary, isolates provider crashes, and lets each provider ship on its own ecosystem's release cadence.

### 9.2 The provider protocol

A provider is any executable that speaks the protocol (JSON over stdio, or file-mediated for large payloads). Operations:

| Op | Core sends | Provider returns |
|---|---|---|
| `capabilities` | — | protocol version, languages handled, analyzers offered + which tools are installed (with versions) |
| `structure` | `{repo, commit, files}` | `{symbols[], imports[], entrypoints[]}` — symbol paths already normalized to the anchor grammar |
| `analyze` | `{repo, commit, files, config}` | `{findings[]}` — each `{file, range, symbol?, severity, rule, message, url?, view}` |

The core **version-negotiates** via `capabilities`, resolves each finding to a symbol using the map, and stores results. Providers are **availability-gated**, **failure-isolated**, and **cached** per (tool, version, file hash) — see §6.2. Symbol-path normalization (`Class#method` / `Class.method` / `Module::Class` / bare) is the **provider's** responsibility, so the core stays language-neutral.

The protocol is finalized as **v1** in [`docs/protocol.md`](protocol.md) (JSONL transport, version negotiation, error/partial-result semantics, file-mediated large payloads); the canonical Go types and conformance fixtures live in `internal/protocol` (TDS-5).

### 9.3 Provider discovery & distribution

- **Tree-sitter fallback provider** — a *separate binary* (not compiled into the core), built per-OS. Gives symbols where a grammar exists and line-range anchors otherwise, so `tds` **never refuses a repo**. It lives outside the core because tree-sitter requires CGO, which would forfeit the CGO-free core and easy cross-compilation (spike TDS-4). Shipped alongside the core binary and discovered like any other provider; if it is somehow absent, unsupported languages degrade to line-range anchors only.
- **Native providers** — discovered on `PATH` (e.g. `tds-provider-ruby`, `tds-provider-js`) or declared in `tds.toml`. Installed via the language's own package manager (a gem; an npm package). The core runs whichever it finds; absent a native provider, a language degrades to the fallback.
- **v1 native providers:** **Ruby/Rails** (gem: prism + rubocop/brakeman/sorbet/simplecov/flog) and **JS/React** (npm: TS compiler + react-docgen + eslint/tsc/coverage/complexity). A Rails+React repo already has Ruby + Node, so both providers install cleanly with no extra runtimes.

### 9.4 Built-in analyzers (v1)

Off-the-shelf tools each provider wraps, invoked with sensible defaults and overridable via `tds.toml`:

| Category | Ruby/Rails provider | JS/React provider | Default view(s) |
|---|---|---|---|
| **Linters** | rubocop | eslint (+ eslint-plugin-react) | annotations + panel |
| **Security** | brakeman | (eslint security rules) | annotations + panel |
| **Types/analysis** | sorbet | tsc | annotations + panel |
| **Coverage** | simplecov | istanbul/lcov | heatmap + badge |
| **Complexity** | flog | eslint complexity | heatmap + badge |

Each analyzer is independent; a repo lights up whatever tools it actually has. The table is the v1 built-in set, not a limit — the provider protocol is the supported way to add languages and tools.

### 9.5 Views (data model)

A **view** is `{ id, title, kind, provenance, findings }` where `kind ∈ {annotations, heatmap, panel, badge}`. Views are computed at `analyze` time, embedded at `build` time (§6.4 step 5), and rendered by the viewer (§8). Because findings are SHA-pinned and version-attributed, a view in a shared bundle is reproducible and self-describing — consistent with the immutable-artifact model (§5).

---

## 10. Technology choices

- **Core:** Go — single **CGO-free** static binary, cross-compiles trivially (validated in spike TDS-4), embeds the viewer assets (`go:embed`); best distribution story for a shareable CLI. Owns dispatch, store, draft/tmux, build, bundle, tour format, anchor resolution.
- **Providers:** separate programs behind the JSON protocol (§9). v1 native providers — **Ruby** (gem: prism + rubocop/brakeman/sorbet/simplecov/flog) and **JS/React** (npm: TS compiler + react-docgen + eslint/tsc/coverage). Discovered on `PATH` / via `tds.toml`.
- **Fallback parsing:** tree-sitter (Go bindings, **CGO**) lives in the **separate fallback-provider binary**, built per-OS — kept out of the core so the core stays CGO-free (spike TDS-4).
- **Store:** SQLite via the pure-Go `modernc.org/sqlite` driver (keeps the core CGO-free), with JSON export. Holds symbols, imports, git signals, and analyzer findings.
- **AI transport:** `tmux` driving `claude --dangerously-skip-permissions`, file-mediated I/O.
- **Viewer:** vanilla JS + minimal CSS, no framework, no CDN — everything inlined/self-contained so bundles are portable and CSP-safe.
- **Build-time highlighting:** a Go highlighter (e.g. chroma) compiled into the core, independent of the providers.

---

## 11. Key decisions (with rationale)

| # | Decision | Rationale | Alternatives rejected |
|---|---|---|---|
| 1 | v1 = onboarding | Shapes defaults; hardest and most valuable | PR/demo/interview → later modes |
| 2 | Draft-then-edit authoring | AI speed + human judgment; curation ~20 min | Fully auto (quality caps); hand-only (slow) |
| 3 | Symbol-first anchors + line fallback | Survives churn; only rename/delete breaks | Line-only (rots fast) |
| 4 | Embed whole repo @ pinned SHA | Immutable, self-true bundles; free-browse | Windows-only (on-rails only) |
| 5 | Static bundle output | Shareable as artifact; no server/network | Serve-only (needs hosting) |
| 6 | Markdown source → JSON manifest | Diffable, PR-reviewable, *is* content | Tool-owned JSON (bad to hand-edit) |
| 7 | Linear spine + collapsible side-quests | Fits real onboarding without a maze | Strict linear (too flat); graph (confusing) |
| 8 | Scrollytelling + keyboard stepping | Reader-first, presenter-capable | Scroll-only / step-only |
| 9 | tmux + `--dangerously-skip-permissions`, file-mediated | Stays on subscription; observable; robust | `claude -p` (billing ambiguity, no observability) |
| 10 | Two-pass draft + hard anchor validation | Anti-hallucination; guaranteed-real anchors | Single-pass (more drift) |
| 11 | SQLite + JSON export | Queryable; substrate for future `serve` | JSON-only (weaker at scale) |
| 12 | Opinionated template, first-class | Reliable structure; home for future modes | Free-form (variable quality) |
| 13 | v1 languages: Ruby/Rails + JS/React | Rails conventions + React are the first targets; both runtimes already present together | Broader set (thin support); Python (adds a 3rd runtime) |
| 14 | Build-time syntax highlighting | No runtime JS, JS-off readable, cheap | Runtime highlighter (heavier, slower) |
| 15 | Separate `tds analyze` stage | Keeps `map` fast/pure; analysis opt-in, cached, isolated | Fold into map (slow/impure); on-demand in build (coupled) |
| 16 | Wrap existing tools, not bespoke analysis | Reuse rubocop/eslint/tsc/etc.; provider protocol for more | Build our own analyzers (huge, worse) |
| 17 | Full view system in v1 (annotations/heatmap/panel/badge) | Instrumented views are a core differentiator | Ship subset (weaker first release) |
| 18 | Analyzers availability-gated + failure-isolated | A missing/broken tool degrades, never blocks | Hard-require tools (fragile across repos) |
| 19 | Go core, single binary + Hugo | One artifact to install; cross-compiles; Hugo is the only external tool | Python/Node core (adds a runtime to target repos) |
| 19a | Multi-page Hugo site is the **only** output; no single-file bundle (TDS-62) | The site is what gets shared for real repos; the bundle was a second UI sharing no design or code with it, and maintaining both meant paying the design cost twice | Keep the emailable single-file bundle (two divergent UIs over one dataset) |
| 19b | Hugo extended ≥ 0.128 is a hard build dependency | Theme stays authored as Hugo templates, so the design iterates with `hugo server` | Port templates to Go `html/template` (re-implements the design system, loses live iteration) |
| 20 | Out-of-process native providers | Best-in-class native analysis (prism, TS compiler); crash isolation | Tree-sitter-only in core (shallower); fork per language (unmaintainable) |
| 21 | Versioned JSON provider protocol | Language-neutral core; third-party providers; independent release cadence | In-core language plugins (couples core to every language) |

---

## 12. Risks and open questions

- **tmux orchestration robustness** — *Core mechanism validated by spike TDS-2* (10/10 mock runs + a real-Claude round trip): wait for a readiness marker before sending; detect completion via a filesystem `.done` marker, not the pane. Remaining hardening (TDS-38): timeouts/retries, dead-pane and stuck-model recovery, session reuse to amortize startup, cleanup on failure.
- **Symbol-path normalization across languages** — one anchor grammar that reads naturally in Ruby and JS/TS, mapped inside each provider. *Spike during the first provider.*
- **Provider protocol design** — getting the `capabilities`/`structure`/`analyze` contract, version negotiation, and error/partial-result semantics right up front; it's the seam everything else hangs on. *Prototype the Ruby provider against a real Rails app early.*
- **Provider distribution & runtimes** — native providers need Ruby / Node present; the Go core spawns them as subprocesses. Fine for Rails+React repos (both already installed), but the install story (gem + npm package + binary) needs to be smooth, with clear messaging when a provider is missing (falls back to tree-sitter).
- **Cross-language build** — *Resolved by spike TDS-4.* tree-sitter requires CGO, so it is split into a separate fallback-provider binary (built per-OS); the core uses pure-Go `modernc.org/sqlite` and stays CGO-free and cross-compilable. Remaining work is packaging the per-OS fallback-provider binaries alongside the core.
- **Bundle size on large repos** — whole-repo embed + lazy-load should hold, but needs a real measurement on a big repo; may need blob compression / on-demand fetch tuning.
- **Draft quality variance** — how good is the two-pass draft *actually*? The template mitigates but doesn't guarantee. *Dogfood on a real Rails repo first.*
- **Focus-range highlighting inside a symbol** — substring vs. sub-line-range resolution rules TBD.
- **Multiple tours per repo** — naming, discovery, an index page. Likely fine but unspecified in v1.
- **Analyzer noise & config** — type checkers and linters can be loud or need repo-specific config (rubocop/eslint config discovery, tsc strictness, sorbet setup). Need sane defaults, per-repo `tds.toml` overrides, and severity filtering so views inform rather than overwhelm.
- **Analyzer performance** — full-repo runs of several tools can be slow on large repos. Mitigated by per-file caching and parallel/opt-in runs, but needs measurement.
- **View volume in the bundle** — many findings across many tools could bloat the bundle; may need per-view thresholds or summarization.
- **Tool version drift** — a view is only reproducible if the tool version is recorded (it is) and ideally re-runnable; different tool versions yield different findings. Provenance is shown; exact re-run is not guaranteed.

---

## 13. v1 scope line

**In:** Go core binary (`map`, `analyze`, `draft`, `build`, `check`); provider system (JSON protocol + tree-sitter fallback compiled in); v1 native providers — **Ruby/Rails** (prism + rubocop/brakeman/sorbet/simplecov/flog) and **JS/React** (TS compiler/react-docgen + eslint/tsc/coverage/complexity); full view system (annotations/heatmap/panel/badge); Markdown tour format + JSON manifest; onboarding template; directory bundle (+ optional single-file); scrollytelling+keyboard viewer with free-browse; SHA-pinned whole-repo embed; SQLite+JSON store.

**Out (later):** `serve` / Datasette mode; `review` / `demo` / interview templates; in-browser editor; live/auto-updating tours; providers beyond Ruby + JS/React (Python, Go, etc.); third-party providers; deep semantic analysis (call graphs, dataflow).

---

*Next: `docs/implementation-plan.md` — phased build plan, then filed into beads with dependencies.*
