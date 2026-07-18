// Package site renders a tour as a multi-page static site.
//
// It emits the Codebase Explorer theme's data contract — `data/*.json` plus one
// content page per source file — and then runs Hugo over the embedded theme to
// produce a browsable site: an overview, an architecture map, a file explorer,
// per-file pages, the tour itself, a symbol index, and a search palette.
//
// Hugo is a real dependency of this output format, deliberately: the theme is
// authored as Hugo templates and keeping it that way means the design can be
// iterated with `hugo server` rather than re-implemented. tds still produces the
// single-file bundle (internal/builder) with no external tools, so the emailable
// artifact is unaffected. See docs/design.md §8 and TDS-58.
package site

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charlesharris/tourdesource/internal/manifest"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

// --- the theme's data contract ---
//
// These shapes are fixed by the theme (see its README). Keep them stable: the
// templates read these field names directly, so a rename here is a silent
// blank on a page rather than a compile error.

// SiteManifest is data/manifest.json.
type SiteManifest struct {
	Repo       RepoInfo    `json:"repo"`
	Stats      []Stat      `json:"stats"`
	Dirs       []DirCount  `json:"dirs"`
	Columns    []string    `json:"columns"`
	Subsystems []Subsystem `json:"subsystems"`
}

type RepoInfo struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
	Lang   string `json:"lang"`
	Title  string `json:"title"`
	Blurb  string `json:"blurb"`
}

type Stat struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type DirCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Pct   int    `json:"pct"`
}

// Subsystem is a named group of files placed in a column of the architecture
// map. Deps name other subsystems; "used by" is derived by the theme.
type Subsystem struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Column   string   `json:"column"`
	Files    int      `json:"files"`
	Commits  int      `json:"commits"`
	Churn    int      `json:"churn"` // 0–100, drives the hairline churn rule
	Entry    string   `json:"entry"`
	Desc     string   `json:"desc"`
	Deps     []string `json:"deps"`
	KeyFiles []string `json:"keyFiles"`
}

// SiteTour is data/tour.json.
type SiteTour struct {
	Chapters []TourChapter `json:"chapters"`
}

type TourChapter struct {
	Title string     `json:"title"`
	Stops []TourStop `json:"stops"`
}

type TourStop struct {
	ID    string `json:"id"`   // deep-linkable: /tour/#<id>
	Loc   string `json:"loc"`  // heading shown for the stop
	File  string `json:"file"` // repo path; its code renders beside the prose
	HL    string `json:"hl"`   // Chroma line range, e.g. "10-25"
	Prose string `json:"prose"`
}

// SiteSymbols is data/symbols.json.
type SiteSymbols struct {
	Symbols []SiteSymbol `json:"symbols"`
}

type SiteSymbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Subsystem string `json:"subsystem"`
	Refs      int    `json:"refs"`
	Summary   string `json:"summary"`
}

// FilePage is the frontmatter of one content/files/<slug>.md.
type FilePage struct {
	Title      string       `yaml:"title"`
	Path       string       `yaml:"path"` // canonical repo path — the join key
	Folder     string       `yaml:"folder"`
	Lang       string       `yaml:"lang"`
	Lines      int          `yaml:"lines"`
	Commits    int          `yaml:"commits"`
	Touched    string       `yaml:"touched"`
	Subsystem  string       `yaml:"subsystem"`
	HL         string       `yaml:"hl"`
	Summary    string       `yaml:"summary"`
	Symbols    []PageSymbol `yaml:"symbols"`
	Imports    []string     `yaml:"imports"`
	ImportedBy []string     `yaml:"importedBy"`
	TourStops  []string     `yaml:"tourStops"`
	Code       string       `yaml:"code"`
}

type PageSymbol struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
}

// --- assembly ---

// Input is everything needed to emit the site's data.
type Input struct {
	Manifest *manifest.Manifest
	Files    []store.File
	Symbols  []protocol.Symbol
	Imports  []protocol.Import
	Signals  []store.GitSignal
	// Entrypoints let a subsystem name a representative entry file.
	Entrypoints []protocol.Entrypoint
	// Source maps a repo path to its raw contents. Files absent here get a page
	// without code rather than being dropped: the explorer should still show
	// that they exist.
	Source map[string]string
	// ProjectName and Blurb describe the repo on the overview.
	ProjectName string
	Blurb       string
	Commit      string
}

