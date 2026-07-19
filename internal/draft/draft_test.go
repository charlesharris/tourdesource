package draft

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
	"github.com/charlesharris/tourdesource/internal/tour"
)

// newFixtureMap writes a store that looks like a small Rails app: two
// controllers, two models, a job, a routes file, with churn making Issue the
// busiest thing in the repo.
func newFixtureMap(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mapDir := filepath.Join(root, ".tds")
	if err := os.MkdirAll(mapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"),
		[]byte("# Acme\n\nAcme is a tracker for widgets.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(mapDir, "map.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	files := []store.File{
		{Path: "README.md", Language: "markdown"},
		{Path: "config/routes.rb", Language: "ruby"},
		{Path: "app/models/issue.rb", Language: "ruby"},
		{Path: "app/models/user.rb", Language: "ruby"},
		{Path: "app/controllers/issues_controller.rb", Language: "ruby"},
		{Path: "app/controllers/application_controller.rb", Language: "ruby"},
		{Path: "app/jobs/mail_job.rb", Language: "ruby"},
		{Path: "test/issue_test.rb", Language: "ruby"},
	}
	if err := st.PutFiles(files); err != nil {
		t.Fatal(err)
	}

	sym := func(path, symbol, kind string, start, end int) protocol.Symbol {
		return protocol.Symbol{Path: path, Symbol: symbol, Name: symbol, Kind: kind, StartLine: start, EndLine: end}
	}
	symbols := []protocol.Symbol{
		sym("app/models/issue.rb", "Issue", "class", 1, 400),
		sym("app/models/user.rb", "User", "class", 1, 200),
		sym("app/controllers/issues_controller.rb", "IssuesController", "class", 1, 120),
		sym("app/controllers/issues_controller.rb", "IssuesController#index", "method", 10, 20),
		sym("app/controllers/issues_controller.rb", "IssuesController#show", "method", 22, 30),
		sym("app/controllers/issues_controller.rb", "IssuesController#helper", "method", 32, 40),
		sym("app/controllers/application_controller.rb", "ApplicationController", "class", 1, 90),
		sym("app/jobs/mail_job.rb", "MailJob", "class", 1, 30),
		// Too small to be a landmark.
		sym("app/models/user.rb", "TinyError", "class", 201, 203),
	}
	if err := st.PutSymbols(symbols); err != nil {
		t.Fatal(err)
	}

	if err := st.PutEntrypoints([]protocol.Entrypoint{
		{Path: "config/routes.rb", Kind: "rails-routes"},
		{Path: "app/models/issue.rb", Kind: "rails-model", Name: "Issue"},
		{Path: "app/models/user.rb", Kind: "rails-model", Name: "User"},
		{Path: "app/controllers/issues_controller.rb", Kind: "rails-controller", Name: "IssuesController"},
		{Path: "app/controllers/application_controller.rb", Kind: "rails-controller", Name: "ApplicationController"},
		{Path: "app/jobs/mail_job.rb", Kind: "rails-job", Name: "MailJob"},
	}); err != nil {
		t.Fatal(err)
	}

	sig := func(path string, churn int, authors ...string) store.GitSignal {
		return store.GitSignal{Path: path, Churn: churn, Authors: authors, AgeDays: 100}
	}
	if err := st.PutGitSignals([]store.GitSignal{
		sig("app/models/issue.rb", 500, "Ada", "Grace", "Alan"),
		sig("app/controllers/issues_controller.rb", 400, "Ada", "Grace"),
		sig("app/models/user.rb", 300, "Grace"),
		sig("app/controllers/application_controller.rb", 200, "Ada"),
		sig("app/jobs/mail_job.rb", 50, "Alan"),
		sig("test/issue_test.rb", 350, "Ada"),
		sig("config/routes.rb", 120, "Ada"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta("commit", "abc123def4567890"); err != nil {
		t.Fatal(err)
	}
	return root
}

func generate(t *testing.T, root string, opts Options) (*Result, string) {
	t.Helper()
	opts.Root = root
	res, err := Generate(context.Background(), opts)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("reading draft: %v", err)
	}
	return res, string(b)
}

// TestDraftParsesAsATour is the whole point: a generated draft must be a valid
// tour, not a nearly-valid one. This caught real nesting bugs — the format
// requires detours to sit inside a stop, and an earlier draft emitted them at
// chapter level.
func TestDraftParsesAsATour(t *testing.T) {
	root := newFixtureMap(t)
	_, md := generate(t, root, Options{})

	parsed, err := tour.Parse([]byte(md))
	if err != nil {
		t.Fatalf("generated draft does not parse as a tour: %v\n\n%s", err, md)
	}
	if len(parsed.Chapters) != 5 {
		t.Errorf("chapters = %d, want the 5 of the onboarding template", len(parsed.Chapters))
	}
	for _, want := range []string{
		"The 30-second version", "Follow one operation end to end",
		"The major landmarks", "Where things live", "Side quests",
	} {
		found := false
		for _, ch := range parsed.Chapters {
			if ch.Title == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing template chapter %q", want)
		}
	}
}

// TestDraftAnchorsExistInTheMap is the anti-hallucination guarantee (design
// §6.2): drafting may only point at symbols the map actually contains.
func TestDraftAnchorsExistInTheMap(t *testing.T) {
	root := newFixtureMap(t)
	_, md := generate(t, root, Options{})

	st, err := store.Open(filepath.Join(root, ".tds", "map.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	symbols, err := st.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, s := range symbols {
		known[s.Path+"::"+s.Symbol] = true
	}
	files, err := st.Files()
	if err != nil {
		t.Fatal(err)
	}
	knownFile := map[string]bool{}
	for _, f := range files {
		knownFile[f.Path] = true
	}

	anchors := anchorsIn(md)
	if len(anchors) == 0 {
		t.Fatal("draft emitted no anchors")
	}
	for _, a := range anchors {
		if strings.Contains(a, "::") {
			if !known[a] {
				t.Errorf("symbol anchor %q is not in the map", a)
			}
			continue
		}
		// Line-range anchor: the file at least must exist.
		path := a
		if i := strings.LastIndex(a, ":"); i > 0 {
			path = a[:i]
		}
		if !knownFile[path] {
			t.Errorf("line-range anchor %q names a file not in the map", a)
		}
	}
}

// TestDraftDoesNotRepeatAnchors keeps the same symbol from being pointed at in
// three chapters, which reads as padding.
func TestDraftDoesNotRepeatAnchors(t *testing.T) {
	root := newFixtureMap(t)
	_, md := generate(t, root, Options{})

	seen := map[string]bool{}
	for _, a := range anchorsIn(md) {
		if seen[a] {
			t.Errorf("anchor %q appears more than once", a)
		}
		seen[a] = true
	}
}

// TestLandmarkRankingPrefersEntrypointsAndChurn documents the ranking: an
// explicit framework entrypoint outranks a merely large class, and among
// entrypoints the busiest leads.
func TestLandmarkRankingPrefersEntrypointsAndChurn(t *testing.T) {
	root := newFixtureMap(t)
	st, err := store.Open(filepath.Join(root, ".tds", "map.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx, err := Assemble(st, root, AssembleOptions{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(ctx.Landmarks) == 0 {
		t.Fatal("no landmarks ranked")
	}
	if got := ctx.Landmarks[0].Symbol.Symbol; got != "Issue" {
		t.Errorf("top landmark = %q, want Issue (busiest entrypoint)", got)
	}
	for _, l := range ctx.Landmarks {
		if l.Symbol.Kind == "method" {
			t.Errorf("landmark %q is a method; landmarks should be places", l.Symbol.Symbol)
		}
		if l.Symbol.Symbol == "TinyError" {
			t.Error("a 3-line class should not rank as a landmark")
		}
	}
}

// TestSliceSkipsTheBaseController checks the trace starts at a real operation.
// ApplicationController is plumbing every request passes through, not an
// operation a newcomer should be walked through first.
func TestSliceSkipsTheBaseController(t *testing.T) {
	root := newFixtureMap(t)
	st, err := store.Open(filepath.Join(root, ".tds", "map.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx, err := Assemble(st, root, AssembleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Slice.Entry == nil {
		t.Fatal("no slice entry proposed")
	}
	if got := ctx.Slice.Entry.Symbol.Symbol; got != "IssuesController" {
		t.Errorf("slice entry = %q, want IssuesController", got)
	}

	var names []string
	for _, s := range ctx.Slice.Steps {
		names = append(names, s.Symbol.Symbol)
	}
	joined := strings.Join(names, ",")
	// Conventional REST actions are the trace; an arbitrary public method is not.
	if !strings.Contains(joined, "IssuesController#index") {
		t.Errorf("steps %v should include the index action", names)
	}
	if strings.Contains(joined, "IssuesController#helper") {
		t.Errorf("steps %v should not include a non-REST method", names)
	}
	// The trace should reach the record the controller names.
	if !strings.Contains(joined, "Issue") {
		t.Errorf("steps %v should reach the Issue model", names)
	}
}

// TestReadmeGroundsTheOpening checks the draft opens with what the project says
// about itself rather than what its file tree implies.
func TestReadmeGroundsTheOpening(t *testing.T) {
	root := newFixtureMap(t)
	_, md := generate(t, root, Options{})
	if !strings.Contains(md, "Acme is a tracker for widgets.") {
		t.Error("draft should quote the README's lead paragraph")
	}
	if !strings.Contains(md, "README.md") {
		t.Error("draft should attribute the lead to the README")
	}
}

// TestAuthorPhraseNeverReportsACount guards a real bug: gitsignals keeps only
// the top few authors, so rendering len(authors) claimed "3 authors" for files
// with hundreds of contributors.
func TestAuthorPhraseNeverReportsACount(t *testing.T) {
	cases := map[string][]string{
		"":                             nil,
		"mostly Ada":                   {"Ada"},
		"mostly Ada and Grace":         {"Ada", "Grace"},
		"mostly Ada, Grace and others": {"Ada", "Grace", "Alan"},
	}
	for want, authors := range cases {
		if got := authorPhrase(authors); got != want {
			t.Errorf("authorPhrase(%v) = %q, want %q", authors, got, want)
		}
	}
}

// TestGenerateRequiresAMap refuses to draft from nothing.
func TestGenerateRequiresAMap(t *testing.T) {
	_, err := Generate(context.Background(), Options{Root: t.TempDir()})
	if err == nil {
		t.Fatal("expected an error when no map exists")
	}
	if !strings.Contains(err.Error(), "tds map") {
		t.Errorf("error should point at `tds map`, got %q", err)
	}
}

// anchorsIn extracts every anchor="..." value from a draft.
func anchorsIn(md string) []string {
	var out []string
	const key = `anchor="`
	for i := 0; ; {
		j := strings.Index(md[i:], key)
		if j < 0 {
			return out
		}
		start := i + j + len(key)
		end := strings.Index(md[start:], `"`)
		if end < 0 {
			return out
		}
		out = append(out, md[start:start+end])
		i = start + end
	}
}

// assertParses fails unless md is a valid tour document.
func assertParses(t *testing.T, md string) {
	t.Helper()
	if _, err := tour.Parse([]byte(md)); err != nil {
		t.Fatalf("document does not parse as a tour: %v\n\n%s", err, md)
	}
}

// TestSeverityWeightingBeatsRawCounts covers TDS-39: on Redmine 63 of 114
// findings are one info-level style rule, so ranking by count would bury eight
// brakeman errors under it.
func TestSeverityWeightingBeatsRawCounts(t *testing.T) {
	noisy := "app/noisy.rb"
	risky := "app/risky.rb"
	var findings []protocol.Finding
	for i := 0; i < 20; i++ {
		findings = append(findings, protocol.Finding{
			Path: noisy, StartLine: i + 1, Severity: "info",
			Tool: "rubocop", Rule: "Style/OneClassPerFile", Message: "style",
		})
	}
	findings = append(findings, protocol.Finding{
		Path: risky, StartLine: 227, Severity: "error",
		Tool: "brakeman", Rule: "Remote Code Execution", Message: "Unsafe reflection",
	})

	concerns := summarizeFindings(findings)
	if concerns[noisy].Total <= concerns[risky].Total {
		t.Fatal("the noisy file should have more findings — otherwise this proves nothing")
	}
	if concerns[risky].Score <= concerns[noisy].Score {
		t.Errorf("weighted score: risky %d must beat noisy %d",
			concerns[risky].Score, concerns[noisy].Score)
	}

	files := []store.File{{Path: noisy, Language: "ruby"}, {Path: risky, Language: "ruby"}}
	a := summarizeAnalysis(findings, concerns, files, nil, 5)
	if len(a.Concerns) == 0 || a.Concerns[0].Path != risky {
		t.Errorf("one error should outrank twenty style nits, got %+v", a.Concerns)
	}
	if a.Errors != 1 || a.Info != 20 {
		t.Errorf("severity tally = %d errors / %d info, want 1 / 20", a.Errors, a.Info)
	}
	if len(a.Tools) != 2 || a.Tools[0] != "brakeman" {
		t.Errorf("tools = %v, want both, sorted", a.Tools)
	}
}

// TestConcernKeepsWorstFindingsFirst — the draft cites only a few findings per
// file, so they must be the ones that matter.
func TestConcernKeepsWorstFindingsFirst(t *testing.T) {
	p := "a.rb"
	concerns := summarizeFindings([]protocol.Finding{
		{Path: p, StartLine: 10, Severity: "info", Tool: "rubocop", Rule: "Style/X"},
		{Path: p, StartLine: 20, Severity: "error", Tool: "brakeman", Rule: "SQL Injection"},
		{Path: p, StartLine: 30, Severity: "warning", Tool: "brakeman", Rule: "File Access"},
		{Path: p, StartLine: 40, Severity: "info", Tool: "rubocop", Rule: "Style/Y"},
	})
	top := concerns[p].Top
	if len(top) != maxTopFindings {
		t.Fatalf("cited %d findings, want the cap of %d", len(top), maxTopFindings)
	}
	if top[0].Rule != "SQL Injection" || top[1].Rule != "File Access" {
		t.Errorf("worst-first ordering broken: %+v", top)
	}
}

// TestAnalysisAbsentWhenNotRun — drafting must work before `tds analyze` has
// ever been run, and must not claim a clean repo when nobody looked.
func TestAnalysisAbsentWhenNotRun(t *testing.T) {
	a := summarizeAnalysis(nil, nil, nil, nil, 5)
	if a.Ran() {
		t.Error("no findings and no tools means analysis never ran")
	}
	if summarizeFindings(nil) != nil {
		t.Error("no findings should produce no concerns")
	}
	if got := concernPhrase(Concern{}); got != "" {
		t.Errorf("empty concern should render as nothing, got %q", got)
	}
}

func TestFirstSentenceTrimsBrakemanProse(t *testing.T) {
	got := firstSentence("Possible SQL injection. Brakeman found this in a scope. See docs.")
	if got != "Possible SQL injection." {
		t.Errorf("firstSentence = %q", got)
	}
	if got := firstSentence("no trailing period here"); got != "no trailing period here" {
		t.Errorf("a single sentence should pass through, got %q", got)
	}
}

// TestSeverityClassesDoNotTradeOff pins the fix: no quantity of a lower
// severity class may overtake a single finding of a higher one.
func TestSeverityClassesDoNotTradeOff(t *testing.T) {
	many := make([]protocol.Finding, 0, 500)
	for i := 0; i < 500; i++ {
		many = append(many, protocol.Finding{Path: "noisy.rb", StartLine: i + 1, Severity: "warning", Tool: "t"})
	}
	one := []protocol.Finding{{Path: "risky.rb", StartLine: 1, Severity: "error", Tool: "t"}}

	noisy := summarizeFindings(many)["noisy.rb"]
	risky := summarizeFindings(one)["risky.rb"]
	if risky.Score <= noisy.Score {
		t.Errorf("one error (%d) must outrank 500 warnings (%d)", risky.Score, noisy.Score)
	}
}
