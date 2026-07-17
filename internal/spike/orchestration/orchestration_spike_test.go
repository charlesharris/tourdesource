package orchestration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping orchestration spike")
	}
}

// TestOrchestrationMockReliable drives the mock assistant 10 times through the
// full tmux round trip and requires 10/10 successes with correctly correlated
// nonces — the acceptance for the harness mechanics (send-keys, capture-pane,
// sentinel + output-file completion detection).
func TestOrchestrationMockReliable(t *testing.T) {
	requireTmux(t)
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby not installed; skipping mock")
	}

	script, err := filepath.Abs(filepath.Join("mockclaude", "mockclaude.rb"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	sess, err := StartSession(fmt.Sprintf("tds2-mock-%d", os.Getpid()), dir, "ruby", script)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Kill()

	if err := sess.WaitFor("READY", 5*time.Second); err != nil {
		t.Fatalf("assistant never became ready: %v", err)
	}

	const runs = 10
	for i := 0; i < runs; i++ {
		nonce := fmt.Sprintf("n%02d", i)
		want := fmt.Sprintf("task body %d", i)
		res, err := sess.Do(Request{Dir: dir, Prompt: want, Nonce: nonce, Timeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("run %d/%d failed: %v", i+1, runs, err)
		}
		if res["ok"] != true {
			t.Fatalf("run %d: ok != true: %v", i, res)
		}
		if res["nonce"] != nonce {
			t.Fatalf("run %d: nonce = %v, want %q (stale/crossed response)", i, res["nonce"], nonce)
		}
		if res["task"] != want {
			t.Fatalf("run %d: task = %v, want %q", i, res["task"], want)
		}
	}
	t.Logf("%d/%d runs succeeded with correctly correlated nonces", runs, runs)
}

// TestRealClaudeIntegration drives the actual `claude --dangerously-skip-permissions`
// through the same harness. Gated behind TDS_SPIKE_REAL_CLAUDE=1 so it never runs
// in CI or normal `make check` (it spends real subscription tokens). Run manually:
//
//	TDS_SPIKE_REAL_CLAUDE=1 go test ./internal/spike/orchestration/ -run RealClaude -v
func TestRealClaudeIntegration(t *testing.T) {
	if os.Getenv("TDS_SPIKE_REAL_CLAUDE") != "1" {
		t.Skip("set TDS_SPIKE_REAL_CLAUDE=1 to run the real-claude integration (spends tokens)")
	}
	requireTmux(t)
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}

	dir := t.TempDir()
	sess, err := StartSession(fmt.Sprintf("tds2-claude-%d", os.Getpid()), dir,
		"claude", "--dangerously-skip-permissions")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Kill()

	// Readiness: wait for the TUI's stable footer marker before sending — keys
	// typed before the input box exists are lost (verified during the spike).
	if err := sess.WaitFor("bypass permissions", 60*time.Second); err != nil {
		t.Fatalf("claude TUI never became ready: %v", err)
	}

	nonce := fmt.Sprintf("r%d", os.Getpid())
	outPath := filepath.Join(dir, nonce+"-out.json")
	donePath := filepath.Join(dir, nonce+".done")
	instr := fmt.Sprintf(
		"Write the JSON object {\"ok\": true} to %s, then create an empty file at %s. Do only that.",
		outPath, donePath)
	if err := sess.SendLine(instr); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Completion via the filesystem, not the pane.
	if err := sess.WaitForFile(donePath, 120*time.Second); err != nil {
		pane, _ := sess.Capture()
		t.Fatalf("no done marker: %v\n--- pane ---\n%s", err, pane)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("done marker present but no output file: %v", err)
	}
	t.Log("real claude produced the output file and done marker")
}
