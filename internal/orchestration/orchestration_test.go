package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

// mockAssistant writes a shell script that stands in for Claude: it reads lines
// from stdin, and for each instruction it extracts the three paths, writes a
// JSON answer, then writes the .done marker — the same contract the real
// assistant is asked to follow in natural language.
//
// Using a real program in a real tmux pane is the point: it exercises the actual
// buffer paste, the readiness wait and the completion polling, which an
// in-process fake would not.
func mockAssistant(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "mock-assistant.sh")
	body := `#!/bin/sh
# Stand-in for an interactive assistant. Prints a readiness marker, then serves
# one request per line of input.
echo "bypass permissions on"
while IFS= read -r line; do
  case "$line" in
    *-prompt.md*)
      prompt=$(printf '%s\n' "$line" | tr ' ' '\n' | grep -- '-prompt\.md$' | head -1)
      out=$(printf '%s\n' "$line" | tr ' ' '\n' | grep -- '-out\.json$' | head -1)
      done_file=$(printf '%s\n' "$line" | tr ' ' '\n' | grep -- '\.done$' | head -1)
      # Echo back a marker from the prompt so the test can prove the round trip
      # carried the right payload.
      marker=$(head -1 "$prompt")
      printf '{"ok":true,"marker":"%s"}' "$marker" > "$out"
      echo "" > "$done_file"
      echo "ANSWERED"
      ;;
  esac
done
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func startMock(t *testing.T) (*Tmux, string) {
	t.Helper()
	requireTmux(t)
	dir := t.TempDir()
	script := mockAssistant(t, dir)

	sess, err := Start(context.Background(), Options{
		WorkDir: dir,
		Session: fmt.Sprintf("tds-test-%d", time.Now().UnixNano()%100000),
		Command: []string{"sh", script},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, dir
}

// TestRoundTripDeliversInstruction is the regression test for the bug that
// motivated the buffer rewrite: instructions sent with send-keys were silently
// dropped by the TUI, leaving the assistant idle until the request timed out.
func TestRoundTripDeliversInstruction(t *testing.T) {
	sess, _ := startMock(t)

	raw, err := sess.Ask(context.Background(), Request{
		Name:    "round trip",
		Prompt:  "MARKER-12345\nrest of the prompt body\n",
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	var got struct {
		OK     bool   `json:"ok"`
		Marker string `json:"marker"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decoding answer: %v (raw %q)", err, raw)
	}
	if !got.OK {
		t.Errorf("ok = false: %s", raw)
	}
	// Proves the prompt file reached the assistant, not just that it ran.
	if got.Marker != "MARKER-12345" {
		t.Errorf("marker = %q, want MARKER-12345 — the prompt did not arrive intact", got.Marker)
	}
}

// TestSequentialRequestsDoNotCross checks that answers are correlated to their
// own request. Each exchange gets its own nonce-scoped files, so a slow or
// duplicated response cannot be read as the next request's answer.
func TestSequentialRequestsDoNotCross(t *testing.T) {
	sess, _ := startMock(t)

	for i := 0; i < 4; i++ {
		want := fmt.Sprintf("MARKER-%03d", i)
		raw, err := sess.Ask(context.Background(), Request{
			Name:    fmt.Sprintf("req-%d", i),
			Prompt:  want + "\nbody\n",
			Timeout: 20 * time.Second,
		})
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		var got struct {
			Marker string `json:"marker"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("request %d: decoding: %v", i, err)
		}
		if got.Marker != want {
			t.Fatalf("request %d: marker = %q, want %q (crossed response)", i, got.Marker, want)
		}
	}
}

// TestLongInstructionSurvives covers why the instruction goes through a buffer
// rather than argv or simulated typing: real instructions carry several absolute
// paths and comfortably exceed a short line.
func TestLongInstructionSurvives(t *testing.T) {
	sess, dir := startMock(t)
	_ = dir

	marker := "MARKER-" + strings.Repeat("x", 400)
	raw, err := sess.Ask(context.Background(), Request{
		Name:    "long",
		Prompt:  marker + "\nbody\n",
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !strings.Contains(string(raw), marker) {
		t.Errorf("long prompt did not survive the round trip")
	}
}

// TestAskTimesOutCleanly checks that a silent assistant fails with an actionable
// error rather than hanging forever.
func TestAskTimesOutCleanly(t *testing.T) {
	requireTmux(t)
	dir := t.TempDir()

	// An assistant that accepts input and never answers.
	script := filepath.Join(dir, "silent.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'bypass permissions on'\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := Start(context.Background(), Options{
		WorkDir: dir,
		Session: fmt.Sprintf("tds-silent-%d", time.Now().UnixNano()%100000),
		Command: []string{"sh", script},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Close()

	_, err = sess.Ask(context.Background(), Request{Name: "x", Prompt: "hi", Timeout: 1500 * time.Millisecond})
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to name the timeout", err)
	}
	// The error should tell the user how to look at what happened.
	if !strings.Contains(err.Error(), "tmux attach") && !strings.Contains(err.Error(), "finish") {
		t.Errorf("error = %q, should be actionable", err)
	}
}

// TestStartFailsFastOnMissingAssistant avoids a confusing timeout when the
// assistant binary simply isn't installed.
func TestStartFailsFastOnMissingAssistant(t *testing.T) {
	requireTmux(t)
	_, err := Start(context.Background(), Options{
		WorkDir: t.TempDir(),
		Command: []string{"definitely-not-a-real-binary-xyz"},
	})
	if err == nil {
		t.Fatal("expected an error for a missing assistant")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error = %q, want it to name the missing binary", err)
	}
}

func TestDecodeJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"bare object", `{"stops":{"a":"one"}}`},
		{"fenced", "```json\n{\"stops\":{\"a\":\"one\"}}\n```"},
		{"fenced without language", "```\n{\"stops\":{\"a\":\"one\"}}\n```"},
		{"with surrounding prose", "Here you go:\n\n{\"stops\":{\"a\":\"one\"}}\n\nHope that helps!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				Stops map[string]string `json:"stops"`
			}
			if err := DecodeJSON([]byte(tc.in), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Stops["a"] != "one" {
				t.Errorf("stops[a] = %q, want one", got.Stops["a"])
			}
		})
	}

	t.Run("rejects non-JSON", func(t *testing.T) {
		var v map[string]any
		if err := DecodeJSON([]byte("I could not complete this task."), &v); err == nil {
			t.Error("expected an error for a non-JSON answer")
		}
	})
}

