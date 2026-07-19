package site

import (
	"strings"
	"testing"
	"time"

	"github.com/charlesharris/tourdesource/internal/manifest"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

// TestFrontmatterKeepsCodeVerbatim is the one that matters most: the `code`
// block is source, and a page that silently mangles it is worse than no page.
func TestFrontmatterKeepsCodeVerbatim(t *testing.T) {
	code := "class Invoice\n" +
		"  # a comment with: a colon, \"quotes\" and a #hash\n" +
		"\n" + // a blank line inside the block
		"  def finalize\n" +
		"    raise \"already: finalized\" if finalized?\n" +
		"  end\n" +
		"end\n"

	out := string(renderFrontmatter(FilePage{
		Title: "app/models/invoice.rb", Path: "app/models/invoice.rb",
		Lang: "ruby", Code: code,
	}))

	body, ok := strings.CutPrefix(out[strings.Index(out, "  code: |\n")+len("  code: |\n"):], "")
	if !ok {
		t.Fatal("no code block emitted")
	}
	body = strings.TrimSuffix(body, "---\n")

	// Reconstruct the source by removing exactly the block indentation.
	var got []string
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		got = append(got, strings.TrimPrefix(line, "    "))
	}
	if reconstructed := strings.Join(got, "\n") + "\n"; reconstructed != code {
		t.Errorf("code was not preserved verbatim:\n got %q\nwant %q", reconstructed, code)
	}
}

