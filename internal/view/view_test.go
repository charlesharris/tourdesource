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

func fv(path string, v float64) protocol.Finding {
	return protocol.Finding{Tool: "flog", View: protocol.ViewHeatmap, Severity: "info", Path: path, Value: &v}
}

// TestHeatmapAggregatesPerFile covers TDS-29: flog emits a score per method —
// 1,390 of them on Redmine — and a flat list of that is a wall, not a map.
func TestHeatmapAggregatesPerFile(t *testing.T) {
	views := Build([]protocol.Finding{
		fv("a.rb", 10), fv("a.rb", 90), fv("a.rb", 30),
		fv("b.rb", 45),
	}, "")
	if len(views) != 1 {
		t.Fatalf("want one heatmap view, got %d", len(views))
	}
	files := views[0].Files
	if len(files) != 2 {
		t.Fatalf("want one row per file, got %d", len(files))
	}
	if files[0].Path != "a.rb" || files[0].Peak != 90 || files[0].Entries != 3 {
		t.Errorf("row = %+v, want a.rb ranked by its worst score with all 3 entries", files[0])
	}
	// Bars are relative to the view's own ceiling: flog scores and coverage
	// percentages share no absolute scale.
	if files[0].Pct != 100 {
		t.Errorf("the top row should fill the bar, got %d%%", files[0].Pct)
	}
	if files[1].Pct != 50 {
		t.Errorf("b.rb at 45 of 90 should be 50%%, got %d%%", files[1].Pct)
	}
}

// TestHeatmapOnlyForHeatmapKind — a panel is a list of defects and must not be
// silently reduced to one row per file.
func TestHeatmapOnlyForHeatmapKind(t *testing.T) {
	views := Build([]protocol.Finding{
		{Tool: "brakeman", View: protocol.ViewPanel, Severity: "error", Path: "a.rb", StartLine: 1},
	}, "")
	if views[0].Files != nil {
		t.Errorf("a panel should not carry heatmap rows, got %+v", views[0].Files)
	}
}

func TestHeatmapCapsRows(t *testing.T) {
	var fs []protocol.Finding
	for i := 0; i < maxHeatFiles+25; i++ {
		fs = append(fs, fv(string(rune('a'+i%26))+string(rune('a'+i/26))+".rb", float64(i+1)))
	}
	views := Build(fs, "")
	if got := len(views[0].Files); got != maxHeatFiles {
		t.Errorf("heat rows = %d, want the cap of %d", got, maxHeatFiles)
	}
}
