---
title: "How the tds Ruby provider works"
template: onboarding
audience: "contributors adding a language provider"
---

The Ruby provider is a small, resident program the `tds` core launches and drives
over a line-delimited JSON protocol. This tour follows one request through it —
from the stdio loop to prism-based symbol extraction — so you can see the shape a
new language provider needs to take.

# Chapter: The protocol loop

::stop{anchor="providers/ruby/lib/tds/provider.rb::TDS::Provider#run"}
The heart of the provider: read **one JSON request per line** from stdin, answer
with one JSON object on stdout, until stdin closes. `stderr` is deliberately left
for logs — only stdout carries protocol traffic. A malformed line becomes an
`invalid_params` error rather than crashing the process.
::

::stop{anchor="providers/ruby/lib/tds/provider.rb::TDS::Provider#handle"}
Each request is dispatched by its `op`. Today that's `capabilities` and
`structure`; `analyze` will join them later. Any other op returns a structured
`unsupported_op` error, and an unexpected exception is caught and returned as an
`internal` error — a provider should never take the core down with it.
::

::stop{anchor="providers/ruby/lib/tds/provider.rb::TDS::Provider#capabilities"}
The handshake payload. The core reads `protocol` to decide compatibility, and
`languages` / `operations` to know what this provider can do. This is where a new
provider announces itself.
::

# Chapter: Extracting structure

::stop{anchor="providers/ruby/lib/tds/structure.rb::TDS::Structure#run"}
The `structure` operation. For each requested file it parses the source with
**prism** (Ruby's official parser) and accumulates symbols, `require` edges, and
Rails entrypoints. A file that can't be read or parsed is reported in
`file_errors` — the batch keeps going.
::

::stop{anchor="providers/ruby/lib/tds/structure.rb::TDS::SymbolCollector#visit_class_node"}
Symbols get their **qualified names** here. The collector is a prism visitor that
pushes each class/module onto a scope stack on the way in and pops it on the way
out, so a method deep inside `Billing::Invoice` comes out as
`Billing::Invoice#finalize`. The `#` (instance) vs `.` (singleton) distinction is
made in `visit_def_node`.

::detour{title="How Rails entrypoints are detected"}
Entrypoint classification is a separate, heuristic pass over the collected
classes.

::stop{anchor="providers/ruby/lib/tds/structure.rb::TDS::EntrypointDetector.kind_for"}
Superclass first — `ApplicationRecord` → `rails-model`, `ApplicationController` →
`rails-controller`, `ApplicationJob` → `rails-job` — then a fall back to path
convention (`app/models/…`, `app/controllers/…Controller`). It's intentionally
approximate: good enough to surface the landmarks of a Rails app.
::
::
::
