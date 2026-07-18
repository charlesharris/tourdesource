package viewer

import (
	"os"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/anchor"
	"github.com/charlesharris/tourdesource/internal/highlight"
	"github.com/charlesharris/tourdesource/internal/manifest"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/tour"
)

func TestAssetsEmbedded(t *testing.T) {
	if strings.TrimSpace(viewerCSS) == "" {
		t.Error("viewer.css not embedded")
	}
	if !strings.Contains(viewerJS, "tds-data") {
		t.Error("viewer.js not embedded (missing expected content)")
	}
	assets := Assets()
	if len(assets["viewer.css"]) == 0 || len(assets["viewer.js"]) == 0 {
		t.Error("Assets() returned empty files")
	}
}

const demoTourSrc = `---
title: "A tiny tour"
audience: "new engineers"
commit: 0123456789abcdef
---

Welcome to the **billing** service.

# Chapter: The aggregate root

::stop{anchor="app/models/invoice.rb::Invoice#finalize" focus="def finalize"}
` + "`finalize`" + ` is the whole domain in a few lines.
::detour{title="Debugging a stuck invoice"}
Look at the lock.
::stop{anchor="app/models/invoice.rb::Invoice.overdue"}
The scope used by the nightly job.
::
::
::
`

const demoRubySrc = `class Invoice < ApplicationRecord
  belongs_to :account

  def finalize
    raise "already finalized" if finalized?
    with_lock { update!(status: :finalized) }
  end

  def finalized?
    status == "finalized"
  end

  def self.overdue
    where("due_on < ?", Date.today)
  end
end
`

