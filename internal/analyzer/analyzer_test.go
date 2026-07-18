package analyzer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/mapper"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

// TestMain doubles as a helper-process host: with TDS_TEST_PROVIDER set, this
// binary behaves as a protocol-speaking provider instead of running tests, so
// the pipeline is exercised over a real subprocess and real pipes rather than an
// in-process fake.
func TestMain(m *testing.M) {
	if mode := os.Getenv("TDS_TEST_PROVIDER"); mode != "" {
		runHelperProvider(mode)
		return
	}
	os.Exit(m.Run())
}

// helperSymbols is the structure the helper provider reports for a.rb. The
// nesting is what exercises innermost-wins resolution.
var helperSymbols = []protocol.Symbol{
	{Path: "a.rb", Kind: "class", Name: "A", Symbol: "A", StartLine: 1, EndLine: 20},
	{Path: "a.rb", Kind: "method", Name: "big", Symbol: "A#big", StartLine: 2, EndLine: 15},
	{Path: "a.rb", Kind: "method", Name: "small", Symbol: "A#small", StartLine: 5, EndLine: 8},
}

func f64(v float64) *float64 { return &v }

// helperFindings covers each resolution case exactly once.
var helperFindings = []protocol.Finding{
	// Inside all three symbols: the narrowest span must win.
	{Path: "a.rb", StartLine: 6, EndLine: 6, Severity: "warning", Rule: "Style/Foo",
		Message: "nested", Tool: "rubocop", ToolVersion: "1.65.0", View: "annotations"},
	// Inside the class only.
	{Path: "a.rb", StartLine: 18, EndLine: 18, Severity: "error", Rule: "Lint/Bar",
		Message: "class level", Tool: "rubocop", ToolVersion: "1.65.0", View: "annotations"},
	// Outside every symbol: must stay unresolved rather than blame the file.
	{Path: "a.rb", StartLine: 99, EndLine: 99, Severity: "info", Rule: "Lint/Baz",
		Message: "no symbol here", Tool: "rubocop", ToolVersion: "1.65.0", View: "annotations"},
	// Provider already resolved it: the core must not second-guess.
	{Path: "a.rb", StartLine: 6, EndLine: 6, Symbol: "A#provider_knows_best",
		Severity: "info", Rule: "Lint/Qux", Message: "preset", Tool: "rubocop",
		ToolVersion: "1.65.0", View: "annotations"},
	// A metric finding carrying a numeric for heatmap views.
	{Path: "a.rb", StartLine: 5, EndLine: 8, Severity: "info", Rule: "coverage",
		Message: "62% covered", Tool: "simplecov", ToolVersion: "0.22.0",
		View: "heatmap", Value: f64(62)},
}

func runHelperProvider(mode string) {
	dec := protocol.NewDecoder(os.Stdin)
	enc := protocol.NewEncoder(os.Stdout)
	for {
		req, err := dec.DecodeRequest()
		if err != nil {
			return // stdin closed
		}
		switch req.Op {
		case protocol.OpCapabilities:
			ops := []string{"capabilities", "structure", "analyze"}
			if mode == "structureonly" {
				ops = []string{"capabilities", "structure"}
			}
			resp, _ := protocol.NewResponse(req.ID, protocol.Capabilities{
				Protocol: "1.0.0", Provider: "tds-test", ProviderVersion: "0.0.1",
				Languages: []string{"ruby"}, Operations: ops,
				Analyzers: []protocol.AnalyzerInfo{
					{Name: "rubocop", Tool: "rubocop", Available: true,
						ToolVersion: "1.65.0", Views: []string{"annotations"}},
					{Name: "simplecov", Tool: "simplecov", Available: true,
						ToolVersion: "0.22.0", Views: []string{"heatmap"}},
					// Advertised but not installed: must be reported, not run.
					{Name: "brakeman", Tool: "brakeman", Available: false,
						Views: []string{"panel"}},
				},
			})
			_ = enc.Encode(resp)

		case protocol.OpStructure:
			resp, _ := protocol.NewResponse(req.ID, protocol.StructureResult{Symbols: helperSymbols})
			_ = enc.Encode(resp)

		case protocol.OpAnalyze:
			if mode == "analyzefails" {
				_ = enc.Encode(protocol.NewErrorResponse(req.ID, protocol.CodeInternal, "analyzer exploded"))
				continue
			}
			result := protocol.AnalyzeResult{Findings: helperFindings}
			if mode == "partialfailure" {
				result.AnalyzerErrors = []protocol.AnalyzerError{
					{Analyzer: "brakeman", Message: "brakeman is not installed"},
				}
			}
			resp, _ := protocol.NewResponse(req.ID, result)
			_ = enc.Encode(resp)

		default:
			_ = enc.Encode(protocol.NewErrorResponse(req.ID, protocol.CodeUnsupportedOp, "unsupported"))
		}
	}
}

