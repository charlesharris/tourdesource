# Lenses

A **lens** is what tds knows about a kind of project. It is declarative data —
not code — that says how to read a repository's shape: which directories play
which architectural role, what is not architecture at all, and (in time) which
analyzers apply and which entrypoint conventions hold.

Lenses are how tds stops being a Rails tool. Recognising Go, Node, Python or
Terraform becomes a data file rather than another branch in a Go function.

## The central rule

> **Lenses interpret paths. Providers interpret code.**

A lens is pure data and needs no runtime: you get a Rails architecture map on a
machine with no Ruby installed. A [provider](protocol.md) is a program that
parses source and needs its language's runtime.

The rule tells you where a new piece of knowledge goes. "`app/controllers` is
the entry layer" is a path fact — a lens. "This class inherits from
`ApplicationController`" is a code fact — a provider.

It also names a duplication that exists today: the Ruby provider's
`EntrypointDetector` classifies Rails entrypoints by superclass *and* by
path/name convention. The first half is provider work. The second half is lens
work living in the wrong place, and moves when lenses drive entrypoints (step 6
below).

## Anatomy

```toml
[lens.rails]
extends  = "ruby"                                 # inherit and override
priority = 20                                     # framework beats language
detect   = { all = ["Gemfile", "config/routes.rb"] }
ignore   = ["test/", "spec/", "db/schema.rb"]

[[lens.rails.role]]
path   = "app/controllers"
column = "entry"
name   = "Controllers"

[[lens.rails.role]]
path        = "app/services"
column      = "feature"
per-segment = true        # one node per child dir, not one node for the parent
```

| Field | Meaning |
|---|---|
| `extends` | Inherit another lens's rules, then override |
| `priority` | Tie-break when two lenses share a root; higher wins |
| `detect` | `all`/`any` of marker paths or globs, relative to the lens root |
| `ignore` | Paths that describe the system rather than compose it |
| `role` | A directory's architectural role and display name |

Every section is optional and additive, so `entrypoint`, `analyzers` and `views`
can arrive later without breaking the schema.

`per-segment` exists because `internal/` in a Go repo holds fifteen packages. A
plain role rule would collapse them into one node called "internal"; per-segment
yields `internal/site`, `internal/cli` and so on.

## Detection and scoping

Lenses are **scoped**, not global. tds walks the repository for markers; each
match produces a lens *instance* rooted at the directory where it matched.

To resolve a file, take every instance whose root is a prefix of the path and
pick:

1. the **longest root** — the most specific scope wins;
2. then the highest `priority`;
3. then lexically by lens name, so the result is deterministic.

This falls out correctly for the cases that matter:

- **tourdesource** — `go @ /` and `ruby @ providers/ruby/`. A file under
  `providers/ruby/lib/` resolves to the Ruby lens because its root is longer,
  even though the Go lens also covers it.
- **Redmine** — `ruby @ /` and `rails @ /` share a root, so `priority` decides
  and Rails wins.
- **A monorepo** — each app's marker roots its own instance. No special case.

Detection runs once, at `tds map` time, and the resolved instances are stored in
the map. The map is then self-describing, and `analyze` and `build` cannot
disagree with each other about what kind of project they are looking at.

### Naming across instances

Two Rails apps in one repository both produce a subsystem called "Controllers".
When more than one instance is active, subsystem names are prefixed with the
lens root; a single-instance repository — the common case — stays unprefixed.

## The generic lens

When nothing matches, the **generic** lens applies: group by directory,
descending one level into container directories (`internal`, `pkg`, `src`,
`cmd`, `apps`, `packages`…). It claims only what is near-universal — `cmd/`,
`bin/` and a root main file are entry points — and puts everything else in an
unlabelled "Modules" column rather than guessing at roles.

This is deliberate. Sorting `internal/store` under "Infrastructure" would be a
guess wearing the costume of an analysis. The architecture page says which
derivation it used and, in the generic case, states plainly that it is showing
structure rather than architecture.

The generic lens shipped ahead of the rest of this design (TDS-67). Every other
lens degrades to it when its markers are absent.

## Where lenses come from

Three sources, merged in increasing order of precedence:

1. **Built in** — embedded TOML in the core. The common ecosystems live here so
   they work with no runtime and no install.
2. **Provider-contributed** — a provider returns lens definitions from
   `capabilities`. This is for framework knowledge that tracks a provider's own
   release cadence: the Ruby provider can ship a Sidekiq lens without a core
   release, and a third-party provider can teach tds a language the core has
   never heard of.
3. **User** — `tds.toml` in the repository being toured. Amend a detected lens,
   force one on, or turn one off. Local truth beats a general rule.

## Lens is not view

A **lens** is how tds reads a project's structure. A **view**
(annotations/heatmap/panel/badge, design §7) is a layer of findings rendered
over a tour. They are different axes and will appear near each other in the
code; the words are not interchangeable.

## Plan

| Step | Work | Test that it worked |
|---|---|---|
| 1 | `internal/lens`: schema, embedded TOML, detection, scoping | Unit tests over synthetic trees |
| 2 | Migrate `dirRoles` → `rails.toml`; generic path becomes the `generic` lens | Redmine's output is unchanged |
| 3 | Go, Node/TypeScript, Python lenses | Non-Rails repos stop falling back to structure-only |
| 4 | `tds.toml` overrides (TDS-27) | A repo can correct tds about itself |
| 5 | Provider-contributed lenses over the protocol | Sidekiq lens ships from the Ruby provider |
| 6 | Lenses drive entrypoints, analyzers and views | The provider/lens boundary is settled |

Steps 1 and 2 change no output — that is what makes them safe, and Redmine's
build being byte-identical afterwards is the acceptance test. Step 3 is where
this starts paying off.
