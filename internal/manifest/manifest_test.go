package manifest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/anchor"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/tour"
)

const sampleTour = `---
title: "Test tour"
template: onboarding
audience: "devs"
commit: abc123
maintainer: charris
---

Tour intro with **bold**.

# Chapter: One

Chapter intro prose.

::stop{anchor="a.rb::Invoice#finalize" focus="def finalize"}
Look at ` + "`finalize`" + ` here.
::detour{title="Aside"}
A side note.
::stop{anchor="a.rb::Invoice.overdue"}
Nested stop.
::
::
::

# Chapter: Two

::stop{anchor="a.rb::Invoice#missing"}
This anchor will not resolve.
::
`

func sampleResolver() *anchor.Resolver {
	return anchor.NewResolver([]protocol.Symbol{
		{Path: "a.rb", Symbol: "Invoice#finalize", Kind: "method", StartLine: 6, EndLine: 9},
		{Path: "a.rb", Symbol: "Invoice.overdue", Kind: "method", StartLine: 15, EndLine: 17},
	})
}

func compileSample(t *testing.T) *Manifest {
	t.Helper()
	parsed, err := tour.Parse([]byte(sampleTour))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m, err := Compile(parsed, sampleResolver())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return m
}

func TestCompileMetadataAndStructure(t *testing.T) {
	m := compileSample(t)

	if m.Version != Version {
		t.Errorf("version = %d, want %d", m.Version, Version)
	}
	if m.Title != "Test tour" || m.Template != "onboarding" || m.Audience != "devs" {
		t.Errorf("metadata wrong: %+v", m)
	}
	if m.Meta["maintainer"] != "charris" {
		t.Errorf("meta passthrough missing: %v", m.Meta)
	}
	if len(m.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(m.Chapters))
	}
	if !strings.Contains(m.Intro, "<strong>bold</strong>") {
		t.Errorf("tour intro not rendered to HTML: %q", m.Intro)
	}
}

func TestCompileResolvesAnchors(t *testing.T) {
	m := compileSample(t)

	stop := m.Chapters[0].Stops[0]
	if !stop.Anchor.Resolved || stop.Anchor.Kind != "symbol" {
		t.Fatalf("stop anchor not resolved: %+v", stop.Anchor)
	}
	if stop.Anchor.Path != "a.rb" || stop.Anchor.StartLine != 6 || stop.Anchor.EndLine != 9 {
		t.Errorf("anchor range wrong: %+v", stop.Anchor)
	}
	if stop.Anchor.Symbol != "Invoice#finalize" {
		t.Errorf("resolved symbol = %q", stop.Anchor.Symbol)
	}
	if stop.Focus != "def finalize" {
		t.Errorf("focus = %q, want %q", stop.Focus, "def finalize")
	}
	if stop.ID == "" {
		t.Error("stop missing an id for deep-linking")
	}
	if !strings.Contains(stop.Prose, "<code>finalize</code>") {
		t.Errorf("stop prose not rendered: %q", stop.Prose)
	}
}

func TestCompileDetour(t *testing.T) {
	m := compileSample(t)

	detours := m.Chapters[0].Stops[0].Detours
	if len(detours) != 1 || detours[0].Title != "Aside" {
		t.Fatalf("detour wrong: %+v", detours)
	}
	nested := detours[0].Stops
	if len(nested) != 1 || nested[0].Anchor.Symbol != "Invoice.overdue" || nested[0].Anchor.StartLine != 15 {
		t.Fatalf("nested detour stop wrong: %+v", nested)
	}
	if nested[0].ID == m.Chapters[0].Stops[0].ID {
		t.Error("nested stop id should differ from its parent")
	}
}

func TestCompileFlagsUnresolved(t *testing.T) {
	m := compileSample(t)

	bad := m.Chapters[1].Stops[0].Anchor
	if bad.Resolved || bad.Kind != "unresolved" || bad.Reason == "" {
		t.Fatalf("expected unresolved+flagged anchor, got %+v", bad)
	}
	if len(m.Warnings) == 0 {
		t.Fatal("expected a warning for the unresolved anchor")
	}
	joined := strings.Join(m.Warnings, "\n")
	if !strings.Contains(joined, "Invoice#missing") {
		t.Errorf("warning should name the missing symbol: %q", joined)
	}
}

func TestWriteJSONIsValid(t *testing.T) {
	m := compileSample(t)
	var buf bytes.Buffer
	if err := m.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal(buf.Bytes(), &round); err != nil {
		t.Fatalf("manifest json invalid: %v", err)
	}
	if round["title"] != "Test tour" {
		t.Errorf("round-trip title = %v", round["title"])
	}
	// HTML prose must survive literally, not <-escaped (SetEscapeHTML(false)).
	if !strings.Contains(buf.String(), `<strong>`) {
		t.Errorf("HTML prose was escaped in JSON output:\n%s", buf.String())
	}
}