// TestFrontmatterNestsCustomParams pins the Hugo-compatibility fix: `path` and
// `lang` were removed as front-matter keys in Hugo 0.144, and emitting them at
// the top level is a hard build error. Nesting under `params:` keeps
// `.Params.path` resolving so the theme needs no change.
func TestFrontmatterNestsCustomParams(t *testing.T) {
	out := string(renderFrontmatter(FilePage{
		Title: "a.rb", Path: "app/a.rb", Lang: "ruby", Folder: "app",
		Imports: []string{"b.rb"}, Code: "x = 1\n",
	}))

	if !strings.Contains(out, "params:\n") {
		t.Fatal("custom fields must be nested under params:")
	}
	for _, reserved := range []string{"\npath:", "\nlang:"} {
		if strings.Contains(out, reserved) {
			t.Errorf("%q at the top level is a hard error on Hugo >= 0.144", strings.TrimSpace(reserved))
		}
	}
	for _, want := range []string{`  path: "app/a.rb"`, `  lang: "ruby"`, "  imports:\n", `    - "b.rb"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing nested field %q", want)
		}
	}
}

// TestTourKeepsDetoursNested covers TDS-63: a side-quest is deliberately off the
// main path, so it must stay attached to its parent stop rather than being
// lifted into the chapter's linear sequence.
func TestTourKeepsDetoursNested(t *testing.T) {
	m := tourWithDetour()
	got := buildTour(m)

	if len(got.Chapters) != 1 {
		t.Fatalf("want 1 chapter, got %d", len(got.Chapters))
	}
	stops := got.Chapters[0].Stops
	if len(stops) != 1 {
		t.Fatalf("want 1 top-level stop (the detour stop must not be lifted), got %d", len(stops))
	}
	if len(stops[0].Detours) != 1 || len(stops[0].Detours[0].Stops) != 1 {
		t.Fatalf("detour stop was lost: %+v", stops[0].Detours)
	}
	if title := stops[0].Detours[0].Title; title != "aside" {
		t.Errorf("detour title = %q, want %q", title, "aside")
	}
	if hl := stops[0].HL; hl != "1-4" {
		t.Errorf("hl = %q, want the Chroma range 1-4", hl)
	}
	if hl := stops[0].Detours[0].Stops[0].HL; hl != "7" {
		t.Errorf("single-line detour hl = %q, want 7", hl)
	}
}

// TestTourKeepsProseAsHTML covers TDS-63: prose is authored Markdown rendered to
// HTML, and flattening it to text threw away every list, link and code span.
func TestTourKeepsProseAsHTML(t *testing.T) {
	m := tourWithDetour()
	got := buildTour(m)

	if p := got.Chapters[0].Stops[0].Prose; p != "<p>one</p>" {
		t.Errorf("prose = %q, want the rendered HTML preserved", p)
	}
	if p := got.Chapters[0].Stops[0].Detours[0].Stops[0].Prose; p != "<p>two</p>" {
		t.Errorf("detour prose = %q, want the rendered HTML preserved", p)
	}
}

// TestTourCarriesAnchorProvenance covers TDS-63: the theme can only warn about a
// bad anchor if the projection hands it the resolution result.
func TestTourCarriesAnchorProvenance(t *testing.T) {
	m := &manifest.Manifest{Chapters: []manifest.Chapter{{
		Stops: []manifest.Stop{
			{ID: "ok", Anchor: manifest.Anchor{Path: "a.rb", Symbol: "A", Kind: "symbol", StartLine: 1, EndLine: 2, Resolved: true}},
			{ID: "loose", Anchor: manifest.Anchor{Path: "b.rb", Raw: "b.rb#B", Kind: "symbol", StartLine: 3, EndLine: 4, Resolved: true, Loose: true}},
			{ID: "bad", Anchor: manifest.Anchor{Path: "c.rb", Raw: "c.rb#Nope", Kind: "unresolved", Reason: "symbol not found"}},
		},
	}}}
	stops := buildTour(m).Chapters[0].Stops

	if a := stops[0].Anchor; !a.Resolved || a.Loose {
		t.Errorf("clean anchor = %+v, want resolved and not loose", a)
	}
	if a := stops[1].Anchor; !a.Loose || a.Raw != "b.rb#B" {
		t.Errorf("loose anchor = %+v, want Loose with its raw string", a)
	}
	if a := stops[2].Anchor; a.Resolved || a.Reason != "symbol not found" {
		t.Errorf("unresolved anchor = %+v, want the reason carried through", a)
	}
}

// TestTourCarriesFrontMatter covers TDS-63: the tour's own introduction,
// audience and compile warnings were dropped entirely.
func TestTourCarriesFrontMatter(t *testing.T) {
	m := &manifest.Manifest{
		Title: "T", Intro: "<p>why</p>", Audience: "new backend engineers",
		Warnings: []string{"c.rb#Nope: symbol not found"},
		Chapters: []manifest.Chapter{{Title: "Ch", Intro: "<p>chapter why</p>"}},
	}
	got := buildTour(m)

	if got.Title != "T" || got.Intro != "<p>why</p>" || got.Audience != "new backend engineers" {
		t.Errorf("front matter lost: %+v", got)
	}
	if len(got.Warnings) != 1 {
		t.Errorf("warnings = %v, want the unresolved-anchor note carried through", got.Warnings)
	}
	if got.Chapters[0].Intro != "<p>chapter why</p>" {
		t.Errorf("chapter intro = %q, want it preserved", got.Chapters[0].Intro)
	}
}

// TestWalkSiteStopsCountsDetours covers TDS-63: the stop count and the per-file
// "visited by the tour" back-links must see detour stops too.
func TestWalkSiteStopsCountsDetours(t *testing.T) {
	var ids []string
	walkSiteStops(buildTour(tourWithDetour()), func(s TourStop) { ids = append(ids, s.ID) })
	if len(ids) != 2 || ids[0] != "s1" || ids[1] != "s2" {
		t.Errorf("walk visited %v, want [s1 s2] in reading order", ids)
	}
}

// tourWithDetour is a one-chapter tour whose only stop carries a side-quest.
func tourWithDetour() *manifest.Manifest {
	return &manifest.Manifest{
		Title: "T",
		Chapters: []manifest.Chapter{{
			Title: "Ch",
			Stops: []manifest.Stop{{
				ID: "s1", Prose: "<p>one</p>",
				Anchor: manifest.Anchor{Path: "a.rb", Symbol: "A", StartLine: 1, EndLine: 4, Resolved: true},
				Detours: []manifest.Detour{{
					Title: "aside",
					Stops: []manifest.Stop{{
						ID: "s2", Prose: "<p>two</p>",
						Anchor: manifest.Anchor{Path: "b.rb", StartLine: 7, EndLine: 7, Resolved: true},
					}},
				}},
			}},
		}},
	}
}

func TestUnresolvedAnchorHasNoHighlight(t *testing.T) {
	m := &manifest.Manifest{Chapters: []manifest.Chapter{{
		Stops: []manifest.Stop{{ID: "s", Anchor: manifest.Anchor{Path: "a.rb", Resolved: false}}},
	}}}
	if hl := buildTour(m).Chapters[0].Stops[0].HL; hl != "" {
		t.Errorf("hl = %q, want empty for an unresolved anchor", hl)
	}
}

// TestSubsystemsGroupByRole covers the deterministic half of TDS-59.
func TestSubsystemsGroupByRole(t *testing.T) {
	files := []store.File{
		// Markers: roles are only claimed for a layout that was actually
		// detected, so a Rails-shaped tree with no Gemfile is not assumed to be
		// Rails (TDS-74).
		{Path: "Gemfile", Language: "ruby"},
		{Path: "config/routes.rb", Language: "ruby"},
		{Path: "app/controllers/issues_controller.rb", Language: "ruby"},
		{Path: "app/controllers/projects_controller.rb", Language: "ruby"},
		{Path: "app/models/issue.rb", Language: "ruby"},
		{Path: "app/jobs/mail_job.rb", Language: "ruby"},
		{Path: "lib/redmine/plugin.rb", Language: "ruby"},
		// Excluded: tests describe the system rather than compose it.
		{Path: "test/unit/issue_test.rb", Language: "ruby"},
		// Excluded: no language.
		{Path: "README.rdoc", Language: ""},
	}
	signals := []store.GitSignal{
		{Path: "app/models/issue.rb", Churn: 500},
		{Path: "app/controllers/issues_controller.rb", Churn: 400},
		{Path: "app/controllers/projects_controller.rb", Churn: 10},
	}
	imports := []protocol.Import{
		{Path: "app/controllers/issues_controller.rb", Target: "app/models/issue.rb"},
		// A self-edge within a subsystem must not become a dependency.
		{Path: "app/controllers/issues_controller.rb", Target: "app/controllers/projects_controller.rb"},
	}
	eps := []protocol.Entrypoint{{Path: "app/controllers/issues_controller.rb", Kind: "rails-controller"}}

	subs, of, _ := DeriveSubsystems(files, nil, imports, signals, eps)

	byName := map[string]Subsystem{}
	for _, s := range subs {
		byName[s.Name] = s
	}
	if _, ok := byName["Controllers"]; !ok {
		t.Fatalf("no Controllers subsystem in %v", byName)
	}
	if got := byName["Controllers"].Column; got != ColEntry {
		t.Errorf("Controllers column = %q, want %q", got, ColEntry)
	}
	if got := byName["Domain models"].Column; got != ColDomain {
		t.Errorf("Domain models column = %q, want %q", got, ColDomain)
	}
	if of["test/unit/issue_test.rb"] != "" {
		t.Error("tests should not be placed in a subsystem")
	}
	if got := byName["Controllers"].Files; got != 2 {
		t.Errorf("Controllers files = %d, want 2", got)
	}
	if got := byName["Controllers"].Commits; got != 410 {
		t.Errorf("Controllers commits = %d, want 410", got)
	}
	// Dependencies are lifted from file-level imports; self-edges dropped.
	if deps := byName["Controllers"].Deps; len(deps) != 1 || deps[0] != "Domain models" {
		t.Errorf("Controllers deps = %v, want [Domain models]", deps)
	}
	// Key files lead with the busiest.
	if kf := byName["Controllers"].KeyFiles; len(kf) == 0 || kf[0] != "app/controllers/issues_controller.rb" {
		t.Errorf("key files should lead with the busiest, got %v", kf)
	}
	// Churn is relative to the busiest subsystem.
	if c := byName["Domain models"].Churn; c != 100 {
		t.Errorf("the busiest subsystem should be churn 100, got %d", c)
	}
}

// TestSubsystemDescriptionsClaimNothing — an invented purpose is exactly the
// confident-but-wrong text the draft is careful to avoid.
func TestSubsystemDescriptionsClaimNothing(t *testing.T) {
	desc := describeSubsystem("Controllers", 57, 3861)
	if !strings.Contains(desc, "57 files") || !strings.Contains(desc, "3,861 commits") {
		t.Errorf("description should state what was measured, got %q", desc)
	}
	if !strings.Contains(desc, "not yet described") {
		t.Errorf("description should admit it is a placeholder, got %q", desc)
	}
	// TDS-70: it used to send the reader to `tds draft --narrate`, which only
	// writes tour-stop prose and leaves subsystems exactly as they were.
	if strings.Contains(desc, "--narrate") {
		t.Errorf("description must not name a command that would not change it, got %q", desc)
	}
}

// TestColumnsDropEmptyOnes covers TDS-70: a classic Rails app has no
// app/services, so claiming an empty "Feature areas" layer misdescribes it.
func TestColumnsDropEmptyOnes(t *testing.T) {
	subs := []Subsystem{
		{Name: "Controllers", Column: ColEntry},
		{Name: "Domain models", Column: ColDomain},
		{Name: "Database", Column: ColInfra},
	}
	got := columnsFor(subs)
	want := []string{ColEntry, ColDomain, ColInfra}
	if len(got) != len(want) {
		t.Fatalf("columns = %v, want %v (the empty Feature areas column dropped)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("columns = %v, want %v in fixed left-to-right order", got, want)
		}
	}
	if len(columnsFor(nil)) != 0 {
		t.Error("no subsystems should yield no columns")
	}
}

// TestGenericDerivationGroupsByDirectory covers TDS-67: a repo whose layout
// matches no convention produced zero subsystems and a blank Architecture tab.
func TestGenericDerivationGroupsByDirectory(t *testing.T) {
	// tourdesource's own shape: Go at the root, a Ruby gem under providers/.
	files := []store.File{
		{Path: "main.go", Language: "go"},
		{Path: "internal/site/data.go", Language: "go"},
		{Path: "internal/site/build.go", Language: "go"},
		{Path: "internal/cli/build.go", Language: "go"},
		{Path: "providers/ruby/lib/tds/structure.rb", Language: "ruby"},
		{Path: "test/unit/thing_test.go", Language: "go"}, // not architecture
	}
	subs, of, derivation := DeriveSubsystems(files, nil, nil, nil, nil)

	if derivation != DerivationDirectory {
		t.Fatalf("derivation = %q, want %q for an unrecognised layout", derivation, DerivationDirectory)
	}
	if len(subs) == 0 {
		t.Fatal("an unrecognised layout must still yield subsystems, not a blank page")
	}
	byName := map[string]Subsystem{}
	for _, s := range subs {
		byName[s.Name] = s
	}
	// internal/ is a container: its packages are separate nodes, not one blob.
	if _, ok := byName["internal/site"]; !ok {
		t.Errorf("want a node per package under internal/, got %v", byName)
	}
	if _, ok := byName["internal"]; ok {
		t.Error("internal/ must not collapse into a single node")
	}
	if n := byName["internal/site"].Files; n != 2 {
		t.Errorf("internal/site files = %d, want 2", n)
	}
	if c := byName["(root)"].Column; c != ColEntry {
		t.Errorf("a root main file should be an entry point, got column %q", c)
	}
	// Nothing derived a role for a Go package, so nothing claims one.
	if c := byName["internal/site"].Column; c != ColModules {
		t.Errorf("column = %q, want the unlabelled %q — no role was derived", c, ColModules)
	}
	if _, ok := of["test/unit/thing_test.go"]; ok {
		t.Error("tests describe the system rather than compose it")
	}
}

// TestConventionWinsOverGeneric covers TDS-67: a recognised layout must not pick
// up directory-shaped guesses alongside its real roles.
func TestConventionWinsOverGeneric(t *testing.T) {
	files := []store.File{
		{Path: "Gemfile", Language: "ruby"},
		{Path: "config/routes.rb", Language: "ruby"},
		{Path: "app/controllers/issues_controller.rb", Language: "ruby"},
		{Path: "app/models/issue.rb", Language: "ruby"},
		{Path: "script/oddball.rb", Language: "ruby"}, // matches no convention
	}
	subs, _, derivation := DeriveSubsystems(files, nil, nil, nil, nil)

	if derivation != DerivationConvention {
		t.Fatalf("derivation = %q, want %q", derivation, DerivationConvention)
	}
	for _, s := range subs {
		if s.Name == "script" {
			t.Error("an unmatched directory must not become a subsystem when a layout was recognised")
		}
	}
}

// TestLibIsSharedDomain covers TDS-70: lib/ is a project's own shared code, and
// filing it under Infrastructure buried Redmine's second largest body of
// domain logic.
// The rule now lives in the ruby lens, inherited by rails (TDS-74), so this
// asserts it end to end through the derivation rather than through a helper.
func TestLibIsSharedDomain(t *testing.T) {
	files := []store.File{
		{Path: "Gemfile", Language: "ruby"},
		{Path: "config/routes.rb", Language: "ruby"},
		{Path: "lib/redmine/access_control.rb", Language: "ruby"},
	}
	subs, _, _ := DeriveSubsystems(files, nil, nil, nil, nil)
	for _, s := range subs {
		if s.Name == "Library" {
			if s.Column != ColDomain {
				t.Errorf("lib/ column = %q, want %q", s.Column, ColDomain)
			}
			return
		}
	}
	t.Fatalf("no Library subsystem derived; got %+v", subs)
}

func TestSymbolIndexRanksContainersFirst(t *testing.T) {
	in := Input{Symbols: []protocol.Symbol{
		{Symbol: "Issue#tiny", Kind: "method", Path: "a.rb"},
		{Symbol: "Issue", Kind: "class", Path: "a.rb"},
		{Symbol: "helper", Kind: "function", Path: "b.rb"},
	}}
	got := buildSymbols(in, map[string]string{}, map[string]int{}, 0)

	if got.Symbols[0].Name != "Issue" {
		t.Errorf("a class should outrank a method and a function, got %q first", got.Symbols[0].Name)
	}
	if n := len(got.Symbols); n != 3 {
		t.Errorf("indexed %d symbols, want 3", n)
	}
}

func TestSymbolIndexIsBounded(t *testing.T) {
	var syms []protocol.Symbol
	for i := 0; i < 100; i++ {
		syms = append(syms, protocol.Symbol{Symbol: "S", Kind: "method", Path: "a.rb"})
	}
	// An index of every symbol in a large repo is unusable rather than thorough.
	if n := len(buildSymbols(Input{Symbols: syms}, nil, nil, 10).Symbols); n != 10 {
		t.Errorf("indexed %d, want the limit of 10", n)
	}
}

func TestHTMLToText(t *testing.T) {
	cases := map[string]string{
		"<p>Hello <code>Issue</code> world</p>": "Hello Issue world",
		"<p>a &amp; b &lt;c&gt;</p>":            "a & b <c>",
		"plain":                                 "plain",
		"<p>one</p>\n<p>two</p>":                "one two",
	}
	for in, want := range cases {
		if got := htmlToText(in); got != want {
			t.Errorf("htmlToText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanCount(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 13227: "13,227", 1234567: "1,234,567"} {
		if got := humanCount(in); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanAge(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"2026-07-17T12:00:00Z": "yesterday",
		"2026-07-14T12:00:00Z": "4 days ago",
		"2026-06-18T12:00:00Z": "4 weeks ago",
		"2025-07-18T12:00:00Z": "12 months ago",
		"not a date":           "",
	}
	for in, want := range cases {
		if got := humanAge(in, now); got != want {
			t.Errorf("humanAge(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugForIsStable(t *testing.T) {
	if a, b := slugFor("app/models/issue.rb"), slugFor("app/models/issue.rb"); a != b {
		t.Error("slugs must be stable")
	}
	if slugFor("app/models/issue.rb") == slugFor("app/models/issue_rb") {
		t.Error("distinct paths should not collide")
	}
}

// TestIsMinified covers TDS-69: minified bundles expand ~9x under Chroma, so
// they must be detected by shape rather than by filename.
func TestIsMinified(t *testing.T) {
	authored := strings.Repeat("  def finalize(invoice, now = Time.current)\n", 400)
	if isMinified([]byte(authored)) {
		t.Error("authored Ruby at ~44 bytes/line must not be treated as minified")
	}
	// Redmine ships this as jquery-3.7.1-ui-1.13.3.js — no .min in the name.
	bundle := strings.Repeat("!function(e,t){\"object\"==typeof module?module.exports=t(e):e.$=t(e)}", 900) + "\n"
	if !isMinified([]byte(bundle)) {
		t.Error("a single-line bundle must be treated as minified")
	}
	// Long but authored: a wide table or a long string literal per line.
	wide := strings.Repeat(strings.Repeat("x", 300)+"\n", 50)
	if isMinified([]byte(wide)) {
		t.Error("300 bytes/line is long but plausible for authored code")
	}
	if isMinified([]byte("short\n")) {
		t.Error("a tiny file cannot be judged minified")
	}
}

// TestThemeIsEmbedded guards against shipping a binary that cannot build a site.
func TestThemeIsEmbedded(t *testing.T) {
	for _, want := range []string{
		"theme/hugo.toml",
		"theme/layouts/_default/baseof.html",
		"theme/layouts/tour/list.html",
		"theme/layouts/partials/tourstop.html",
		"theme/layouts/partials/tree.html",
		"theme/static/css/classical.css",
		"theme/static/js/app.js",
	} {
		if _, err := themeFS.ReadFile(want); err != nil {
			t.Errorf("theme file %s is not embedded: %v", want, err)
		}
	}
}

func TestFindHugoReportsMissingActionably(t *testing.T) {
	_, _, err := findHugo("definitely-not-hugo-xyz")
	if err == nil {
		t.Fatal("expected an error for a missing hugo")
	}
	// A missing build tool should say what to install, not just fail.
	for _, want := range []string{"brew install hugo", "extended", minHugoVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.164.0", "0.128.0", 1},
		{"0.128.0", "0.128.0", 0},
		{"0.127.9", "0.128.0", -1},
		{"1.0.0", "0.999.0", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
