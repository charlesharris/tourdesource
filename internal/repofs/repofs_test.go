package repofs

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var hexSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	// Deterministic identity so commits succeed without a configured user.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeFile writes rel (slash path) under dir with content.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo builds a temp git repo committing a.rb="V1" and dir/b.txt, and
// returns its root.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	git(t, root, "-c", "init.defaultBranch=main", "init")
	writeFile(t, root, "a.rb", "V1")
	writeFile(t, root, "dir/b.txt", "hello b")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "initial")
	return root
}

func TestResolveHEAD(t *testing.T) {
	root := newRepo(t)
	want := git(t, root, "rev-parse", "HEAD")

	for _, commit := range []string{"", "auto"} {
		got, err := Resolve(root, commit)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", commit, err)
		}
		if !hexSHA.MatchString(got) {
			t.Errorf("Resolve(%q) = %q, not a 40-hex sha", commit, got)
		}
		if got != want {
			t.Errorf("Resolve(%q) = %q, want %q", commit, got, want)
		}
	}
}

func TestReadEnumerates(t *testing.T) {
	root := newRepo(t)
	snap, err := Read(root, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(snap.Files) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(snap.Files), snap.Files)
	}
	// Sorted by path: a.rb before dir/b.txt.
	if snap.Files[0].Path != "a.rb" || snap.Files[1].Path != "dir/b.txt" {
		t.Fatalf("unexpected/unsorted paths: %+v", snap.Files)
	}
	if snap.Files[0].Size != int64(len("V1")) {
		t.Errorf("a.rb size = %d, want %d", snap.Files[0].Size, len("V1"))
	}
	if snap.Files[1].Size != int64(len("hello b")) {
		t.Errorf("dir/b.txt size = %d, want %d", snap.Files[1].Size, len("hello b"))
	}
}

func TestContentIsPinned(t *testing.T) {
	root := newRepo(t)
	snap, err := Read(root, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Overwrite the working tree AFTER reading the snapshot, without committing.
	writeFile(t, root, "a.rb", "V2 changed")

	got, err := snap.Content("a.rb")
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if string(got) != "V1" {
		t.Errorf("Content(a.rb) = %q, want %q (snapshot must read committed bytes, not the working tree)", got, "V1")
	}
}

func TestWriteBlobs(t *testing.T) {
	root := newRepo(t)
	snap, err := Read(root, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Dirty the working tree to confirm blobs come from the commit, not disk.
	writeFile(t, root, "a.rb", "V2 changed")

	out := t.TempDir()
	indexPath, err := snap.WriteBlobs(out)
	if err != nil {
		t.Fatalf("WriteBlobs: %v", err)
	}

	aBytes, err := os.ReadFile(filepath.Join(out, "files", "a.rb"))
	if err != nil {
		t.Fatalf("reading files/a.rb: %v", err)
	}
	if string(aBytes) != "V1" {
		t.Errorf("files/a.rb = %q, want %q", aBytes, "V1")
	}
	if _, err := os.Stat(filepath.Join(out, "files", "dir", "b.txt")); err != nil {
		t.Errorf("files/dir/b.txt missing: %v", err)
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}
	var entries []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parsing index.json: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("index has %d entries, want 2: %+v", len(entries), entries)
	}
	byPath := map[string]int64{}
	for _, e := range entries {
		byPath[e.Path] = e.Size
	}
	if byPath["a.rb"] != int64(len("V1")) {
		t.Errorf("index a.rb size = %d, want %d", byPath["a.rb"], len("V1"))
	}
	if _, ok := byPath["dir/b.txt"]; !ok {
		t.Errorf("index missing dir/b.txt: %+v", entries)
	}
}

func TestResolveNonGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	if _, err := Resolve(dir, ""); err == nil {
		t.Error("Resolve on a non-git dir: got nil error, want error")
	}
}
