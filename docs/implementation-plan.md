# tour-de-source — Implementation Plan

**Status:** Draft for review
**Companion to:** `docs/design.md`
**Date:** 2026-07-17

---

## 1. How to read this

Work is organized into **milestones** (→ beads epics) made of **tasks** (→ beads issues) with explicit **dependency edges** (→ beads `blocks`/`depends-on`). Each task has a stable ID (`TDS-N`), a type, a rough size, its dependencies, and a "done when" acceptance line so it can be filed and picked up independently.

- **Type:** `spike` (de-risk, throwaway-ok), `feat` (product code), `chore` (infra/docs).
- **Size:** `S` ≈ ≤1 day · `M` ≈ 2–4 days · `L` ≈ ~1 week+.
- **Deps:** hard prerequisites. Anything not listed can proceed in parallel.

## 2. Approach & sequencing principles

1. **De-risk first (M0).** The two things most likely to sink the project — tmux↔Claude orchestration and the provider protocol — get proof-of-concept spikes before anything is built on them. The static-build question (CGO-free SQLite + tree-sitter in one Go binary) is validated up front too.
2. **Vertical slice before breadth.** Take **Ruby/Rails all the way through** (map → build → viewer → analyze → draft) to prove the whole pipeline end-to-end, *then* add the **JS/React** provider (M5) to prove the protocol generalizes. Second language should be mostly "implement the contract," not "discover the design."
3. **Earliest visible artifact.** By the end of M2 a **hand-authored** tour renders in the viewer end-to-end — no AI required. This is the first demo-able moment and it exercises core, store, build, and viewer together.
4. **AI is late, not early.** `draft` (M4) sits on top of a system that already works deterministically. This keeps the hard-to-test AI stage off the critical path of "does the pipeline work."
5. **Parallel tracks.** After M1, the **viewer track** (HTML/JS) and the **provider/analysis track** (Go core + Ruby/Node) can proceed concurrently — they meet at `tds build`.

## 3. Milestone overview

| # | Milestone | Outcome | Key deps |
|---|---|---|---|
| **M0** | Foundations & spikes | Scaffold + 3 spikes retire the top risks | — |
| **M1** | Structure map (Ruby) | `tds map` populates the store from a real Rails app | M0 |
| **M2** | Format, build, bundle, core viewer | A hand-authored tour renders end-to-end in a browser | M1 |
| **M3** | Analyze + view system | `tds analyze` findings render as annotations/heatmap/panel/badge | M1, M2 |
| **M4** | AI drafting | `tds draft` produces a curated-ready `*.tour.md` | M1, M3 |
| **M5** | JS/React provider | Rails+React repos fully supported; protocol proven | M1–M3 |
| **M6** | Dogfood, distribution, release | Shippable single binary + providers, dogfooded | M4, M5 |

---

## 4. Milestones & tasks

### M0 — Foundations & de-risking spikes

| ID | Task — done when | Type | Size | Deps |
|---|---|---|---|---|
| TDS-1 | **Repo scaffolding** — Go module, CLI skeleton (cobra), lint/test/CI, Makefile; `tds --help` runs and CI is green. | chore | S | — |
| TDS-2 | **Spike: tmux↔Claude orchestration** — drive `claude --dangerously-skip-permissions` in a tmux pane, file-mediated prompt/response, sentinel completion detection; a prompt reliably returns a JSON file 10/10 runs with a documented readiness protocol. | spike | M | TDS-1 |
| TDS-3 | **Spike: provider protocol + Ruby hello-world** — draft the JSON protocol; a minimal Ruby provider answers `capabilities` and a stub `structure`; the Go core spawns it and gets one real symbol back from an actual Rails file. | spike | M | TDS-1 |
| TDS-4 | **Spike: static build** — CGO-free SQLite (`modernc.org/sqlite`) + tree-sitter Go bindings compile into one static binary; CI cross-compiles macOS+Linux, opens a DB, and parses a file. | spike | S | TDS-1 |

### M1 — Structure map (Ruby) + store + anchors

