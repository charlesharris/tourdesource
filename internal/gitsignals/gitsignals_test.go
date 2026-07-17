package gitsignals

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// commit writes content to path under root and commits it with a controlled
// author and fixed date, so history is fully deterministic.
func commit(t *testing.T, root, path, content, author, email, date string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, nil, "add", path)
	env := []string{
		"GIT_AUTHOR_NAME=" + author,
		"GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + author,
		"GIT_COMMITTER_EMAIL=" + email,
		"GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_DATE=" + date,
	}
	run(t, root, env, "commit", "-m", "touch "+path)
}

// run executes a git subcommand in root, appending extra to the environment.
func run(t *testing.T, root string, extra []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), extra...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestCompute(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := t.TempDir()
	run(t, root, nil, "-c", "init.defaultBranch=main", "init")

	const (
		aliceEmail = "alice@example.com"
		bobEmail   = "bob@example.com"
		date1      = "2024-01-01T00:00:00Z"
		date2      = "2024-03-15T00:00:00Z"
	)

	// foo.rb: 3 commits (2 Alice, 1 Bob) across two dates.
	commit(t, root, "foo.rb", "v1", "Alice", aliceEmail, date1)
	commit(t, root, "foo.rb", "v2", "Bob", bobEmail, date1)
	commit(t, root, "foo.rb", "v3", "Alice", aliceEmail, date2)
	// bar.rb: 1 commit.
	commit(t, root, "bar.rb", "b1", "Bob", bobEmail, date2)

	sigs, err := Compute(root, nil)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	foo, ok := sigs["foo.rb"]
	if !ok {
		t.Fatalf("no signal for foo.rb; got %v", sigs)
	}
	if foo.Churn != 3 {
		t.Errorf("foo.rb churn = %d, want 3", foo.Churn)
	}
	if !foo.FirstCommit.Equal(mustTime(t, date1)) {
		t.Errorf("foo.rb FirstCommit = %v, want %s", foo.FirstCommit, date1)
	}
	if !foo.LastCommit.Equal(mustTime(t, date2)) {
		t.Errorf("foo.rb LastCommit = %v, want %s", foo.LastCommit, date2)
	}
	if want := []string{"Alice", "Bob"}; !reflect.DeepEqual(foo.Authors, want) {
		t.Errorf("foo.rb Authors = %v, want %v", foo.Authors, want)
	}
	if foo.AgeDays <= 0 {
		t.Errorf("foo.rb AgeDays = %d, want positive", foo.AgeDays)
	}

	bar, ok := sigs["bar.rb"]
	if !ok {
		t.Fatalf("no signal for bar.rb; got %v", sigs)
	}
	if bar.Churn != 1 {
		t.Errorf("bar.rb churn = %d, want 1", bar.Churn)
	}

	// Filtering to a subset returns only the requested path.
	only, err := Compute(root, []string{"foo.rb"})
	if err != nil {
		t.Fatalf("Compute(filter): %v", err)
	}
	if len(only) != 1 {
		t.Fatalf("filtered result = %v, want 1 entry", only)
	}
	if _, ok := only["foo.rb"]; !ok {
		t.Errorf("filtered result missing foo.rb: %v", only)
	}
}

func TestComputeNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, err := Compute(t.TempDir(), nil); err == nil {
		t.Fatal("expected error for non-repo directory")
	}
}
