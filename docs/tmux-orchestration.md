# Driving an assistant over tmux

How `internal/orchestration` talks to Claude Code, and — more usefully — the
things that do not work. This supersedes the mechanics section of
[spike TDS-2](spikes/tds-2-tmux-orchestration.md); the spike's own conclusions
about *architecture* (file-mediated I/O, marker-file completion) still hold.

## The shape

| Channel | Mechanism | Carries |
|---|---|---|
| Input | `load-buffer` → `paste-buffer`, then one `Enter` | one instruction line |
| Payload out | a file the assistant is told to read | the prompt (may be 10s of KB) |
| Payload back | a file the assistant is told to write | the JSON answer |
| Completion | a `.done` marker file, polled | "the answer is ready" |
| State | `capture-pane` | only: "is the TUI up yet?" |

The division is the whole design. tmux moves *keystrokes* and shows you a live
window; the filesystem moves *data*.

## Paste the text, then press Enter

Two failures, found the hard way, and the fix is a combination of both lessons.

**1. Never type text with `send-keys -l`.** It simulates typing keystroke by
keystroke, and an interactive TUI is free to debounce, re-render mid-stream, or
drop what arrives while it is painting — Claude Code does. The symptom is silent
and expensive: the instruction never lands, the assistant sits at an empty
prompt, and the run burns its entire timeout with nothing to show. A 10-minute
Redmine run died this way.

**2. A trailing newline in the buffer does not submit.** It looks like it should
— `paste-buffer` without `-p` bypasses bracketed paste, so a newline *is*
delivered as Enter, and for a short one-line instruction it submits correctly.
But Claude Code treats a paste that **wraps across multiple display lines** as a
pasted block and inserts the newline literally. The instruction then sits in the
input box, perfectly delivered and completely inert. This is nastier than
failure 1 because `capture-pane` shows the text present and correct.

So: buffer for the content, one keypress for the submit.

```sh
# Wrong — simulated typing is lossy
tmux send-keys -t "$session" -l "$instruction"

# Wrong — submits a short instruction, silently inert once it wraps
printf '%s\n' "$instruction" | tmux load-buffer -b "$buf" -
tmux paste-buffer -b "$buf" -t "$session" -d

# Right
printf '%s' "$instruction" | tmux load-buffer -b "$buf" -   # no trailing newline
tmux paste-buffer -b "$buf" -t "$session" -d
sleep 0.25                                                  # let the TUI reflow
tmux send-keys -t "$session" Enter
```

`send-keys` is still the only way to press a key, and that is fine: a lone Enter
carries no text to garble or drop. The rule is about *content*, not about the
command.

Details that matter:

- **Load from stdin, not argv.** Instructions carry absolute paths and
  punctuation that should never go near shell quoting, and argv has a length
  limit a long instruction can reach.
- **Keep the instruction to one line.** Long content belongs in the prompt file.
- **Pause before Enter.** Enter arriving while the TUI is still reflowing a long
  paste can be swallowed.
- **`-d` deletes the buffer after pasting**, so a long run cannot accumulate
  buffers or paste a stale one twice.

## Verify the paste landed

Even a correct paste can vanish. Readiness is judged from the pane, and the pane
can look ready before the input box will accept anything — so `paste-buffer`
succeeds, tmux reports no error, and the text is simply gone. The run then waits
out its entire timeout on an assistant that was never asked anything.

So don't trust the paste: confirm it. After pasting, poll `capture-pane` for a
witness string (we use the request nonce, which appears in all three paths), and
only press Enter once it is visible. Retry the paste if it is not.

Two details:

- **Strip whitespace on both sides before comparing.** The pane wraps long lines
  wherever it likes, including through the middle of a path, so a literal
  substring search misses a witness that is plainly on screen.
- **Clear the input box before each retry** (`C-u`). A partially landed paste
  plus a retry concatenates into nonsense and submits it.

## Readiness markers must not match dialogs

`❯` looks like a good "the prompt is live" marker. It is not: it is also the
menu **selection caret**, so it matches Claude Code's first-run dialogs —

```
 Quick safety check: Is this a project you created or one you trust?
 ❯ 1. Yes, I trust this folder
   2. No, exit
```

— and we declare readiness against a modal that discards pasted text. This cost
two Redmine runs before it was spotted. Match something that only appears on a
live input prompt (the `bypass permissions on` footer hint), and **detect the
known dialogs explicitly** so a blocked assistant fails immediately with a
message naming the dialog, instead of timing out.

Related: don't start the assistant in a directory it has never seen, or you
invite the trust dialog on every run. Prompt files are addressed by absolute
path, so the assistant's working directory does not need to be the work
directory — start it in the user's own cwd, which they have already approved.

## Use `capture-pane` only for state

Read the pane to answer "is it ready to accept input?" — nothing else. Claude
Code repaints constantly, wraps long lines, and draws boxes; scraping it for a
payload or a completion sentinel breaks on all three. Completion is a file.

Readiness genuinely matters: the pane is blank for several seconds at startup and
anything delivered before the TUI exists is lost. Wait for a stable marker
(`bypass permissions on` in the footer) before the first paste.

## Never put a path next to punctuation

The instruction names three paths. Write them so each is followed by whitespace:

```
...write your answer as a single JSON object to the file /tmp/x-out.json and
finally create an empty file at /tmp/x.done to signal that you are finished.
```

not

```
...write your answer to /tmp/x-out.json. Then create /tmp/x.done.
```

A reader — a model, or a shell script parsing the line — will happily take the
trailing `.` as part of the filename, and the answer lands somewhere nobody is
watching. Our own mock assistant fell for this first, which is how we found it.

## Testing it

`internal/orchestration/orchestration_test.go` runs a shell-script stand-in for
the assistant in a **real tmux pane**, so the buffer paste, the readiness wait
and the completion polling are all genuinely exercised. An in-process fake would
test none of the parts that actually break.

Tests assert the prompt arrives *intact* (the answer echoes a marker read from
the prompt file) rather than merely that something ran — the send-keys bug would
have passed a weaker check.

**What the mock cannot catch.** A shell stand-in reads its stdin a line at a
time, so it accepts input from any of the three mechanisms above, including the
two broken ones. Failure 2 in particular is a property of Claude Code's paste
handling and reproduces nowhere else. The mock covers delivery, correlation,
timeouts and cleanup; submission semantics have to be verified against the real
TUI, so re-check them by hand after changing anything in `submit`.