| ID | Task — done when | Type | Size | Deps |
|---|---|---|---|---|
| TDS-5 | **Provider protocol v1** — finalize `capabilities`/`structure`/`analyze` request/response JSON schemas, version negotiation, error/partial-result semantics, transport (stdio + file-mediated for large payloads); Go types + conformance fixtures exist. | feat | M | TDS-3 |
| TDS-6 | **Core: provider host** — discovery (PATH + `tds.toml`), subprocess spawn, capabilities negotiation, timeouts, failure isolation; core lists discovered providers and falls back gracefully when one is absent. | feat | M | TDS-5, TDS-4 |
| TDS-7 | **Store: schema + JSON export** — SQLite schema (files, symbols, imports, entrypoints, git signals, findings) + migrations + `map.json` export; sample data round-trips. | feat | M | TDS-4 |
| TDS-8 | **Core: file walk + language detection** — walk respecting `.gitignore`; classify files by language; correct tags on a Rails+React repo. | feat | S | TDS-1 |
| TDS-9 | **Core: git signals** — churn, age, last-touched, primary authors via git; values match `git log` spot checks. | feat | S | TDS-1 |
| TDS-10 | **Ruby provider: `structure`** — prism-based symbols (classes/modules/methods) with qualified paths, ranges, normalized-body hash; imports; Rails entrypoints (routes/controllers/models/jobs); verified on a real Rails app. | feat | L | TDS-5 |
| TDS-11 | **Fallback provider: `structure`** — *separate* tree-sitter fallback-provider binary (CGO; kept out of the CGO-free core per spike TDS-4) yields symbols where a grammar exists, degrades to line-range anchors otherwise, never errors. | feat | M | TDS-6 |
| TDS-12 | **Anchor resolution + Ruby normalization** — resolve `path::Symbol` → concrete range against the store; symbol-path normalization; line-range fallback when unresolved (flagged). | feat | M | TDS-7, TDS-10 |
| TDS-13 | **`tds map` command** — wire walk + git + providers → store + JSON; on a Rails app produces a populated `map.sqlite` + `map.json`. | feat | M | TDS-6, TDS-7, TDS-8, TDS-9, TDS-10, TDS-11 |

### M2 — Tour format, build, bundle + core viewer

| ID | Task — done when | Type | Size | Deps |
|---|---|---|---|---|
| TDS-14 | **Tour markdown parser** — frontmatter + `::stop`/`::detour` directives → tour AST with good validation errors; parses the design-doc example. | feat | M | TDS-1 |
| TDS-15 | **Manifest schema + compiler** — tour AST + resolved anchors → JSON manifest (chapters/stops/anchors/detours); fixture tour compiles with anchors resolved to ranges. | feat | M | TDS-14, TDS-12 |
| TDS-16 | **Build-time highlighting** — chroma renders pre-tokenized HTML per file/range for Ruby+JS; no runtime highlighter needed. | feat | M | TDS-1 |
| TDS-17 | **Repo embed + commit pinning** — resolve `commit: auto`→SHA, read that snapshot, lay out lazy-loadable per-file blobs + index; manifest references the SHA. | feat | M | TDS-7 |
| TDS-18 | **Viewer: two-pane skeleton** — narrative rail + code pane, loads a manifest, embedded via `go:embed`; renders chapters/stops with code. | feat | M | TDS-15 |
| TDS-19 | **Viewer: scrollytelling + keyboard** — scroll drives the code pane to the active stop; `←/→` step stop-to-stop. | feat | M | TDS-18 |
| TDS-20 | **Viewer: free-browse ⟷ on-rails** — file-tree browser over embedded blobs; open any file; "return to tour" snaps back. | feat | M | TDS-18, TDS-17 |
| TDS-21 | **Viewer: deep links, outline, JS-off** — shareable stop/view URL fragments; chapter/stop outline nav; readable with JS disabled. | feat | S | TDS-18 |
| TDS-22 | **`tds build` command** — directory bundle (`index.html` + assets + blobs + manifest) that opens self-contained in a browser. | feat | L | TDS-15, TDS-16, TDS-17, TDS-18 |
| TDS-23 | **Single-file bundle option** — `--single-file` emits one offline HTML (practical for small repos). | feat | S | TDS-22 |
| TDS-24 | **`tds check` command** — re-resolve anchors at HEAD; classify clean/moved/broken; report + non-zero exit on broken. | feat | M | TDS-12 |

