package view

import (
	"testing"

	"github.com/charlesharris/tourdesource/internal/protocol"
)

func f(tool, kind, sev, path string, line int) protocol.Finding {
	return protocol.Finding{
		Tool: tool, ToolVersion: "1.0", View: kind, Severity: sev,
		Path: path, StartLine: line, Rule: "R", Message: "m",
	}
}

// TestBuildGroupsByToolAndKind — a tool can emit more than one kind (brakeman
// contributes both annotations and a panel), so the pair is the identity.
func TestBuildGroupsByToolAndKind(t *testing.T) {
	views := Build([]protocol.Finding{
		f("brakeman", protocol.ViewPanel, "error", "a.rb", 1),
		f("brakeman", protocol.ViewAnnotations, "warning", "b.rb", 2),
		f("rubocop", protocol.ViewAnnotations, "info", "c.rb", 3),
	}, "abc123")

	if len(views) != 3 {
		t.Fatalf("want one view per (tool, kind), got %d: %+v", len(views), views)
	}
	ids := map[string]bool{}
	for _, v := range views {
		ids[v.ID] = true
	}
	for _, want := range []string{"brakeman-panel", "brakeman-annotations", "rubocop-annotations"} {
		if !ids[want] {
			t.Errorf("missing view %q; have %v", want, ids)
		}
	}
}

// TestBuildOrdersBySeverity — the switcher's default order should match what
// matters, not what happens to sort first alphabetically.
func TestBuildOrdersBySeverity(t *testing.T) {
	views := Build([]protocol.Finding{
		f("aaa", protocol.ViewAnnotations, "info", "a.rb", 1),
		f("aaa", protocol.ViewAnnotations, "info", "a.rb", 2),
		f("zzz", protocol.ViewPanel, "error", "b.rb", 1),
	}, "")
	if views[0].Provenance.Tool != "zzz" {
		t.Errorf("the view with an error should lead, got %q", views[0].Provenance.Tool)
	}
}

// TestProvenanceIsCarried — a finding without attribution is an anonymous
// accusation, so every view must name its tool, version and commit.
func TestProvenanceIsCarried(t *testing.T) {
	views := Build([]protocol.Finding{f("brakeman", protocol.ViewPanel, "error", "a.rb", 1)}, "deadbeef")
	p := views[0].Provenance
	if p.Tool != "brakeman" || p.ToolVersion != "1.0" || p.Commit != "deadbeef" {
		t.Errorf("provenance = %+v, want tool, version and commit", p)
	}
}

func TestCountsTallySeverity(t *testing.T) {
	views := Build([]protocol.Finding{
		f("t", protocol.ViewPanel, "error", "a.rb", 1),
		f("t", protocol.ViewPanel, "warning", "a.rb", 2),
		f("t", protocol.ViewPanel, "info", "a.rb", 3),
		f("t", protocol.ViewPanel, "convention", "a.rb", 4), // unknown -> info
	}, "")
	c := views[0].Counts
	if c.Total != 4 || c.Errors != 1 || c.Warnings != 1 || c.Info != 2 {
		t.Errorf("counts = %+v, want 4/1/1/2", c)
	}
}

// TestUnknownKindFallsBackToPanel — a provider that omits or mistypes the view
// kind still has something worth showing.
func TestUnknownKindFallsBackToPanel(t *testing.T) {
	views := Build([]protocol.Finding{f("t", "", "info", "a.rb", 1)}, "")
	if len(views) != 1 || views[0].Kind != protocol.ViewPanel {
		t.Errorf("unknown kind should become a panel, got %+v", views)
	}
}

// TestNoFindingsProducesNoViews — an empty layer in a switcher is a promise the
// data does not keep.
func TestNoFindingsProducesNoViews(t *testing.T) {
	if got := Build(nil, "abc"); got != nil {
		t.Errorf("no findings should produce no views, got %+v", got)
	}
}

func TestByPathAndBySymbol(t *testing.T) {
	views := Build([]protocol.Finding{
		{Tool: "t", View: protocol.ViewPanel, Path: "a.rb", StartLine: 9, Symbol: "A#x"},
		{Tool: "t", View: protocol.ViewPanel, Path: "a.rb", StartLine: 2, Symbol: "A#y"},
		{Tool: "t", View: protocol.ViewPanel, Path: "b.rb", StartLine: 1}, // unattributed
	}, "")

	byPath := ByPath(views)
	if len(byPath["a.rb"]) != 2 {
		t.Fatalf("a.rb should carry 2 findings, got %d", len(byPath["a.rb"]))
	}
	if byPath["a.rb"][0].StartLine != 2 {
		t.Error("findings on a page should read in line order")
	}

	bySym := BySymbol(views)
	if len(bySym) != 2 {
		t.Errorf("only attributed findings get a badge, got %v", bySym)
	}
	if _, ok := bySym[""]; ok {
		t.Error("an unattributed finding must not become an empty-symbol badge")
	}
}
