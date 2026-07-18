// Package draft turns a repo map into a curated-ready tour skeleton.
//
// It has two halves, matching design §7 and the M4 plan:
//
//   - Context assembly (TDS-39) reads the map — entrypoints, git signals,
//     symbols, README — and ranks what a newcomer most needs to see.
//   - The template (TDS-40) is the opinionated onboarding skeleton those
//     findings get poured into.
//
// The output is a `.tour.md` whose anchors are drawn exclusively from symbols
// that exist in the map, with prose left as TODO placeholders carrying the facts
// tds knows. Constraining anchors to real symbols is the primary
// anti-hallucination lever (design §6.2): the drafting stage chooses *what to
// point at* from ground truth, and only the prose is left to a human or an LLM.
package draft

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/charlesharris/tourdesource/internal/anchor"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

// Context is the grounding assembled from a map: what this repo is, and which
// parts of it a newcomer most needs to see. It is deliberately a plain data
// structure — it is both what the deterministic renderer consumes and what a
// future AI pass (TDS-41/42) would receive as its prompt payload.
type Context struct {
	Root        string
	Commit      string
	ProjectName string
	Readme      Readme
	Languages   []LangCount
	Entrypoints map[string][]protocol.Entrypoint // by kind, each ranked
	// Landmarks is a ranked *pool*, deeper than the chapter needs. The renderer
	// skips any landmark an earlier chapter already anchored — repeating the same
	// symbol in three chapters reads as padding — and draws replacements from
	// further down, so the chapter still lands at its intended size.
	Landmarks     []Landmark
	LandmarkLimit int
	Hotspots      []Hotspot
	Slice         Slice
	Conventions   []Convention

	// landmarksUsed records how many of the pool actually became stops, after
	// dropping those an earlier chapter already covered.
	landmarksUsed int
	// resolver turns a stop's anchor back into concrete lines, so narration can
	// be shown the code it is describing. It resolves against the same map the
	// anchors were drawn from, so this cannot disagree with the built tour.
	resolver *anchor.Resolver
}

// resolveAnchor returns the file and line range an anchor points at, or an empty
// path when it does not resolve.
func (c *Context) resolveAnchor(anchorStr string) (path string, start, end int) {
	if c.resolver == nil {
		return "", 0, 0
	}
	got, err := c.resolver.Resolve(anchorStr)
	if err != nil || got.Kind == anchor.KindUnresolved {
		return "", 0, 0
	}
	return got.Path, got.StartLine, got.EndLine
}

// Readme is the repo's front-door prose, used to ground the opening chapter.
type Readme struct {
	Path  string
	Title string
	Lead  string // first substantive paragraph
	Lines int
}

// LangCount is a language and how many files use it.
type LangCount struct {
	Language string
	Files    int
}

// Landmark is a symbol worth a stop, with the evidence that selected it. The
// reason travels into the draft so a curator can see why tds proposed it.
type Landmark struct {
	Symbol protocol.Symbol
	Churn  int
	// Authors are the file's *primary* authors — gitsignals keeps the top few by
	// commit count, not the full contributor list. Naming them answers "who do I
	// ask about this?", which is worth more to a newcomer than a headcount.
	Authors []string
	Lines   int
	Kind    string // entrypoint kind, when it is one
	Score   float64
	Reason  string
}

// Hotspot is a file that changes often — where the work actually happens, and
// the natural target of a "I'm here to fix a bug" side-quest.
type Hotspot struct {
	Path    string
	Churn   int
	Authors []string // primary authors, as on Landmark
	AgeDays int
}

// Slice is the "follow one operation end to end" chapter's proposed trace: an
// entry point and the records it touches. It is a *proposal* — without call-graph
// analysis (explicitly out of scope, design §12) tds cannot know the true path,
// so the draft says so and invites the curator to adjust it.
type Slice struct {
	Entry  *Landmark
	Steps  []Landmark
	Reason string
}