### M3 — Analyze + the view system

| ID | Task — done when | Type | Size | Deps |
|---|---|---|---|---|
| TDS-25 | **Core: analyze pipeline** — call providers' `analyze`, resolve findings to symbols via the map, write to store; findings carry provenance (tool+version+commit). | feat | M | TDS-5, TDS-13 |
| TDS-26 | **Findings cache** — key by (tool, version, file hash); re-runs only re-analyze changed files. | feat | S | TDS-25 |
| TDS-27 | **Config: `tds.toml`** — enable/disable/configure analyzers and providers per repo; sensible defaults when absent. | feat | S | TDS-25 |
| TDS-28 | **Ruby analyzers: lint + security** — provider runs rubocop + brakeman, returns normalized findings mapped to the right view kinds. | feat | M | TDS-10 |
| TDS-29 | **Ruby analyzers: types + coverage + complexity** — provider runs sorbet + simplecov + flog → findings (annotations/heatmap/badge). | feat | M | TDS-10 |
| TDS-30 | **`tds analyze` command** — end-to-end analyze over a Rails app populates findings in the store. | feat | M | TDS-25, TDS-26, TDS-27, TDS-28, TDS-29 |
| TDS-31 | **View data model + manifest embedding** — `{id,title,kind,provenance,findings}`; build embeds selected views; default = all views with findings. | feat | M | TDS-15, TDS-25 |
| TDS-32 | **Viewer: annotations view** — inline/gutter markers with rule/message/link popovers. | feat | M | TDS-18, TDS-31 |
| TDS-33 | **Viewer: heatmap view** — line/file shading for coverage/complexity. | feat | M | TDS-18, TDS-31 |
| TDS-34 | **Viewer: panel view** — filterable findings table (file/severity/rule) with jump-to-code. | feat | M | TDS-18, TDS-31 |
| TDS-35 | **Viewer: badge view** — per-symbol summary chips on stops. | feat | S | TDS-18, TDS-31 |
| TDS-36 | **Viewer: view switcher** — toggle multiple views, show provenance, honor stop `view="…"` deep links. | feat | S | TDS-32, TDS-33, TDS-34, TDS-35 |
| TDS-37 | **`tds build`: embed views** — build wires selected views/findings into the bundle. | feat | S | TDS-22, TDS-31 |

### M4 — AI drafting

| ID | Task — done when | Type | Size | Deps |
|---|---|---|---|---|
| TDS-38 | **tmux orchestration (hardened)** — productionize the M0 spike: session reuse/cleanup, timeouts, retries, error surfacing, nonce sentinels. | feat | M | TDS-2 |
| TDS-39 | **Draft context assembly** — build grounding from map + findings + README (entrypoints, landmarks, hotspots) as the prompt payload. | feat | M | TDS-13, TDS-30 |
| TDS-40 | **Onboarding template** — the opinionated skeleton (30-sec / one-operation / landmarks / conventions / side-quests) as a fillable structure. | feat | S | TDS-1 |
| TDS-41 | **Draft pass 1: outline** — Claude returns a chapter/stop skeleton with anchors drawn only from real symbol IDs in the map. | feat | M | TDS-38, TDS-39, TDS-40 |
| TDS-42 | **Draft pass 2: narrate** — per-stop prose from the anchored symbol + neighbors; parallelized. | feat | M | TDS-41 |
| TDS-43 | **Anchor validation gate** — resolve every proposed anchor before writing; drop/flag non-resolving ones. | feat | S | TDS-42, TDS-12 |
| TDS-44 | **`tds draft` command** — emits a curated-ready `*.tour.md` for a real Rails app. | feat | M | TDS-41, TDS-42, TDS-43 |

### M5 — JS/React provider

