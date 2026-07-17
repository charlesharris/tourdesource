package builder

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/mapper"
)

// TestBuildEndToEnd exercises the whole pipeline on a real temp git repo: map a
// mini Rails app, then build a tour of it and assert the bundle is a
// self-contained, openable page with the tour's code embedded.
func TestBuildEndToEnd(t *testing.T) {
	for _, bin := range []string{"git", "ruby"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	if err := exec.Command("ruby", "-e", "require 'prism'").Run(); err != nil {
		t.Skip("ruby prism not available")
	}

	root := t.TempDir()
	write(t, root, "app/models/invoice.rb",
		"class Invoice < ApplicationRecord\n  def finalize\n    save!\n  end\nend\n")
	write(t, root, "tour/onboarding.tour.md", `---
title: "Billing tour"
audience: "new devs"
---

# Chapter: The model

::stop{anchor="app/models/invoice.rb::Invoice#finalize"}
This is where an invoice is finalized.
::
`)
	initRepo(t, root)

	rubyExe, err := filepath.Abs(filepath.Join("..", "..", "providers", "ruby", "exe", "tds-provider-ruby"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TDS_PROVIDER_RUBY", rubyExe)

	// map first
	if _, err := mapper.Build(context.Background(), mapper.Options{Root: root}); err != nil {
		t.Fatalf("map: %v", err)
	}

	// then build
	res, err := Build(context.Background(), Options{
		TourPath: filepath.Join(root, "tour", "onboarding.tour.md"),
		Repo:     root,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if res.Stops != 1 {
		t.Errorf("stops = %d, want 1", res.Stops)
	}
	if res.CodeFiles != 1 {
		t.Errorf("code files = %d, want 1 (Invoice#finalize's file)", res.CodeFiles)
	}
	if res.EmbedFiles == 0 {
		t.Error("expected pinned repo blobs to be embedded")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings (anchor should resolve): %v", res.Warnings)
	}
	if res.Commit == "" {
		t.Error("bundle should be pinned to a commit")
	}

	// index.html: self-contained + carries the tour and its highlighted code.
	index, err := os.ReadFile(res.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	page := string(index)
	for _, want := range []string{"<!doctype html>", "Billing tour", "Invoice#finalize", "chroma", "finalized"} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html missing %q", want)
		}
	}

	// manifest.json exists and is valid.
	mdata, err := os.ReadFile(filepath.Join(res.BundleDir, "manifest.json"))
	if err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	var mdoc map[string]any
	if err := json.Unmarshal(mdata, &mdoc); err != nil {
		t.Fatalf("manifest.json invalid: %v", err)
	}

	// pinned repo blobs written.
	if _, err := os.Stat(filepath.Join(res.BundleDir, "repo", "files", "app", "models", "invoice.rb")); err != nil {
		t.Errorf("pinned blob missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.BundleDir, "repo", "index.json")); err != nil {
		t.Errorf("repo index.json missing: %v", err)
	}
}

func TestBuildMissingMap(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.tour.md", "# Chapter: X\n\n::stop{anchor=\"a.rb:1-2\"}\nhi\n::\n")
	_, err := Build(context.Background(), Options{TourPath: filepath.Join(root, "x.tour.md"), Repo: root})
	if err == nil || !strings.Contains(err.Error(), "map not found") {
		t.Fatalf("expected a 'map not found' error, got %v", err)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initRepo(t *testing.T, root string) {
	t.Helper()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_AUTHOR_DATE=2024-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-01T00:00:00Z",
	)
	for _, args := range [][]string{
		{"-c", "init.defaultBranch=main", "init"},
		{"add", "-A"},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}
