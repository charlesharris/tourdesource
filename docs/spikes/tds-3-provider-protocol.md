# Spike TDS-3 — provider protocol + Ruby hello-world

**Question:** Can the Go core drive a native (Ruby) provider over a simple JSON
protocol and get real structural symbols back from an actual Rails file?

**Answer:** Yes. The core spawns `ruby provider.rb`, exchanges JSON over stdio,
and prism returns clean class/module/method symbols with qualified paths and
line ranges. The seam works; this validates the architecture in `design.md` §9.

## What was built

- `internal/spike/provider/ruby/provider.rb` — a ~90-line Ruby provider using
  prism. Answers `capabilities` and `structure`.
- `internal/spike/provider/testdata/app/models/invoice.rb` — a Rails-style fixture.
- `internal/spike/provider/provider_spike_test.go` — the Go core side: spawns
  the provider, exchanges JSON, asserts on the returned symbols.

Result on the fixture: `Invoice` (class, 2–18), `Invoice#finalize`,
`Invoice#finalized?`, and `Invoice.overdue` — note the provider correctly
distinguishes `#` (instance) from `.` (singleton) methods. Toolchain: Ruby
3.4.4, prism 1.9.0, Go 1.26.

## Draft protocol (spike shape → input to TDS-5)

Request (stdin) / response (stdout), one JSON object each:

```jsonc
// capabilities
--> {"op":"capabilities"}
<-- {"protocol":"0.0.1-spike","provider":"tds-provider-ruby",
     "languages":["ruby"],"operations":["capabilities","structure"],
     "analyzers":[],"prism":"1.9.0"}

// structure
--> {"op":"structure","root":"testdata","files":["app/models/invoice.rb"]}
<-- {"symbols":[
     {"path":"...","kind":"class","name":"Invoice","symbol":"Invoice",
      "start_line":2,"end_line":18}, ...]}
```

Symbol shape: `{path, kind, name, symbol (qualified), start_line, end_line}`.
A future `analyze` op returns `{findings:[...]}` (not built in this spike).

## Findings & recommendations for TDS-5 (protocol v1)

1. **Normalization is the provider's job.** The provider emits already-qualified
   symbol paths (`Class#method`, `Class.singleton`, `Module::Class`), keeping the
   core language-neutral. Confirms design §9.2. Keep this.
2. **Transport: move from one-shot spawn to a persistent process.** Each spawn
   costs ~170ms (Ruby + prism startup). One-shot is fine for a spike but would be
   too slow per-file on a large repo. **Recommend:** a long-lived provider process
   with **newline-delimited JSON (JSONL)** framing — one request per line, one
   response per line, correlated by an `id`. Falls back to one-shot if a provider
   declares it can't stay resident.
3. **Batching.** `structure`/`analyze` should take a *list* of files (as here) so
   the provider parses many files per message, amortizing IPC.
4. **Version negotiation.** `capabilities.protocol` is the negotiation point; the
   core should check a semver range and refuse/downgrade on mismatch.
5. **Error & partial semantics.** Need a structured error channel (per-file
   errors shouldn't fail the whole batch) — e.g. `{"symbols":[...],"errors":[{"path":...,"message":...}]}`.
   Not modeled in the spike.
6. **Stderr is for logs, stdout is for protocol.** The spike already keeps them
   separate; make it a rule so provider logging can't corrupt the framing.
7. **Discovery.** The core located the provider by path here; TDS-6 adds PATH /
   `tds.toml` discovery + the `capabilities` handshake.

No architectural surprises — the provider seam is sound and ready to harden in
TDS-5/TDS-6, with the native Ruby provider proper built in TDS-10.
