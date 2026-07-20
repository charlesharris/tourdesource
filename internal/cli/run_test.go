package cli

import (
	"os"
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

// TestRunCommandIsRegistered guards the on-ramp actually being reachable, and
// that its safety flag is spelled the way the docs say.
func TestRunCommandIsRegistered(t *testing.T) {
	run, _, err := newRootCmd().Find([]string{"run"})
	if err != nil || run.Name() != "run" {
		t.Fatalf("`tds run` is not registered on the root command (%v)", err)
	}
	for _, flag := range []string{"no-narrate", "redraft", "serve", "port", "narrate-files", "tour"} {
		if run.Flags().Lookup(flag) == nil {
			t.Errorf("`tds run` is missing the --%s flag", flag)
		}
	}
}
