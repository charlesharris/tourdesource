// Package orchestration drives an interactive assistant (Claude Code) to answer
// structured requests, so generation runs on the author's own subscription
// rather than through an API key.
//
// The transport is deliberately split (spike TDS-2):
//
//   - tmux carries *input*, via its paste buffers. The assistant runs in a
//     detached pane; we load the instruction into a buffer and paste it. The
//     pane doubles as an observability window — you can `tmux attach` mid-run.
//   - the filesystem carries *data and completion*. The assistant reads its
//     prompt from a file and writes its answer to a file, then creates a `.done`
//     marker. We poll for the marker, never scrape the TUI for the payload.
//
// Instruction *text* goes through `load-buffer`/`paste-buffer`, never typed with
// `send-keys`. Simulated typing is delivered keystroke by keystroke and an
// interactive TUI is free to debounce, re-render, or drop it mid-stream — which
// it does: an earlier version of this package silently lost whole instructions
// that way, leaving the assistant idling at an empty prompt until the request
// timed out. A buffer paste is one atomic write.
//
// *Submission* is then a single Enter keypress. A trailing newline inside the
// buffer is not enough: Claude Code treats a paste that wraps across lines as a
// pasted block and inserts the newline literally, so the instruction sits in the
// input box looking delivered while nothing happens. One keypress carries no
// text to garble, so it is not subject to the problem above.
//
// `capture-pane` is used only to read *state* — whether the TUI is up. Never for
// the payload: Claude Code re-renders constantly, so scraping output for a
// sentinel breaks on box-drawing, scrolling and redraws. Writing the `.done`
// marker after the answer file also means we never read a half-written payload.
//
// This is the hardened form of internal/spike/orchestration (TDS-38).
package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultTimeout bounds a single request. Generation can be slow; this is a
// backstop against a wedged pane, not a latency target.
const DefaultTimeout = 5 * time.Minute

// readyTimeout bounds how long we wait for the assistant's TUI to come up.
// Spike finding: the pane is blank for >5s at startup and keystrokes sent
// before it is ready are silently lost.
const readyTimeout = 90 * time.Second

// pollInterval is how often readiness and completion are checked.
const pollInterval = 250 * time.Millisecond

// Request is one file-mediated exchange.
type Request struct {
	// Name labels this request's files; it is slugged into the filenames so a
	// run's working directory stays readable while debugging.
	Name string
	// Prompt is written to a file the assistant is told to read. It never goes
	// through the pane: prompts are multi-line and full of code, and every
	// newline in a pasted buffer submits, so pasting a prompt would fire it off
	// a line at a time.
	Prompt string
	// Timeout overrides DefaultTimeout for this request.
	Timeout time.Duration
}

// Assistant answers requests. Production drives Claude in tmux; tests use a
// scripted stand-in, so the narrate pipeline is testable without spending tokens.
type Assistant interface {
	// Ask performs one round trip and returns the raw response bytes, which the
	// caller decodes and validates.
	Ask(ctx context.Context, req Request) ([]byte, error)
	Close() error
}

// Options configure a tmux-hosted assistant.
type Options struct {
	// WorkDir holds prompt/answer files. It must exist and should be outside the
	// repository under tour, so generation never dirties the subject checkout.
	// Every path in an instruction is absolute, so this need not be the
	// assistant's working directory — and deliberately isn't; see StartDir.
	WorkDir string
	// StartDir is the assistant's working directory. It defaults to the process's
	// own cwd rather than WorkDir, because Claude Code shows a blocking
	// "do you trust this folder?" dialog the first time it starts in an unfamiliar
	// directory — and WorkDir is freshly created on most runs, so it would hit
	// that dialog every time.
	StartDir string
	// Session names the tmux session; a unique suffix is appended.
	Session string
	// Command is the assistant to run. Defaults to Claude Code with permission
	// prompts bypassed — it only ever reads a prompt file and writes an answer
	// file inside WorkDir.
	Command []string
	// ReadyMarkers are pane substrings that mean "the TUI is up". Any one is
	// enough. Defaults suit Claude Code.
	ReadyMarkers []string
	// Logf receives progress lines.
	Logf func(format string, a ...any)
}