// Convention is an observation about how the repo is laid out, for the
// "where things live" chapter.
type Convention struct {
	Title  string
	Detail string
	Anchor string // optional example anchor
}

// AssembleOptions tune context assembly.
type AssembleOptions struct {
	// MaxLandmarks bounds the "major landmarks" chapter. Design §7 calls for
	// 4–6: enough to map the system, few enough to still be a tour.
	MaxLandmarks int
	// MaxHotspots bounds the bug-fixing side-quest.
	MaxHotspots int
}

func (o AssembleOptions) withDefaults() AssembleOptions {
	if o.MaxLandmarks <= 0 {
		o.MaxLandmarks = 6
	}
	if o.MaxHotspots <= 0 {
		o.MaxHotspots = 5
	}
	return o
}

// Assemble builds the drafting context from a populated map.
func Assemble(st *store.Store, root string, opts AssembleOptions) (*Context, error) {
	opts = opts.withDefaults()

	commit, err := st.Meta("commit")
	if err != nil {
		return nil, fmt.Errorf("reading commit: %w", err)
	}
	files, err := st.Files()
	if err != nil {
		return nil, fmt.Errorf("reading files: %w", err)
	}
	symbols, err := st.Symbols()
	if err != nil {
		return nil, fmt.Errorf("reading symbols: %w", err)
	}
	entrypoints, err := st.Entrypoints()
	if err != nil {
		return nil, fmt.Errorf("reading entrypoints: %w", err)
	}
	signals, err := st.GitSignals()
	if err != nil {
		return nil, fmt.Errorf("reading git signals: %w", err)
	}

	churn := map[string]store.GitSignal{}
	for _, s := range signals {
		churn[s.Path] = s
	}

	ctx := &Context{
		Root:        root,
		Commit:      commit,
		ProjectName: projectName(root),
		Readme:      readReadme(root, files),
		Languages:   rankLanguages(files),
		Entrypoints: groupEntrypoints(entrypoints, churn),
	}
	// Rank a deeper pool than the chapter needs so the renderer has replacements
	// for landmarks that earlier chapters already covered.
	ctx.resolver = anchor.NewResolver(symbols)
	ctx.Landmarks = rankLandmarks(symbols, entrypoints, churn, opts.MaxLandmarks*3+4)
	ctx.LandmarkLimit = opts.MaxLandmarks
	ctx.Hotspots = rankHotspots(files, churn, opts.MaxHotspots)
	ctx.Slice = proposeSlice(ctx, symbols, churn)
	ctx.Conventions = observeConventions(files)
	return ctx, nil
}

