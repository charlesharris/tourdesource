package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

// newCacheStore is a bare store for the cache primitives — analyzer_test.go's
// openStore expects a mapped repo, which these do not need.
func newCacheStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "map.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestHashFilesChangesWithContent(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.rb")
	if err := os.WriteFile(p, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := hashFiles(root, []string{"a.rb"})["a.rb"]
	if first == "" {
		t.Fatal("no hash for a readable file")
	}
	if err := os.WriteFile(p, []byte("x = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if second := hashFiles(root, []string{"a.rb"})["a.rb"]; second == first {
		t.Error("content change must change the hash")
	}
	// An unreadable file gets no hash, which makes it a permanent miss — the
	// safe direction.
	if _, ok := hashFiles(root, []string{"gone.rb"})["gone.rb"]; ok {
		t.Error("a missing file should not get a hash")
	}
}

// TestCacheRoundTrip — a hit with no findings is still a hit, or every clean
// file is re-analyzed forever, and clean files are most of them.
func TestCacheRoundTrip(t *testing.T) {
	st := newCacheStore(t)
	fs := []protocol.Finding{{Path: "a.rb", StartLine: 3, Tool: "rubocop", Rule: "R"}}

	if _, ok := st.CachedFindings("rubocop", "1.0", "a.rb", "h1"); ok {
		t.Error("empty cache should miss")
	}
	if err := st.PutCachedFindings("rubocop", "1.0", "a.rb", "h1", fs); err != nil {
		t.Fatal(err)
	}
	got, ok := st.CachedFindings("rubocop", "1.0", "a.rb", "h1")
	if !ok || len(got) != 1 || got[0].Rule != "R" {
		t.Fatalf("round trip = %+v, %v", got, ok)
	}
	// A different hash, tool or version is a different key.
	if _, ok := st.CachedFindings("rubocop", "1.0", "a.rb", "h2"); ok {
		t.Error("a changed file must miss")
	}
	if _, ok := st.CachedFindings("rubocop", "2.0", "a.rb", "h1"); ok {
		t.Error("a new tool version must miss — its verdict may differ")
	}
	if _, ok := st.CachedFindings("flog", "1.0", "a.rb", "h1"); ok {
		t.Error("another tool must not read this tool's entry")
	}

	if err := st.PutCachedFindings("rubocop", "1.0", "clean.rb", "h9", nil); err != nil {
		t.Fatal(err)
	}
	got, ok = st.CachedFindings("rubocop", "1.0", "clean.rb", "h9")
	if !ok || len(got) != 0 {
		t.Errorf("a clean file must cache as a hit with no findings, got %+v %v", got, ok)
	}
}

// TestRecordCacheKeepsAnalyzersApart covers the bug that made the second run
// double its findings: one provider call can carry several analyzers, and
// storing the whole batch under each one's key makes every cached file return
// the union.
func TestRecordCacheKeepsAnalyzersApart(t *testing.T) {
	st := newCacheStore(t)
	hashes := map[string]string{"a.rb": "h1"}
	pl := plan{
		info:  protocol.AnalyzerInfo{Name: "rubocop", Tool: "rubocop", ToolVersion: "1.0", Incremental: true},
		stale: []string{"a.rb"},
	}
	// A single call's results, carrying two tools.
	batch := []protocol.Finding{
		{Path: "a.rb", StartLine: 1, Tool: "rubocop", Rule: "cop"},
		{Path: "a.rb", StartLine: 2, Tool: "flog", Rule: "complexity"},
		{Path: "a.rb", StartLine: 3, Tool: "brakeman", Rule: "sql"},
	}
	recordCache(st, pl, batch, hashes, func(string, ...any) {})

	got, ok := st.CachedFindings("rubocop", "1.0", "a.rb", "h1")
	if !ok {
		t.Fatal("expected a cache entry")
	}
	if len(got) != 1 || got[0].Tool != "rubocop" {
		t.Errorf("cached %+v, want only rubocop's own finding", got)
	}
}

// TestRecordCacheSkipsNonIncremental — brakeman's verdict on a controller can
// change because a model changed, so caching it per file would serve a stale
// answer wearing a current one's clothes.
func TestRecordCacheSkipsNonIncremental(t *testing.T) {
	st := newCacheStore(t)
	pl := plan{
		info:  protocol.AnalyzerInfo{Name: "brakeman", Tool: "brakeman", ToolVersion: "8.0", Incremental: false},
		stale: []string{"a.rb"},
	}
	recordCache(st, pl, []protocol.Finding{{Path: "a.rb", Tool: "brakeman"}},
		map[string]string{"a.rb": "h1"}, func(string, ...any) {})

	if _, ok := st.CachedFindings("brakeman", "8.0", "a.rb", "h1"); ok {
		t.Error("a whole-program analyzer must never be cached per file")
	}
}

func TestGroupByStaleSharesIdenticalWork(t *testing.T) {
	a := plan{info: protocol.AnalyzerInfo{Name: "a"}, stale: []string{"x.rb", "y.rb"}}
	b := plan{info: protocol.AnalyzerInfo{Name: "b"}, stale: []string{"x.rb", "y.rb"}}
	c := plan{info: protocol.AnalyzerInfo{Name: "c"}, stale: []string{"z.rb"}}
	done := plan{info: protocol.AnalyzerInfo{Name: "d"}} // fully cached

	groups := groupByStale([]plan{a, b, c, done})
	if len(groups) != 2 {
		t.Fatalf("want 2 groups (ab, c), got %d: %+v", len(groups), groups)
	}
	if len(groups[0].plans) != 2 {
		t.Errorf("identical file sets should share one call, got %+v", groups[0].names())
	}
	for _, g := range groups {
		for _, p := range g.plans {
			if p.info.Name == "d" {
				t.Error("an analyzer with nothing stale must not be called")
			}
		}
	}
}