func (o Options) withDefaults() Options {
	if len(o.Command) == 0 {
		o.Command = []string{"claude", "--dangerously-skip-permissions"}
	}
	if len(o.ReadyMarkers) == 0 {
		// Deliberately NOT a bare "❯": that is the menu selection caret too, so it
		// matches Claude Code's blocking dialogs and reports readiness while the
		// TUI is refusing text. The footer hint only appears on a live prompt.
		o.ReadyMarkers = []string{"bypass permissions on"}
	}
	if o.StartDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			o.StartDir = cwd
		} else {
			o.StartDir = o.WorkDir
		}
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Session == "" {
		o.Session = "tds"
	}
	return o
}

// Tmux is an assistant running in a detached tmux pane.
type Tmux struct {
	opts    Options
	session string
	seq     int
}

// Start launches the assistant and waits for it to become ready to accept input.
func Start(ctx context.Context, opts Options) (*Tmux, error) {
	opts = opts.withDefaults()

	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("tmux is required to drive the assistant: %w", err)
	}
	if _, err := exec.LookPath(opts.Command[0]); err != nil {
		return nil, fmt.Errorf("assistant %q not found on PATH: %w", opts.Command[0], err)
	}
	if opts.WorkDir == "" {
		return nil, fmt.Errorf("orchestration: WorkDir is required")
	}
	if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating work dir: %w", err)
	}

	session := fmt.Sprintf("%s-%d", opts.Session, os.Getpid())
	args := append([]string{"new-session", "-d", "-s", session, "-c", opts.StartDir}, opts.Command...)
	if out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session: %v: %s", err, strings.TrimSpace(string(out)))
	}

	t := &Tmux{opts: opts, session: session}
	opts.Logf("assistant starting in tmux session %q (attach with: tmux attach -t %s)", session, session)

	if err := t.waitReady(ctx); err != nil {
		t.Close()
		return nil, err
	}
	opts.Logf("assistant ready")
	return t, nil
}

// waitReady polls the pane until a readiness marker appears. Sending before this
// loses the keystrokes silently, which is the single most common way this
// integration fails.
func (t *Tmux) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		pane, err := t.capture()
		if err == nil {
			// A modal dialog swallows pasted text without error, so surface it as
			// a real failure rather than letting the run time out mysteriously.
			if blocked := blockingDialog(pane); blocked != "" {
				return fmt.Errorf("the assistant is waiting on a prompt it needs a human for: %s\n"+
					"Answer it once with `tmux attach -t %s`, or start the assistant in a "+
					"directory you have already approved (--narrate-workdir does not change this; "+
					"the assistant runs in your current directory)", blocked, t.session)
			}
			for _, m := range t.opts.ReadyMarkers {
				if strings.Contains(pane, m) {
					return nil
				}
			}
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("assistant did not become ready within %s "+
		"(attach with `tmux attach -t %s` to see why)", readyTimeout, t.session)
}

// blockingDialogs are first-run prompts that consume input and answer nothing.
// Pasting into one looks like success — the text simply disappears.
var blockingDialogs = []struct{ marker, describe string }{
	{"Is this a project you created or one you trust", "the trust-this-folder prompt"},
	{"Do you trust the files in this folder", "the trust-this-folder prompt"},
	{"Select login method", "the login prompt"},
	{"Please run /login", "an expired login"},
}

// blockingDialog names the dialog visible in the pane, or "" if none is.
func blockingDialog(pane string) string {
	for _, d := range blockingDialogs {
		if strings.Contains(pane, d.marker) {
			return d.describe
		}
	}
	return ""
}