// rankLanguages orders languages by file count, dropping unclassified files.
func rankLanguages(files []store.File) []LangCount {
	counts := map[string]int{}
	for _, f := range files {
		if f.Language == "" {
			continue
		}
		counts[f.Language]++
	}
	out := make([]LangCount, 0, len(counts))
	for l, n := range counts {
		out = append(out, LangCount{Language: l, Files: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Files != out[j].Files {
			return out[i].Files > out[j].Files
		}
		return out[i].Language < out[j].Language
	})
	return out
}

// groupEntrypoints buckets entrypoints by kind, ranking each bucket by the churn
// of its file so the busiest controller leads its group.
func groupEntrypoints(eps []protocol.Entrypoint, churn map[string]store.GitSignal) map[string][]protocol.Entrypoint {
	out := map[string][]protocol.Entrypoint{}
	for _, e := range eps {
		out[e.Kind] = append(out[e.Kind], e)
	}
	for kind := range out {
		group := out[kind]
		sort.Slice(group, func(i, j int) bool {
			ci, cj := churn[group[i].Path].Churn, churn[group[j].Path].Churn
			if ci != cj {
				return ci > cj
			}
			return group[i].Path < group[j].Path
		})
	}
	return out
}

// entrypointBonus is the score an explicit framework entrypoint earns. It
// dominates the size and churn terms on purpose: a Rails controller is a
// landmark because of what it *is*, not because of how much it churns.
const entrypointBonus = 100

// rankLandmarks scores container symbols — classes and modules, the things with
// names a newcomer will hear in conversation — and returns the best few.
//
// The score combines three signals the map already has:
//
//	entrypoint    an explicit framework landmark (controller, model, job, ...)
//	churn         how often the file changes: where the work is
//	size          how much lives in it: a proxy for how central it is
//
// Methods are excluded: a landmark is a place, and a chapter that lists methods
// reads as an index rather than a tour.
func rankLandmarks(
	symbols []protocol.Symbol,
	eps []protocol.Entrypoint,
	churn map[string]store.GitSignal,
	max int,
) []Landmark {
	epByName := map[string]string{} // qualified name -> entrypoint kind
	epByPath := map[string]string{}
	for _, e := range eps {
		if e.Name != "" {
			epByName[e.Name] = e.Kind
		}
		epByPath[e.Path] = e.Kind
	}

	maxChurn, maxLines := 1, 1
	for _, s := range symbols {
		if n := s.EndLine - s.StartLine; n > maxLines {
			maxLines = n
		}
	}
	for _, g := range churn {
		if g.Churn > maxChurn {
			maxChurn = g.Churn
		}
	}

	var out []Landmark
	for _, s := range symbols {
		if s.Kind != "class" && s.Kind != "module" {
			continue
		}
		lines := s.EndLine - s.StartLine
		// Skip trivia: one-line error classes and the like are not landmarks.
		if lines < 10 {
			continue
		}
		g := churn[s.Path]

		kind := epByName[s.Symbol]
		if kind == "" {
			kind = epByPath[s.Path]
		}

		score := 60*float64(g.Churn)/float64(maxChurn) + 40*float64(lines)/float64(maxLines)
		if kind != "" {
			score += entrypointBonus
		}

		out = append(out, Landmark{
			Symbol:  s,
			Churn:   g.Churn,
			Authors: g.Authors,
			Lines:   lines,
			Kind:    kind,
			Score:   score,
			Reason:  landmarkReason(kind, g.Churn, g.Authors, lines),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Symbol.Symbol < out[j].Symbol.Symbol
	})

	// One landmark per file: five stops inside one god-class is a worse tour
	// than five stops across five subsystems.
	var picked []Landmark
	seen := map[string]bool{}
	for _, l := range out {
		if seen[l.Symbol.Path] {
			continue
		}
		seen[l.Symbol.Path] = true
		picked = append(picked, l)
		if len(picked) >= max {
			break
		}
	}
	return picked
}

func landmarkReason(kind string, churn int, authors []string, lines int) string {
	var parts []string
	if kind != "" {
		parts = append(parts, "a "+strings.ReplaceAll(kind, "-", " "))
	}
	parts = append(parts, fmt.Sprintf("%d lines", lines))
	if churn > 0 {
		parts = append(parts, fmt.Sprintf("%d commits", churn))
	}
	if a := authorPhrase(authors); a != "" {
		parts = append(parts, a)
	}
	return strings.Join(parts, ", ")
}

// authorPhrase names a file's primary authors. gitsignals keeps only the top few
// by commit count, so this is deliberately phrased as "mostly X" rather than a
// total — reporting it as a contributor count would be wrong.
func authorPhrase(authors []string) string {
	switch len(authors) {
	case 0:
		return ""
	case 1:
		return "mostly " + authors[0]
	case 2:
		return "mostly " + authors[0] + " and " + authors[1]
	default:
		return "mostly " + strings.Join(authors[:2], ", ") + " and others"
	}
}

// rankHotspots returns the files that change most — where a newcomer fixing a
// bug is most likely to end up.
func rankHotspots(files []store.File, churn map[string]store.GitSignal, max int) []Hotspot {
	var out []Hotspot
	for _, f := range files {
		// Only code: a churning lockfile or translation bundle is noise here.
		if f.Language == "" || f.Language == "json" || f.Language == "yaml" || f.Language == "markdown" {
			continue
		}
		g, ok := churn[f.Path]
		if !ok || g.Churn == 0 {
			continue
		}
		out = append(out, Hotspot{Path: f.Path, Churn: g.Churn, Authors: g.Authors, AgeDays: g.AgeDays})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Churn != out[j].Churn {
			return out[i].Churn > out[j].Churn
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// resourceName maps a controller-ish name to its likely record: "IssuesController"
// -> "Issue". It is a convention-following guess, which is why the slice it feeds
// is presented to the curator as a proposal.
var controllerSuffix = regexp.MustCompile(`Controller$`)

func resourceName(controller string) string {
	base := controllerSuffix.ReplaceAllString(controller, "")
	if base == "" {
		return ""
	}
	return strings.TrimSuffix(base, "s") // Issues -> Issue
}

// proposeSlice picks the vertical trace for the "one operation end to end"
// chapter: the busiest controller, the actions it exposes, and the record those
// actions most likely operate on.
//
// tds has no call graph, so this follows naming convention rather than control
// flow. The draft labels it a proposal for exactly that reason.
func proposeSlice(ctx *Context, symbols []protocol.Symbol, churn map[string]store.GitSignal) Slice {
	controllers := ctx.Entrypoints["rails-controller"]
	if len(controllers) == 0 {
		// No web layer to trace: fall back to the top landmark so the chapter
		// still has a spine.
		if len(ctx.Landmarks) > 0 {
			return Slice{
				Entry:  &ctx.Landmarks[0],
				Reason: "no framework entrypoints were detected, so this is the highest-ranked landmark instead of a request trace",
			}
		}
		return Slice{}
	}

	// The busiest controller that isn't the framework base class — Application-
	// Controller is plumbing every request passes through, not an operation.
	var entry protocol.Entrypoint
	for _, c := range controllers {
		if !strings.HasPrefix(c.Name, "Application") {
			entry = c
			break
		}
	}
	if entry.Name == "" {
		entry = controllers[0]
	}

	byPath := map[string][]protocol.Symbol{}
	for _, s := range symbols {
		byPath[s.Path] = append(byPath[s.Path], s)
	}

	mk := func(s protocol.Symbol, reason string) Landmark {
		g := churn[s.Path]
		return Landmark{Symbol: s, Churn: g.Churn, Authors: g.Authors,
			Lines: s.EndLine - s.StartLine, Reason: reason}
	}

	var entryLandmark *Landmark
	for _, s := range byPath[entry.Path] {
		if s.Symbol == entry.Name && (s.Kind == "class" || s.Kind == "module") {
			l := mk(s, "the entry point for this operation")
			entryLandmark = &l
			break
		}
	}

	var steps []Landmark
	// The controller's actions, in source order, capped so the chapter stays a
	// trace rather than an API listing.
	const maxActions = 3
	n := 0
	for _, s := range byPath[entry.Path] {
		if s.Kind != "method" || !strings.HasPrefix(s.Symbol, entry.Name+"#") {
			continue
		}
		if !isAction(strings.TrimPrefix(s.Symbol, entry.Name+"#")) {
			continue
		}
		steps = append(steps, mk(s, "a request action on "+entry.Name))
		if n++; n >= maxActions {
			break
		}
	}

	// The record those actions most likely touch.
	if res := resourceName(entry.Name); res != "" {
		for _, s := range symbols {
			if s.Symbol == res && s.Kind == "class" {
				steps = append(steps, mk(s, "the record "+entry.Name+" operates on (matched by naming convention)"))
				break
			}
		}
	}

	return Slice{
		Entry: entryLandmark,
		Steps: steps,
		Reason: "chosen as the busiest non-base controller; tds has no call graph, " +
			"so the steps follow Rails naming convention rather than traced control flow",
	}
}

// restActions are the conventional Rails actions, preferred over ad-hoc public
// methods when proposing a trace.
var restActions = map[string]bool{
	"index": true, "show": true, "new": true, "create": true,
	"edit": true, "update": true, "destroy": true,
}

func isAction(name string) bool { return restActions[name] }

// observeConventions reports how the repo is laid out — the "where things live"
// chapter. Each observation is derived from the file list, never assumed.
func observeConventions(files []store.File) []Convention {
	dirCount := map[string]int{}
	var testDirs []string
	for _, f := range files {
		top := f.Path
		if i := strings.Index(top, "/"); i > 0 {
			top = top[:i]
		} else {
			top = "."
		}
		dirCount[top]++
	}
	for _, d := range []string{"test", "spec", "tests", "__tests__"} {
		if dirCount[d] > 0 {
			testDirs = append(testDirs, fmt.Sprintf("%s/ (%d files)", d, dirCount[d]))
		}
	}

	type dc struct {
		dir string
		n   int
	}
	var dirs []dc
	for d, n := range dirCount {
		if d == "." {
			continue
		}
		dirs = append(dirs, dc{d, n})
	}
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].n != dirs[j].n {
			return dirs[i].n > dirs[j].n
		}
		return dirs[i].dir < dirs[j].dir
	})
	if len(dirs) > 8 {
		dirs = dirs[:8]
	}
	layout := make([]string, len(dirs))
	for i, d := range dirs {
		layout[i] = fmt.Sprintf("`%s/` (%d)", d.dir, d.n)
	}

	out := []Convention{{
		Title:  "Top-level layout",
		Detail: "The largest directories are " + strings.Join(layout, ", ") + ".",
	}}
	if len(testDirs) > 0 {
		out = append(out, Convention{
			Title:  "Where the tests live",
			Detail: "Tests are under " + strings.Join(testDirs, ", ") + ".",
		})
	}
	return out
}

// projectName derives a display name from the repo directory.
func projectName(root string) string {
	return filepath.Base(strings.TrimRight(filepath.Clean(root), string(filepath.Separator)))
}

// readmeNames are the front-door files checked, in preference order.
var readmeNames = []string{
	"README.md", "README.rdoc", "README.rst", "README.txt", "README",
	"readme.md", "Readme.md",
}

// readReadme finds and summarizes the repo's README: its title and first
// substantive paragraph ground the opening chapter in what the project says
// about itself, rather than in what its file tree implies.
func readReadme(root string, files []store.File) Readme {
	present := map[string]bool{}
	for _, f := range files {
		present[f.Path] = true
	}
	for _, name := range readmeNames {
		if !present[name] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		text := string(b)
		return Readme{
			Path:  name,
			Title: firstHeading(text),
			Lead:  firstParagraph(text),
			Lines: strings.Count(text, "\n") + 1,
		}
	}
	return Readme{}
}

var headingPrefix = regexp.MustCompile(`^[#=]+\s*`)

// firstHeading returns the first Markdown/RDoc heading, or "".
func firstHeading(text string) string {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "=") {
			return strings.TrimSpace(headingPrefix.ReplaceAllString(t, ""))
		}
		return "" // body before any heading: no title to take
	}
	return ""
}

// firstParagraph returns the first paragraph that isn't a heading, badge, or
// boilerplate line, truncated to a sentence or two.
func firstParagraph(text string) string {
	var para []string
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "=") ||
			strings.HasPrefix(t, "[!") || strings.HasPrefix(t, "![") ||
			strings.HasPrefix(t, "<") {
			continue
		}
		para = append(para, t)
		if len(para) >= 3 {
			break
		}
	}
	s := strings.Join(para, " ")
	const limit = 320
	if len(s) > limit {
		if i := strings.LastIndex(s[:limit], ". "); i > 40 {
			return s[:i+1]
		}
		return strings.TrimSpace(s[:limit]) + "…"
	}
	return s
}