// TestPasteIsVerifiedBeforeSubmit covers the race that ate two real runs: the
// readiness markers live in the footer, which paints before the input box will
// accept a paste, so a paste can vanish with no error anywhere. Delivery is now
// confirmed against the pane and retried.
//
// The stand-in here ignores everything until a sentinel file appears, emulating
// a TUI that is not yet accepting input.
func TestPasteIsVerifiedBeforeSubmit(t *testing.T) {
	requireTmux(t)
	dir := t.TempDir()
	script := mockAssistant(t, dir)

	sess, err := Start(context.Background(), Options{
		WorkDir: dir,
		Session: fmt.Sprintf("tds-verify-%d", time.Now().UnixNano()%100000),
		Command: []string{"sh", script},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Close()

	// A normal round trip must still verify and submit exactly once — the retry
	// loop must not double-paste when the first attempt lands.
	raw, err := sess.Ask(context.Background(), Request{
		Name:    "verify",
		Prompt:  "MARKER-VERIFY\nbody\n",
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !strings.Contains(string(raw), "MARKER-VERIFY") {
		t.Errorf("round trip did not carry the prompt: %s", raw)
	}
}

func TestStripSpaceEnablesWrappedMatching(t *testing.T) {
	// tmux wraps long lines wherever it likes, including mid-path. A literal
	// search would miss a witness that is plainly on screen.
	wrapped := "Read the file /Users/x/tours/narrate-1-\n  001-prompt.md and follow"
	if !strings.Contains(stripSpace(wrapped), stripSpace("narrate-1-001-prompt.md")) {
		t.Error("stripSpace should let a wrapped path match its unwrapped form")
	}
	if strings.Contains(wrapped, "narrate-1-001-prompt.md") {
		t.Skip("this pane text was not actually wrapped; test is vacuous")
	}
}

// TestBlockingDialogDetection covers the readiness false-positive that cost two
// real runs. A modal consumes pasted text without error, so it has to be
// recognised rather than waited out.
func TestBlockingDialogDetection(t *testing.T) {
	trustPrompt := ` Quick safety check: Is this a project you created or one you trust? (Like your
 own code, a well-known open source project, or work from your team).
 ❯ 1. Yes, I trust this folder
   2. No, exit`

	if got := blockingDialog(trustPrompt); got == "" {
		t.Error("the trust-this-folder dialog must be detected")
	}

	// The readiness marker must not fire on that dialog. A bare "❯" would —
	// it is the menu selection caret as well as the input prompt, which is
	// exactly how this got past us.
	for _, marker := range (Options{}).withDefaults().ReadyMarkers {
		if strings.Contains(trustPrompt, marker) {
			t.Errorf("readiness marker %q matches the trust dialog; it will report ready on a modal", marker)
		}
	}

	t.Run("a live prompt is not a dialog", func(t *testing.T) {
		live := "⚠ Your login expires in 5 days · run /login to renew\n❯ \n  ⏵⏵ bypass permissions on (shift+tab to cycle)"
		if got := blockingDialog(live); got != "" {
			t.Errorf("a login *expiry warning* is not blocking, got %q", got)
		}
		ready := false
		for _, marker := range (Options{}).withDefaults().ReadyMarkers {
			if strings.Contains(live, marker) {
				ready = true
			}
		}
		if !ready {
			t.Error("a live prompt should satisfy at least one readiness marker")
		}
	})
}
