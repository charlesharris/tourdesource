# tds-provider-ruby

The Ruby/Rails **provider** for [tour-de-source](../../) (`tds`). A separate
program the `tds` core launches and drives over the [provider protocol
v1](../../docs/protocol.md) (JSONL over stdio) — kept out of the CGO-free core
per spike TDS-4.

## What it does

- **`capabilities`** — advertises protocol version, languages (`ruby`), and
  (later) analyzers.
- **`structure`** — extracts, via [prism](https://github.com/ruby/prism):
  - **symbols** — classes/modules/methods with normalized qualified paths
    (`Invoice#finalize`, `Invoice.overdue`, `Billing::Invoice`), line ranges, and
    a `body_hash` for drift detection;
  - **imports** — `require` / `require_relative` edges;
  - **entrypoints** — Rails routes/controllers/models/jobs (by superclass, then
    path/name convention).

`analyze` (rubocop, brakeman, sorbet, simplecov, flog) arrives in TDS-28/29.

## Run

```sh
exe/tds-provider-ruby        # reads JSONL requests on stdin, writes responses on stdout
```

Requires Ruby >= 3.4 (prism ships as a default gem).

## Test

```sh
bundle exec rake test        # or: ruby -Itest -Ilib test/structure_test.rb
```