// buildDemo runs the full M2 chain (parse -> compile -> highlight -> render).
func buildDemo(t *testing.T) []byte {
	t.Helper()
	parsed, err := tour.Parse([]byte(demoTourSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	resolver := anchor.NewResolver([]protocol.Symbol{
		{Path: "app/models/invoice.rb", Symbol: "Invoice#finalize", Kind: "method", StartLine: 4, EndLine: 7},
		{Path: "app/models/invoice.rb", Symbol: "Invoice.overdue", Kind: "method", StartLine: 13, EndLine: 15},
	})
	m, err := manifest.Compile(parsed, resolver)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	hl, err := highlight.Highlight(demoRubySrc, "ruby")
	if err != nil {
		t.Fatalf("highlight: %v", err)
	}
	out, err := Render(Input{
		Manifest:     m,
		Code:         map[string]string{"app/models/invoice.rb": hl.HTML},
		HighlightCSS: highlight.StylesheetCSS(),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func TestRenderSelfContainedPage(t *testing.T) {
	page := string(buildDemo(t))

	for _, want := range []string{
		"<!doctype html>",
		"<title>A tiny tour</title>",
		`id="tds-data"`,
		"Invoice#finalize", // resolved anchor / location label in the data
		"billing",          // tour intro prose
		"chroma",           // embedded highlighted code
		"new engineers",    // audience
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	// The highlighter CSS and viewer JS must be inlined (self-contained).
	if !strings.Contains(page, ".chroma") {
		t.Error("highlighter CSS not inlined")
	}
	if !strings.Contains(page, "addEventListener") {
		t.Error("viewer JS not inlined")
	}
	// No external references (CSP-safe / offline).
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") || strings.Contains(page, "src=") {
		t.Error("page should have no external references")
	}
}

func TestRenderNilManifest(t *testing.T) {
	if _, err := Render(Input{}); err == nil {
		t.Error("expected an error for a nil manifest")
	}
}

// TestGenerateDemo writes a full demo bundle to $TDS_VIEWER_DEMO_OUT when set,
// for manual/visual inspection (e.g. publishing as an artifact). Skipped in CI.
func TestGenerateDemo(t *testing.T) {
	out := os.Getenv("TDS_VIEWER_DEMO_OUT")
	if out == "" {
		t.Skip("set TDS_VIEWER_DEMO_OUT to write a demo bundle")
	}
	if err := os.WriteFile(out, buildDemo(t), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote demo bundle to %s", out)
}

// TestStaticRenderingIsReadableWithoutJS covers the JS-off half of TDS-21. The
// prose is the tour; a reader with scripting disabled should get the narrative,
// not an empty <div>.
func TestStaticRenderingIsReadableWithoutJS(t *testing.T) {
	page := string(buildDemo(t))

	// The app root must carry real content before any script runs.
	start := strings.Index(page, `<div id="tds-app">`)
	end := strings.Index(page, `<script type="application/json"`)
	if start < 0 || end < 0 || end < start {
		t.Fatal("could not locate the app root ahead of the data script")
	}
	static := page[start:end]

	for _, want := range []string{
		"Welcome to the",                // tour intro
		"The aggregate root",            // chapter title
		"is the whole domain",           // stop prose
		"Invoice#finalize",              // resolved anchor label
		"Debugging a stuck invoice",     // detour title
		"The scope used by the nightly", // nested detour stop prose
	} {
		if !strings.Contains(static, want) {
			t.Errorf("static rendering is missing %q", want)
		}
	}
}

// TestOutlineListsChapters covers the outline half of TDS-21: without it a
// whole-project tour is one long scroll with no way to see what it covers.
func TestOutlineListsChapters(t *testing.T) {
	m := &manifest.Manifest{
		Title: "Whole project",
		Chapters: []manifest.Chapter{
			{
				Title: "Authorization",
				Stops: []manifest.Stop{
					{ID: "s1", Prose: "one"},
					{ID: "s2", Prose: "two", Detours: []manifest.Detour{
						// Nested stops count toward the chapter total: the outline
						// should reflect how much there is to read.
						{Title: "aside", Stops: []manifest.Stop{{ID: "s3", Prose: "three"}}},
					}},
				},
			},
			{Title: "Rendering", Stops: []manifest.Stop{{ID: "s4", Prose: "four"}}},
		},
	}
	out, err := Render(Input{Manifest: m})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	page := string(out)

	if !strings.Contains(page, `class="tds-toc"`) {
		t.Error("no outline rendered")
	}
	if !strings.Contains(page, `href="#chapter-1"`) || !strings.Contains(page, `href="#chapter-2"`) {
		t.Error("outline must link every chapter by fragment")
	}
	if !strings.Contains(page, "3 stops") {
		t.Error("chapter 1 should count its detour stop: want '3 stops'")
	}
	if !strings.Contains(page, "1 stop") {
		t.Error("a single-stop chapter should read '1 stop', not '1 stops'")
	}

	// The fragments the outline links to must exist as ids in the same document,
	// so the links work with JavaScript disabled.
	for _, id := range []string{`id="chapter-1"`, `id="chapter-2"`, `id="stop-s1"`, `id="stop-s3"`} {
		if !strings.Contains(page, id) {
			t.Errorf("missing anchor target %s", id)
		}
	}
}

// TestOutlineEscapesChapterTitles keeps an author-supplied title from breaking
// out of the outline markup.
func TestOutlineEscapesChapterTitles(t *testing.T) {
	m := &manifest.Manifest{
		Title: "x",
		Chapters: []manifest.Chapter{{
			Title: `Auth <script>alert(1)</script> & "quotes"`,
			Stops: []manifest.Stop{{ID: "s1", Prose: "p"}},
		}},
	}
	out, err := Render(Input{Manifest: m})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(out), "<script>alert(1)</script>") {
		t.Error("chapter title was not escaped")
	}
	if !strings.Contains(string(out), "&amp;") {
		t.Error("expected the ampersand to be escaped")
	}
}

// TestViewerScriptSupportsDeepLinks is TDS-21's acceptance criterion. The script
// is inlined rather than executed here, so this asserts the wiring is present
// and consistent with the fragments the static rendering emits.
func TestViewerScriptSupportsDeepLinks(t *testing.T) {
	js := string(Assets()["viewer.js"])
	for _, want := range []string{
		"hashchange",   // reacts to fragment changes
		"replaceState", // updates the URL without flooding history
		"applyHash",    // restores state from the URL on load
		"stop-",        // the stop fragment scheme
		"chapter-",     // the chapter fragment scheme
	} {
		if !strings.Contains(js, want) {
			t.Errorf("viewer.js is missing deep-link support: %q", want)
		}
	}
}
