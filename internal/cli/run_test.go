package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeTour creates an empty tour file in dir.
func writeTour(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("# Tour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestResolveTourProtectsCuration is the rule that makes `tds run` safe to
// repeat. `tds draft` overwrites the tour, and the tour is the one artifact a
// human curates — so run must notice an existing one and leave it alone.
func TestResolveTourProtectsCuration(t *testing.T) {
	mapDir := filepath.Join(t.TempDir(), ".tds")
	want := writeTour(t, mapDir, "demo.tour.md")

	got, exists, err := resolveTour("", mapDir)
	if err != nil {
		t.Fatalf("resolveTour: %v", err)
	}
	if !exists {
		t.Error("an existing tour was not detected; a re-run would overwrite it")
	}
	if got != want {
		t.Errorf("resolved %q, want %q", got, want)
	}
}

// TestResolveTourOnEmptyRepoDefersNaming covers the first run. There is nothing
// to protect, and run leaves the path empty so the drafter picks its own name
// rather than run second-guessing it.
func TestResolveTourOnEmptyRepoDefersNaming(t *testing.T) {
	mapDir := filepath.Join(t.TempDir(), ".tds")
	if err := os.MkdirAll(mapDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, exists, err := resolveTour("", mapDir)
	if err != nil {
		t.Fatalf("resolveTour: %v", err)
	}
	if exists {
		t.Error("reported an existing tour in an empty map dir")
	}
	if got != "" {
		t.Errorf("path = %q, want empty so the drafter names it", got)
	}
}

// TestResolveTourRefusesAmbiguity: guessing which of several tours to build
// would silently render the wrong one, and — worse — a wrong guess combined
// with --redraft would overwrite a tour the user never meant to touch.
func TestResolveTourRefusesAmbiguity(t *testing.T) {
	mapDir := filepath.Join(t.TempDir(), ".tds")
	writeTour(t, mapDir, "one.tour.md")
	writeTour(t, mapDir, "two.tour.md")

	_, _, err := resolveTour("", mapDir)
	if err == nil {
		t.Fatal("two tours should be an error, not a guess")
	}
	if !strings.Contains(err.Error(), "--tour") {
		t.Errorf("the error should say how to resolve it, got %q", err)
	}
}

// TestResolveTourHonoursExplicitPath — --tour wins, and reports correctly
// whether that file is there yet.
func TestResolveTourHonoursExplicitPath(t *testing.T) {
	dir := t.TempDir()
	mapDir := filepath.Join(dir, ".tds")
	// A tour in the map dir that must NOT be chosen over the explicit one.
	writeTour(t, mapDir, "ignored.tour.md")
	explicit := writeTour(t, dir, "chosen.tour.md")

	got, exists, err := resolveTour(explicit, mapDir)
	if err != nil {
		t.Fatalf("resolveTour: %v", err)
	}
	if got != explicit || !exists {
		t.Errorf("resolveTour(%q) = (%q, %v), want (%q, true)", explicit, got, exists, explicit)
	}

	missing := filepath.Join(dir, "not-there.tour.md")
	got, exists, err = resolveTour(missing, mapDir)
	if err != nil {
		t.Fatalf("resolveTour: %v", err)
	}
	if got != missing || exists {
		t.Errorf("resolveTour(%q) = (%q, %v), want (%q, false)", missing, got, exists, missing)
	}
}

// gitRepo makes a repository with one commit and returns its root and SHA.
func gitRepo(t *testing.T) (root, sha string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=T", "commit", "-qm", "initial"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return root, strings.TrimSpace(string(out))
}

// writeTourAt writes a minimal parseable tour pinned to a commit.
func writeTourAt(t *testing.T, path, commit string) {
	t.Helper()
	body := "---\ntitle: \"A tour\"\ntemplate: onboarding\ncommit: " + commit + "\n---\n\n# Chapter: One\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTourIsStaleFollowsTheCommit is the rule that keeps a re-run honest for
// anyone relying on narration rather than hand-curation: prose is anchored to
// code, so a tour pinned to an older commit describes code that has moved.
func TestTourIsStaleFollowsTheCommit(t *testing.T) {
	root, head := gitRepo(t)
	tourPath := filepath.Join(root, ".tds", "demo.tour.md")

	writeTourAt(t, tourPath, head)
	if stale, why := tourIsStale(root, tourPath, true); stale {
		t.Errorf("a tour pinned to HEAD should be reused, got stale: %s", why)
	}

	const older = "7cc8af70ecc890599addc2c54b235c9c1eee3c51"
	writeTourAt(t, tourPath, older)
	stale, why := tourIsStale(root, tourPath, true)
	if !stale {
		t.Fatal("a tour pinned to another commit should be stale")
	}
	// The message has to name both commits, or the user cannot tell what moved.
	if !strings.Contains(why, older[:12]) || !strings.Contains(why, head[:12]) {
		t.Errorf("staleness message should name both commits, got %q", why)
	}
}

// TestTourIsStaleIsConservative: re-drafting spends tokens and discards the
// file, so anything undeterminable must NOT trigger it.
func TestTourIsStaleIsConservative(t *testing.T) {
	root, head := gitRepo(t)
	tourPath := filepath.Join(root, ".tds", "demo.tour.md")

	// No tour at all — nothing to be stale about.
	if stale, _ := tourIsStale(root, tourPath, false); stale {
		t.Error("a missing tour should not report as stale")
	}

	// A tour recording no commit cannot be compared.
	writeTourAt(t, tourPath, "")
	if stale, _ := tourIsStale(root, tourPath, true); stale {
		t.Error("a tour with no commit recorded should not report as stale")
	}

	// Not a git repository: no HEAD to compare against.
	nogit := t.TempDir()
	other := filepath.Join(nogit, ".tds", "demo.tour.md")
	writeTourAt(t, other, head)
	if stale, _ := tourIsStale(nogit, other, true); stale {
		t.Error("a non-git directory should not report a stale tour")
	}

	// Unparseable file.
	bad := filepath.Join(root, ".tds", "bad.tour.md")
	if err := os.WriteFile(bad, []byte("\x00not a tour"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stale, _ := tourIsStale(root, bad, true); stale {
		t.Error("an unparseable tour should not report as stale")
	}
}

// TestRunCommandIsRegistered guards the on-ramp actually being reachable, and
// that its flags are spelled the way the docs say.
func TestRunCommandIsRegistered(t *testing.T) {
	run, _, err := newRootCmd().Find([]string{"run"})
	if err != nil || run.Name() != "run" {
		t.Fatalf("`tds run` is not registered on the root command (%v)", err)
	}
	for _, flag := range []string{"no-narrate", "redraft", "keep-tour", "serve", "port", "narrate-files", "tour"} {
		if run.Flags().Lookup(flag) == nil {
			t.Errorf("`tds run` is missing the --%s flag", flag)
		}
	}
}