// buildManifest assembles data/manifest.json.
func buildManifest(in Input, subs []Subsystem, columns []string) SiteManifest {
	langs := map[string]int{}
	for _, f := range in.Files {
		if f.Language != "" {
			langs[f.Language]++
		}
	}

	totalCommits := 0
	for _, s := range in.Signals {
		totalCommits += s.Churn
	}

	stops := 0
	for _, ch := range in.Manifest.Chapters {
		stops += countStops(ch.Stops)
	}

	return SiteManifest{
		Repo: RepoInfo{
			Name:   in.ProjectName,
			Commit: shortCommit(in.Commit),
			Lang:   describeStack(langs),
			Title:  in.Manifest.Title,
			Blurb:  in.Blurb,
		},
		Stats: []Stat{
			{Value: humanCount(len(in.Files)), Label: "Files"},
			{Value: humanCount(len(subs)), Label: "Subsystems"},
			{Value: humanCount(len(in.Symbols)), Label: "Symbols"},
			{Value: humanCount(stops), Label: "Tour stops"},
			{Value: humanCount(totalCommits), Label: "Commits"},
		},
		Dirs:       topDirs(in.Files, 8),
		Columns:    columns,
		Subsystems: subs,
	}
}

// buildTour flattens the compiled tour into the theme's shape.
//
// The theme renders a flat stop list with a chapter kicker, so detour stops are
// lifted into their parent chapter rather than dropped — a side-quest is still
// part of the tour, and losing it would silently shrink the reader's path.
func buildTour(m *manifest.Manifest) SiteTour {
	var out SiteTour
	for _, ch := range m.Chapters {
		tc := TourChapter{Title: ch.Title}
		var walk func(stops []manifest.Stop)
		walk = func(stops []manifest.Stop) {
			for _, s := range stops {
				tc.Stops = append(tc.Stops, TourStop{
					ID:    s.ID,
					Loc:   stopLabel(s),
					File:  s.Anchor.Path,
					HL:    lineRange(s.Anchor),
					Prose: htmlToText(s.Prose),
				})
				for _, d := range s.Detours {
					walk(d.Stops)
				}
			}
		}
		walk(ch.Stops)
		out.Chapters = append(out.Chapters, tc)
	}
	return out
}

// buildSymbols assembles data/symbols.json, ranked so the most-referenced
// symbols lead: an index of 13,000 symbols is only useful if it is ordered.
func buildSymbols(in Input, subsystemOf map[string]string, refs map[string]int, limit int) SiteSymbols {
	out := SiteSymbols{Symbols: []SiteSymbol{}}
	for _, s := range in.Symbols {
		// Methods are indexed too, but containers lead — a symbol index that is
		// 90% getters buries what a reader is looking for.
		out.Symbols = append(out.Symbols, SiteSymbol{
			Name:      s.Symbol,
			Kind:      s.Kind,
			File:      s.Path,
			Subsystem: subsystemOf[s.Path],
			Refs:      refs[s.Symbol],
		})
	}
	sort.SliceStable(out.Symbols, func(i, j int) bool {
		a, b := out.Symbols[i], out.Symbols[j]
		if ra, rb := kindRank(a.Kind), kindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Refs != b.Refs {
			return a.Refs > b.Refs
		}
		return a.Name < b.Name
	})
	if limit > 0 && len(out.Symbols) > limit {
		out.Symbols = out.Symbols[:limit]
	}
	return out
}

func kindRank(kind string) int {
	switch kind {
	case "class", "module", "struct", "interface", "trait", "enum":
		return 0
	case "type":
		return 1
	case "function":
		return 2
	default: // method and everything else
		return 3
	}
}

// --- helpers ---

func countStops(stops []manifest.Stop) int {
	n := 0
	for _, s := range stops {
		n++
		for _, d := range s.Detours {
			n += countStops(d.Stops)
		}
	}
	return n
}

