# Spike TDS-2 — tmux ↔ Claude orchestration

**Question:** Can `tds` reliably drive `claude --dangerously-skip-permissions`
inside a tmux pane, exchange work via files, and detect completion — on the
author's subscription, without an API key?

**Answer:** Yes. The harness ran a mock assistant **10/10** through the full
round trip, and a real `claude` run wrote the correct JSON + a `.done` marker in
~8s. Two design refinements fell out of the spike (below).

## What was built

- `internal/spike/orchestration/driver.go` — a Go tmux driver: `StartSession`,
  `SendLine` (literal `send-keys` + `Enter`), `Capture`, `WaitFor` (pane marker,
  for readiness), `WaitForFile` (filesystem, for completion), and `Do` (one
  file-mediated round trip).
- `internal/spike/orchestration/mockclaude/mockclaude.rb` — stands in for
  Claude's role so the mechanics are tested deterministically, for free.
- `orchestration_spike_test.go` — `TestOrchestrationMockReliable` (10/10, always
  runs) and `TestRealClaudeIntegration` (gated behind `TDS_SPIKE_REAL_CLAUDE=1`
  so it never spends tokens in CI / `make check`).

Toolchain: tmux 3.6a, Claude Code v2.1.212, Go 1.26.

## Findings (these amend the design)

1. **Readiness must be detected before sending.** Keystrokes sent before the TUI
   is up are silently lost — this is what made the naive first attempt fail. The
   pane is blank for >5s at startup, then renders a bordered TUI. Wait for a
   stable marker (`bypass permissions on` in the footer, or the `❯` input prompt)
   before `SendLine`.
2. **Detect completion on the filesystem, not the pane.** Claude Code renders on
   the alternate screen; scraping `capture-pane` for a printed sentinel is
   fragile (box-drawing, scrolling, redraws). Instead, instruct the assistant to
   write the payload **and then** a `.done` marker file, and poll for the marker.
   This is TUI-agnostic and avoids reading a half-written payload. tmux is then
   purely the *input* transport + an observability window; **the filesystem is
   the data and completion channel.**
3. **Mechanics that work:** `tmux new-session -d -s NAME -c DIR <program>`;
   `send-keys -t NAME -l "text"` then `send-keys -t NAME Enter`; `capture-pane -p`;
   `--dangerously-skip-permissions` lets Claude read/write the files with no
   prompt. `pane_dead` / `pane_current_command` are available for liveness checks.

## The readiness/completion protocol (recommended for TDS-38)

```
start tmux session running `claude --dangerously-skip-permissions` in a work dir
wait for pane to contain the readiness marker         (WaitFor, ~30–60s budget)
write <nonce>-prompt.md
send-keys: "Read <prompt>. Write your answer as JSON to <nonce>-out.json,
            then create <nonce>.done. Do only that."
wait for <nonce>.done to exist                        (WaitForFile, per-task budget)
read + JSON-parse <nonce>-out.json
```

## Left for TDS-38 (hardening)

- Robust readiness matching that survives Claude version/footer changes.
- Timeouts + retries; detect a dead pane (`pane_dead=1`) or a stuck/asking model
  and recover or surface a clear error.
- **Session reuse:** keep one resident session and stream many requests (each
  with its own nonce) to amortize the multi-second startup — the two-pass draft
  (TDS-41/42) will issue many requests.
- Per-request temp dirs; cleanup on failure; never depend on the model's prose,
  only on the files it writes.