| ID | Task — done when | Type | Size | Deps |
|---|---|---|---|---|
| TDS-45 | **JS provider scaffold** — npm package that speaks the protocol (`capabilities` + conformance fixtures pass). | feat | M | TDS-5 |
| TDS-46 | **JS provider: `structure`** — TS compiler + react-docgen → symbols/imports/entrypoints/components on a real React app. | feat | L | TDS-45 |
| TDS-47 | **JS symbol-path normalization** — resolve JS/TS anchors (incl. components) against the store. | feat | M | TDS-46, TDS-12 |
| TDS-48 | **JS analyzers: lint + types** — eslint (+ eslint-plugin-react, security rules) + tsc → findings. | feat | M | TDS-45 |
| TDS-49 | **JS analyzers: coverage + complexity** — istanbul/lcov + eslint complexity → heatmap/badge findings. | feat | M | TDS-45 |
| TDS-50 | **Multi-language repo handling** — one Rails+React repo flows through map/analyze/build with both providers active. | feat | M | TDS-13, TDS-30, TDS-46, TDS-48 |
| TDS-51 | **JS provider distribution** — npm publish + discovery UX; core finds it on PATH/`tds.toml`. | chore | S | TDS-45 |

### M6 — Dogfood, distribution, release

| ID | Task — done when | Type | Size | Deps |
|---|---|---|---|---|
| TDS-52 | **Dogfood end-to-end** — draft→curate→build a tour of a real Rails+React app; file and fix the issues found. | chore | M | TDS-44, TDS-50, TDS-36, TDS-37 |
| TDS-53 | **Bundle size tuning** — measure on a large repo; add blob compression / per-view thresholds as needed. | feat | M | TDS-22, TDS-37 |
| TDS-54 | **Provider install UX** — smooth gem/npm install; clear "provider missing → falling back to tree-sitter" messaging. | feat | S | TDS-6, TDS-51 |
| TDS-55 | **Docs: tour authoring guide** — format reference + worked example. | chore | S | TDS-14 |
| TDS-56 | **Docs: provider-authoring guide** — the protocol, so third parties can add languages. | chore | S | TDS-5 |
| TDS-57 | **Release** — cross-compiled binaries, versioning, embedded viewer, install script. | chore | M | TDS-4, TDS-22 |

---

## 5. Dependency notes & critical path

**Longest chain (critical path):**
`TDS-1 → TDS-3 → TDS-5 → TDS-10 → TDS-12 → TDS-15 → TDS-18 → TDS-22 → TDS-37 → TDS-52`
i.e. scaffold → protocol → Ruby structure → anchors → manifest → viewer → build → views-in-bundle → dogfood. Schedule risk concentrates on **TDS-10** (Ruby structure, `L`) and **TDS-22** (`tds build`, `L`); protect their slack.

**Parallel tracks after M1:**
- **Viewer track** (TDS-18→19/20/21, then 32–36) is pure HTML/JS and needs only a fixture manifest — can run alongside the provider work.
- **Analysis track** (TDS-25–30, Ruby analyzers) is independent of the viewer until they meet at TDS-31/37.
- **Draft track** (TDS-38 hardening) can start as soon as the M0 spike (TDS-2) lands — it only needs real *content* (TDS-39) once map+analyze exist.

**Second-language proof (M5)** deliberately depends on the finished protocol (TDS-5) and the vertical slice, so it validates generality rather than co-designing it.

## 6. Filing into beads

- Each **milestone → an epic**; each **task → an issue** under it, carrying the `TDS-N` as a label/alias for cross-reference.
- Each **Deps** entry → a `depends-on` (equivalently the prerequisite `blocks` this task) edge.
- Suggested labels: `track:core`, `track:viewer`, `track:ruby-provider`, `track:js-provider`, `track:draft`; plus `type:spike|feat|chore` and `size:S|M|L`.
- **Ready queue** at kickoff (no unmet deps): TDS-1 first; then TDS-2, TDS-3, TDS-4 unblock immediately after.

## 7. Definition of done for v1

- `tds map`, `tds analyze`, `tds draft`, `tds build`, `tds check` all work on a real **Rails + React** repo.
- Both native providers (Ruby, JS/React) ship and are discovered; **tree-sitter fallback** covers other languages.
- Full **view system** (annotations/heatmap/panel/badge) renders in the viewer.
- A tour is a **SHA-pinned, self-contained static site** with guided narration and free-browse.
- Drafting runs on the author's **Claude subscription** via tmux, and the emitted draft has **only validated anchors**.
- Core ships as a **single Go binary** plus a **Hugo extended ≥ 0.128** dependency for `tds build`.

---

*Next: file these tasks and dependency edges into beads.*