// Ask performs one request/response round trip.
func (t *Tmux) Ask(ctx context.Context, req Request) ([]byte, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	t.seq++
	nonce := fmt.Sprintf("%s-%03d", slug(req.Name), t.seq)
	promptPath := filepath.Join(t.opts.WorkDir, nonce+"-prompt.md")
	outPath := filepath.Join(t.opts.WorkDir, nonce+"-out.json")
	donePath := filepath.Join(t.opts.WorkDir, nonce+".done")

	// Stale files from an earlier run would be read as this run's answer.
	for _, p := range []string{outPath, donePath} {
		_ = os.Remove(p)
	}
	if err := os.WriteFile(promptPath, []byte(req.Prompt), 0o644); err != nil {
		return nil, fmt.Errorf("writing prompt: %w", err)
	}

	// The instruction is natural language because the assistant is Claude, not a
	// command interpreter. It must stay a single line: the paste is submitted by
	// its trailing newline, so an embedded newline would send the instruction as
	// two separate messages, the first of them incomplete. The bulk of the work
	// lives in the prompt file, which has no such constraint.
	// Every path is followed by whitespace, never by punctuation. A path written
	// as "...to /tmp/x-out.json." invites the reader — model or script — to take
	// the trailing period as part of the filename, and the answer then lands
	// somewhere nobody is looking.
	instruction := fmt.Sprintf(
		"Read the file %s and follow the instructions in it, "+
			"then write your answer as a single JSON object to the file %s "+
			"and finally create an empty file at %s to signal that you are finished. "+
			"Do not print the answer in this conversation, and do not modify any other file.",
		promptPath, outPath, donePath)

	// The nonce appears in every path in the instruction, so it is a reliable
	// witness that the paste actually reached the input box.
	if err := t.submit(ctx, instruction, nonce); err != nil {
		return nil, err
	}
	t.opts.Logf("sent request %q, waiting up to %s", nonce, timeout)

	if err := t.waitForFile(ctx, donePath, timeout); err != nil {
		return nil, fmt.Errorf("request %q: %w", nonce, err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("request %q: done marker written but no answer file: %w", nonce, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("request %q: answer file is empty", nonce)
	}
	return data, nil
}

// waitForFile polls for the completion marker. This is the completion signal —
// the pane is never parsed for it.
func (t *Tmux) waitForFile(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for the assistant to finish", timeout)
}

// submit delivers one instruction to the pane and submits it: the text goes in
// through a tmux paste buffer, and a single Enter keypress sends it.
//
// The split is deliberate. Buffer paste for the *content* — see the package
// comment for why simulated typing loses it. A lone Enter for the *submit*,
// because there is no buffer-based way to press a key, and one control keypress
// is not what breaks: it carries no text to garble or drop.
//
// A trailing newline inside the buffer will not do it. That works for a short
// instruction, but Claude Code treats a paste that wraps across lines as a
// pasted block, where the newline is inserted literally instead of submitting.
// The instruction then sits in the input box looking perfectly delivered while
// the run waits out its entire timeout — which is exactly what happened before
// this was split.
func (t *Tmux) submit(ctx context.Context, text, witness string) error {
	text = strings.TrimRight(text, "\r\n")

	// Readiness markers live in the footer, which paints before the input box
	// will actually accept a paste. Rather than guess at a settling delay, paste
	// and then confirm with capture-pane that the text is really in the input
	// box, retrying if it is not. This is the race that silently ate a whole
	// run: the paste vanished and we waited out the full timeout on an assistant
	// that had been asked nothing.
	const attempts = 5
	var lastErr error
	for i := 0; i < attempts; i++ {
		// Clear the input box first. A previous attempt may have landed
		// partially, and pasting on top of it would submit the instruction twice
		// over concatenated into nonsense.
		if err := t.clearInput(ctx); err != nil {
			return err
		}
		if err := t.paste(ctx, text); err != nil {
			return err
		}
		if ok, err := t.paneContains(ctx, witness, 3*time.Second); err != nil {
			lastErr = err
		} else if ok {
			return t.pressEnter(ctx)
		} else {
			lastErr = fmt.Errorf("the instruction did not appear in the pane")
		}
		t.opts.Logf("paste attempt %d/%d did not land, retrying", i+1, attempts)
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	return fmt.Errorf("could not deliver the instruction after %d attempts: %w "+
		"(attach with `tmux attach -t %s` to see the pane)", attempts, lastErr, t.session)
}

// paste loads the text into a tmux buffer and pastes it into the pane.
func (t *Tmux) paste(ctx context.Context, text string) error {
	buffer := fmt.Sprintf("tds-%s-%d", t.session, t.seq)

	// Load via stdin rather than an argv value: instructions carry absolute
	// paths and punctuation that have no business going through shell quoting,
	// and argv has a length limit that a long instruction can reach.
	//
	// No trailing newline: submission is the explicit Enter, and a newline here
	// would either double-submit or land as a literal line break.
	load := exec.CommandContext(ctx, "tmux", "load-buffer", "-b", buffer, "-")
	load.Stdin = strings.NewReader(text)
	if out, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %v: %s", err, strings.TrimSpace(string(out)))
	}

	// -d deletes the buffer after pasting, so a long run doesn't accumulate
	// buffers and a stale one can never be pasted twice.
	if out, err := exec.CommandContext(ctx, "tmux",
		"paste-buffer", "-b", buffer, "-t", t.session, "-d").CombinedOutput(); err != nil {
		_ = exec.Command("tmux", "delete-buffer", "-b", buffer).Run()
		return fmt.Errorf("tmux paste-buffer: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// paneContains polls the pane for a witness string.
//
// Both sides have their whitespace stripped before comparing: the pane wraps
// long lines wherever it likes, including through the middle of a path, so a
// literal search would miss a witness that is plainly there.
func (t *Tmux) paneContains(ctx context.Context, witness string, timeout time.Duration) (bool, error) {
	needle := stripSpace(witness)
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		pane, err := t.capture()
		if err != nil {
			lastErr = err
		} else if strings.Contains(stripSpace(pane), needle) {
			return true, nil
		}
		time.Sleep(pollInterval)
	}
	return false, lastErr
}

// clearInput empties the pane's input box (readline's "kill line").
func (t *Tmux) clearInput(ctx context.Context) error {
	if out, err := exec.CommandContext(ctx, "tmux",
		"send-keys", "-t", t.session, "C-u").CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys C-u: %v: %s", err, strings.TrimSpace(string(out)))
	}
	time.Sleep(100 * time.Millisecond)
	return nil
}

// stripSpace removes all whitespace, so wrapped pane text can be matched against
// the unwrapped original.
func stripSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pressEnter submits whatever is in the input box.
func (t *Tmux) pressEnter(ctx context.Context) error {
	// Let the TUI finish reflowing the pasted text; Enter arriving mid-reflow
	// can be swallowed.
	time.Sleep(250 * time.Millisecond)
	if out, err := exec.CommandContext(ctx, "tmux",
		"send-keys", "-t", t.session, "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys Enter: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (t *Tmux) capture() (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", t.session).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Close tears the session down. Safe to call more than once.
func (t *Tmux) Close() error {
	_ = exec.Command("tmux", "kill-session", "-t", t.session).Run()
	return nil
}

// DecodeJSON pulls a JSON object out of an assistant's answer. Assistants
// sometimes wrap JSON in a ```json fence despite being asked for a bare object,
// so that is tolerated rather than treated as a failure.
func DecodeJSON(data []byte, v any) error {
	s := strings.TrimSpace(string(data))
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		if j := strings.Index(rest, "```"); j >= 0 {
			s = strings.TrimSpace(rest[:j])
		}
	}
	// Trim any prose either side of the object.
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("assistant response is not valid JSON: %w", err)
	}
	return nil
}

// slug reduces a label to something safe for a filename.
func slug(s string) string {
	if s == "" {
		return "req"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '/':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "req"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}
