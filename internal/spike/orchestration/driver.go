// Package orchestration is the TDS-2 spike: driving an interactive assistant
// (Claude, or a mock) inside a tmux pane using file-mediated I/O and sentinel
// completion detection. tmux is the transport that keeps generation on the
// author's subscription and stays observable; data crosses the boundary through
// the filesystem, never by scraping the TUI. See
// docs/spikes/tds-2-tmux-orchestration.md. Throwaway — hardened in TDS-38.
package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Session is a running tmux session with one pane running a program.
type Session struct {
	Name string
}

// StartSession launches `program` detached in a new tmux session rooted at workdir.
func StartSession(name, workdir string, program ...string) (*Session, error) {
	args := append([]string{"new-session", "-d", "-s", name, "-c", workdir}, program...)
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux new-session: %v: %s", err, out)
	}
	return &Session{Name: name}, nil
}

// Kill tears the session down. Safe to call more than once.
func (s *Session) Kill() {
	_ = exec.Command("tmux", "kill-session", "-t", s.Name).Run()
}

// SendLine types a literal line into the pane and submits it with Enter.
func (s *Session) SendLine(text string) error {
	if out, err := exec.Command("tmux", "send-keys", "-t", s.Name, "-l", text).CombinedOutput(); err != nil {
		return fmt.Errorf("send-keys literal: %v: %s", err, out)
	}
	if out, err := exec.Command("tmux", "send-keys", "-t", s.Name, "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("send-keys enter: %v: %s", err, out)
	}
	return nil
}

// Capture returns the current visible pane contents.
func (s *Session) Capture() (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", s.Name).Output()
	if err != nil {
		return "", fmt.Errorf("capture-pane: %w", err)
	}
	return string(out), nil
}

// WaitFor polls the pane until `marker` appears or the timeout elapses. Used for
// READINESS detection — waiting for the assistant's input prompt before sending
// work. (Spike TDS-2 finding: keystrokes sent before the TUI is ready are lost.)
func (s *Session) WaitFor(marker string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pane, err := s.Capture()
		if err == nil && strings.Contains(pane, marker) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s waiting for %q", timeout, marker)
}

// WaitForFile polls the filesystem until `path` exists or the timeout elapses.
// This is the COMPLETION signal (spike TDS-2 finding): detecting done via a marker
// file the assistant writes is robust and TUI-agnostic — far more reliable than
// scraping Claude's alternate-screen pane for a sentinel.
func (s *Session) WaitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s waiting for file %q", timeout, path)
}

// Request is one file-mediated exchange: write a prompt, ask the assistant to
// answer into a JSON file and print a unique done-sentinel, then wait for BOTH
// the sentinel (in the pane) AND a parseable output file before returning it.
//
// The instruction sent here (`RUN ...`) is the mock's contract; the real-Claude
// instruction is the natural-language equivalent documented in the spike notes.
type Request struct {
	Dir     string        // working dir holding prompt/out files
	Prompt  string        // prompt body written to prompt.md
	Nonce   string        // unique per request; scopes the sentinel + output
	Timeout time.Duration // per-request budget
}

// Do performs one request/response round trip and returns the decoded JSON.
//
// Completion is detected by the assistant writing a `.done` marker AFTER the
// output file, so we never read a half-written payload and never parse the TUI.
// The `RUN ...` line is the mock's instruction contract; the real-Claude
// equivalent is the natural-language instruction in the spike notes.
func (s *Session) Do(req Request) (map[string]any, error) {
	promptPath := filepath.Join(req.Dir, req.Nonce+"-prompt.md")
	outPath := filepath.Join(req.Dir, req.Nonce+"-out.json")
	donePath := filepath.Join(req.Dir, req.Nonce+".done")
	if err := os.WriteFile(promptPath, []byte(req.Prompt), 0o644); err != nil {
		return nil, err
	}

	if err := s.SendLine(fmt.Sprintf("RUN %s %s %s %s", promptPath, outPath, donePath, req.Nonce)); err != nil {
		return nil, err
	}
	if err := s.WaitForFile(donePath, req.Timeout); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("done marker present but no output file: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("output file is not valid JSON: %w", err)
	}
	return result, nil
}
