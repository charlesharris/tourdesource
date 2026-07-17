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