// stopLabel is the heading shown for a stop: its symbol where it has one, and
// otherwise the file it points at.
func stopLabel(s manifest.Stop) string {
	if s.Anchor.Symbol != "" {
		return s.Anchor.Symbol
	}
	if s.Anchor.Path != "" {
		return s.Anchor.Path
	}
	return s.Anchor.Raw
}

// lineRange renders an anchor as a Chroma hl_lines range.
func lineRange(a manifest.Anchor) string {
	if !a.Resolved || a.StartLine <= 0 {
		return ""
	}
	if a.EndLine <= a.StartLine {
		return fmt.Sprintf("%d", a.StartLine)
	}
	return fmt.Sprintf("%d-%d", a.StartLine, a.EndLine)
}

// htmlToText flattens rendered prose back to plain text.
//
// The theme puts stop prose inside a <p>, so handing it HTML would nest block
// elements inside a paragraph and produce invalid markup. The manifest keeps the
// rendered form for the single-file viewer; here we want the words.
func htmlToText(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	text := b.String()
	for _, ent := range [][2]string{
		{"&amp;", "&"}, {"&lt;", "<"}, {"&gt;", ">"},
		{"&quot;", `"`}, {"&#39;", "'"}, {"&nbsp;", " "},
	} {
		text = strings.ReplaceAll(text, ent[0], ent[1])
	}
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}

// topDirs returns the largest top-level directories with their share of files.
func topDirs(files []store.File, max int) []DirCount {
	counts := map[string]int{}
	for _, f := range files {
		name := f.Path
		if i := strings.Index(name, "/"); i > 0 {
			name = name[:i]
		} else {
			continue // a root-level file belongs to no directory
		}
		counts[name]++
	}
	out := make([]DirCount, 0, len(counts))
	for name, n := range counts {
		pct := 0
		if len(files) > 0 {
			pct = n * 100 / len(files)
		}
		out = append(out, DirCount{Name: name, Count: n, Pct: pct})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// describeStack names the stack from the language mix, so the header reads
// "Ruby on Rails" rather than "ruby".
func describeStack(langs map[string]int) string {
	type lc struct {
		lang string
		n    int
	}
	var ranked []lc
	for l, n := range langs {
		ranked = append(ranked, lc{l, n})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].n != ranked[j].n {
			return ranked[i].n > ranked[j].n
		}
		return ranked[i].lang < ranked[j].lang
	})
	if len(ranked) == 0 {
		return ""
	}
	names := make([]string, 0, 2)
	for i, r := range ranked {
		if i >= 2 {
			break
		}
		names = append(names, displayLang(r.lang))
	}
	return strings.Join(names, " · ")
}

var langNames = map[string]string{
	"ruby": "Ruby", "javascript": "JavaScript", "typescript": "TypeScript",
	"python": "Python", "go": "Go", "java": "Java", "rust": "Rust",
	"c": "C", "cpp": "C++", "css": "CSS", "html": "HTML",
	"yaml": "YAML", "json": "JSON", "markdown": "Markdown",
}

func displayLang(l string) string {
	if n, ok := langNames[l]; ok {
		return n
	}
	if l == "" {
		return "other"
	}
	return strings.ToUpper(l[:1]) + l[1:]
}

// humanCount formats a count with thousands separators.
func humanCount(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, parts...), ",")
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// humanAge renders a timestamp as "4 days ago".
func humanAge(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 48*time.Hour:
		return "yesterday"
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d weeks ago", int(d.Hours()/24/7))
	case d < 730*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%d years ago", int(d.Hours()/24/365))
	}
}

// slugFor turns a repo path into a stable page slug, matching the theme's
// convention: separators and dots become dashes.
func slugFor(path string) string {
	s := strings.NewReplacer("/", "-", ".", "-", " ", "-", "_", "_").Replace(path)
	return strings.Trim(strings.ToLower(s), "-")
}

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("site: encoding data: %v", err)) // shapes are ours; a failure is a bug
	}
	return b
}