// newMappedRepo builds a repo whose provider is this test binary, runs the map
// pipeline over it, and returns the repo root. Analyze reads the map, so every
// case needs one.
func newMappedRepo(t *testing.T, mode string) string {
	t.Helper()
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "a.rb"),
		[]byte(strings.Repeat("# line\n", 25)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Discover honours explicit [[providers]] entries, which is how the test
	// points the core at this binary instead of a real provider.
	cfg := fmt.Sprintf("[[providers]]\nname = \"ruby\"\ncommand = [%q]\nenv = [\"TDS_TEST_PROVIDER=%s\"]\n",
		os.Args[0], mode)
	if err := os.WriteFile(filepath.Join(root, "tds.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mapper.Build(context.Background(), mapper.Options{Root: root}); err != nil {
		t.Fatalf("building the map: %v", err)
	}
	return root
}

func openStore(t *testing.T, root string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(root, ".tds", "map.sqlite"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// findingBy returns the stored finding for a rule, for per-case assertions.
func findingBy(t *testing.T, findings []protocol.Finding, rule string) protocol.Finding {
	t.Helper()
	for _, f := range findings {
		if f.Rule == rule {
			return f
		}
	}
	t.Fatalf("no finding for rule %q in %+v", rule, findings)
	return protocol.Finding{}
}

// TestFindingsLandInTheStore is TDS-25's acceptance criterion, plus the symbol
// resolution and provenance that make a finding useful once it is there.
func TestFindingsLandInTheStore(t *testing.T) {
	root := newMappedRepo(t, "analyze")

	res, err := Run(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	if res.Findings != len(helperFindings) {
		t.Errorf("Result.Findings = %d, want %d", res.Findings, len(helperFindings))
	}
	if res.Resolved != 4 || res.Unresolved != 1 {
		t.Errorf("resolved/unresolved = %d/%d, want 4/1", res.Resolved, res.Unresolved)
	}

	stored, err := openStore(t, root).Findings()
	if err != nil {
		t.Fatalf("reading findings: %v", err)
	}
	if len(stored) != len(helperFindings) {
		t.Fatalf("stored %d findings, want %d", len(stored), len(helperFindings))
	}

	t.Run("resolves to the innermost containing symbol", func(t *testing.T) {
		// Line 6 sits inside A (1-20), A#big (2-15) and A#small (5-8).
		if got := findingBy(t, stored, "Style/Foo").Symbol; got != "A#small" {
			t.Errorf("symbol = %q, want A#small (the narrowest span)", got)
		}
	})

	t.Run("falls back to the enclosing symbol", func(t *testing.T) {
		if got := findingBy(t, stored, "Lint/Bar").Symbol; got != "A" {
			t.Errorf("symbol = %q, want A", got)
		}
	})

	t.Run("leaves a finding outside every symbol unresolved", func(t *testing.T) {
		if got := findingBy(t, stored, "Lint/Baz").Symbol; got != "" {
			t.Errorf("symbol = %q, want empty: line 99 is in no symbol", got)
		}
	})

	t.Run("keeps a symbol the provider already resolved", func(t *testing.T) {
		// The provider knows its own language better than a line-range lookup does.
		if got := findingBy(t, stored, "Lint/Qux").Symbol; got != "A#provider_knows_best" {
			t.Errorf("symbol = %q, want the provider's own value", got)
		}
	})

	t.Run("records tool provenance", func(t *testing.T) {
		f := findingBy(t, stored, "Style/Foo")
		if f.Tool != "rubocop" || f.ToolVersion != "1.65.0" {
			t.Errorf("provenance = %s/%s, want rubocop/1.65.0", f.Tool, f.ToolVersion)
		}
		// The commit is provenance too, held once at the store level: findings are
		// only valid for the source the map indexed.
		commit, err := openStore(t, root).Meta("commit")
		if err != nil {
			t.Fatalf("reading commit: %v", err)
		}
		if commit != res.Commit {
			t.Errorf("store commit %q != result commit %q", commit, res.Commit)
		}
	})

	t.Run("preserves a metric value", func(t *testing.T) {
		f := findingBy(t, stored, "coverage")
		if f.Value == nil || *f.Value != 62 {
			t.Errorf("value = %v, want 62", f.Value)
		}
		if f.View != "heatmap" {
			t.Errorf("view = %q, want heatmap", f.View)
		}
	})

	t.Run("reports unavailable analyzers without running them", func(t *testing.T) {
		byName := map[string]AnalyzerRun{}
		for _, a := range res.Analyzers {
			byName[a.Name] = a
		}
		brakeman, ok := byName["brakeman"]
		if !ok {
			t.Fatalf("brakeman missing from %+v", res.Analyzers)
		}
		if brakeman.Available {
			t.Error("brakeman must be reported as unavailable")
		}
		if brakeman.Findings != 0 {
			t.Errorf("an unavailable analyzer cannot have findings, got %d", brakeman.Findings)
		}
		// Counting findings per analyzer is what distinguishes "clean" from
		// "never ran".
		if got := byName["rubocop"].Findings; got != 4 {
			t.Errorf("rubocop findings = %d, want 4", got)
		}
		if got := byName["simplecov"].Findings; got != 1 {
			t.Errorf("simplecov findings = %d, want 1", got)
		}
	})

	t.Run("refreshes the json export", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(root, ".tds", "map.json"))
		if err != nil {
			t.Fatalf("reading map.json: %v", err)
		}
		if !strings.Contains(string(b), "A#small") {
			t.Error("map.json must include the findings just written")
		}
	})
}

// TestRunReplacesPreviousFindings keeps repeated runs from accumulating
// duplicates of the same finding.
func TestRunReplacesPreviousFindings(t *testing.T) {
	root := newMappedRepo(t, "analyze")

	for i := 1; i <= 3; i++ {
		if _, err := Run(context.Background(), Options{Root: root}); err != nil {
			t.Fatalf("analyze run %d: %v", i, err)
		}
		stored, err := openStore(t, root).Findings()
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) != len(helperFindings) {
			t.Fatalf("after run %d: %d findings, want %d (runs must replace, not append)",
				i, len(stored), len(helperFindings))
		}
	}
}

// TestRunRestrictsAnalyzers checks that the analyzer filter reaches the provider
// and narrows what the summary reports.
func TestRunRestrictsAnalyzers(t *testing.T) {
	root := newMappedRepo(t, "analyze")

	res, err := Run(context.Background(), Options{Root: root, Analyzers: []string{"rubocop"}})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	for _, a := range res.Analyzers {
		if a.Name != "rubocop" {
			t.Errorf("analyzer %q reported despite the rubocop-only filter", a.Name)
		}
	}
}

// TestRunRequiresAMap refuses to guess: analyzing without a map would have
// nothing to resolve findings against.
func TestRunRequiresAMap(t *testing.T) {
	_, err := Run(context.Background(), Options{Root: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when no map exists")
	}
	if !strings.Contains(err.Error(), "tds map") {
		t.Errorf("error should point at `tds map`, got %q", err)
	}
}

// TestRunRefusesStaleMap is the integrity check behind commit provenance:
// findings carry line numbers, so resolving them against a different commit
// would attribute them to the wrong source.
func TestRunRefusesStaleMap(t *testing.T) {
	root := newMappedRepo(t, "analyze")
	initGitRepo(t, root)

	// Re-map so the store records a real commit to disagree with.
	if _, err := mapper.Build(context.Background(), mapper.Options{Root: root}); err != nil {
		t.Fatalf("re-building the map: %v", err)
	}

	_, err := Run(context.Background(), Options{
		Root:   root,
		Commit: "0000000000000000000000000000000000000000",
	})
	if err == nil {
		t.Fatal("expected a stale-map error")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error = %q, want it to name the staleness", err)
	}
}

// TestRunToleratesProviderFailure keeps one broken provider from failing the
// run — the same isolation the map pipeline gives.
func TestRunToleratesProviderFailure(t *testing.T) {
	root := newMappedRepo(t, "analyzefails")

	var warnings strings.Builder
	res, err := Run(context.Background(), Options{
		Root:  root,
		Warnf: func(f string, a ...any) { fmt.Fprintf(&warnings, f+"\n", a...) },
	})
	if err != nil {
		t.Fatalf("a failing analyzer must not fail the run: %v", err)
	}
	if res.Findings != 0 {
		t.Errorf("findings = %d, want 0", res.Findings)
	}
	if !strings.Contains(warnings.String(), "analyze failed") {
		t.Errorf("the failure must be reported, got warnings %q", warnings.String())
	}
}

// TestRunReportsPerAnalyzerErrors surfaces a partial failure without discarding
// the analyzers that did succeed.
func TestRunReportsPerAnalyzerErrors(t *testing.T) {
	root := newMappedRepo(t, "partialfailure")

	var warnings strings.Builder
	res, err := Run(context.Background(), Options{
		Root:  root,
		Warnf: func(f string, a ...any) { fmt.Fprintf(&warnings, f+"\n", a...) },
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if res.Findings != len(helperFindings) {
		t.Errorf("findings = %d, want the successful analyzers' results kept", res.Findings)
	}
	if !strings.Contains(warnings.String(), "brakeman") {
		t.Errorf("per-analyzer error must be reported, got %q", warnings.String())
	}
}

// TestRunSkipsStructureOnlyProviders keeps the core from asking a provider for
// an op it never advertised — the tree-sitter fallback being the real case.
func TestRunSkipsStructureOnlyProviders(t *testing.T) {
	root := newMappedRepo(t, "structureonly")

	res, err := Run(context.Background(), Options{Root: root})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(res.Providers) != 0 {
		t.Errorf("providers = %v, want none: the provider serves structure only", res.Providers)
	}
	if res.Findings != 0 {
		t.Errorf("findings = %d, want 0", res.Findings)
	}
}

func TestSymbolIndexResolve(t *testing.T) {
	ix := newSymbolIndex(helperSymbols)
	cases := []struct {
		path string
		line int
		want string
	}{
		{"a.rb", 1, "A"},       // class only
		{"a.rb", 3, "A#big"},   // class + big
		{"a.rb", 6, "A#small"}, // all three; innermost wins
		{"a.rb", 8, "A#small"}, // inclusive upper bound
		{"a.rb", 9, "A#big"},   // just past small
		{"a.rb", 20, "A"},      // inclusive class end
		{"a.rb", 21, ""},       // past every symbol
		{"a.rb", 0, ""},        // before every symbol
		{"other.rb", 6, ""},    // unmapped file
	}
	for _, tc := range cases {
		if got := ix.resolve(tc.path, tc.line); got != tc.want {
			t.Errorf("resolve(%s, %d) = %q, want %q", tc.path, tc.line, got, tc.want)
		}
	}
}

func initGitRepo(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "-q", "-m", "initial"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}
