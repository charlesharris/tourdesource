# tour-de-source (`tds`)

**Analyze a repository and produce a shareable, interactive _tour_ of the codebase** —
for onboarding, demos, code review, and interviews. A tour is guided narration
anchored to real code, compiled into a self-contained static site you can open in
a browser or email to someone. It's shareable both as **content** (diffable
Markdown) and as an **experience** (the viewer below).

![The tds viewer showing a tour of this repo](docs/images/tour-light.png)

> The screenshots on this page are a **real tour of this repository's Ruby
> provider**, produced by `tds map` + `tds build`. The tour source is
> [`docs/tours/ruby-provider.tour.md`](docs/tours/ruby-provider.tour.md).

## How it works

A tour walks the reader through code in a deliberate order. The left rail is the
narration — chapters and stops; the right pane shows the actual source, and each
stop drives the code pane to its **anchor** (a symbol like
`app/models/invoice.rb::Invoice#finalize`, resolved to a concrete line range).
Stops can carry collapsible **side-quests** for role-specific detours.

| Side-quests (detours) | Dark theme |
|---|---|
| ![A detour expanded](docs/images/tour-detour.png) | ![Dark theme](docs/images/tour-dark.png) |

The bundle is **pinned to a commit** and fully self-contained — no server, no
network, no external assets — so a shared tour is always internally consistent
and works offline.

## Quickstart

```sh
# 1. Build the structural map of a repository (symbols, imports, git signals)
tds map .

# 2. Author a tour (soon: `tds draft` generates a curated-ready draft for you)
$EDITOR onboarding.tour.md

# 3. Compile to a self-contained bundle and open it
tds build onboarding.tour.md
open .tds/tour/index.html
```

A tour source file is Markdown with light directives:

```markdown
---
title: "A tour of the billing service"
audience: "new backend engineers"
---

# Chapter: Follow one invoice end to end

::stop{anchor="app/models/invoice.rb::Invoice#finalize" focus="def finalize"}
`finalize` is the whole domain in a few lines — get an invoice from draft to
finalized without double-charging.

::detour{title="If you're debugging a stuck invoice"}
Stuck invoices are almost always the lock below.
::stop{anchor="app/models/invoice.rb::Invoice#with_lock"}
...
::
::
::
```

Anchors are **symbol-first** with a `path:line-start-end` fallback; they resolve
against the map, so ordinary edits (code shifting around) don't break them —
only genuine renames/deletes do, and `tds check` (coming) reports that drift.

## Pipeline

`tds` is a small Go binary that orchestrates discrete, inspectable stages.
Deep language analysis lives in out-of-process **providers** behind a
[versioned JSON protocol](docs/protocol.md), so the core stays language-neutral
and ships as one static binary.

| Stage | What it does | Status |
|---|---|---|
| `tds map` | Structural index: symbols, imports, Rails entrypoints, git signals → SQLite + JSON | ✅ working |
| `tds analyze` | Run language tooling (linters, types, coverage) into normalized findings | 🚧 planned (M3) |
| `tds draft` | AI-assisted tour draft to curate (drives Claude over your subscription) | 🚧 planned (M4) |
| `tds build` | Compile a tour into a self-contained static bundle | ✅ working |
| `tds check` | Re-resolve anchors against HEAD and report drift | 🚧 planned |

**Languages:** Ruby/Rails today (via a prism-based provider); JavaScript/React
next. A tree-sitter fallback covers other languages with line-range anchors.

## Status

Early, but the core loop works end to end: **`tds map` → author a `.tour.md` →
`tds build` → open a real, shareable tour.** The Markdown is hand-written for now;
`tds draft` (milestone M4) will generate it for you. See
[`docs/design.md`](docs/design.md) for the full design and
[`docs/implementation-plan.md`](docs/implementation-plan.md) for the roadmap.

## Build from source

Requires **Go 1.26+**. The Ruby provider requires **Ruby 3.4+** (prism ships as a
default gem).

```sh
make build        # -> ./bin/tds
make check        # lint + tests
```

The Ruby provider isn't published globally yet, so point `tds` at the in-repo
build when mapping Ruby code:

```sh
export TDS_PROVIDER_RUBY="$PWD/providers/ruby/exe/tds-provider-ruby"
./bin/tds map .
./bin/tds build docs/tours/ruby-provider.tour.md
```

## Documentation

- [`docs/design.md`](docs/design.md) — architecture and design decisions
- [`docs/implementation-plan.md`](docs/implementation-plan.md) — milestones and tasks
- [`docs/protocol.md`](docs/protocol.md) — the provider protocol (v1)
- [`docs/tours/ruby-provider.tour.md`](docs/tours/ruby-provider.tour.md) — an example tour
