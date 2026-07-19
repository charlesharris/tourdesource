package site

import (
	"fmt"
	"sort"

	"github.com/charlesharris/tourdesource/internal/lens"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

// Subsystem derivation — the deterministic half of TDS-59.
//
// The architecture map needs named groups of files placed in columns. tds can
// see structure but not intent: it knows `app/controllers` holds 61 controllers,
// not that they divide into "Issues & Tracking" and "REST API & Feeds". So this
// derives groups from directory role and computes every number honestly, and
// leaves *naming* to the narrate pass, which can read the code.
//
// The result is therefore mechanically named on its own ("app/models"), and the
// LLM pass upgrades the names and descriptions without changing the grouping.

// Column names, ordered left (what receives requests) to right (what everything
// else stands on). Fixed by the theme.
const (
	ColEntry   = "Entry / HTTP"
	ColFeature = "Feature areas"
	ColDomain  = "Shared domain"
	ColInfra   = "Infrastructure"
	// ColModules is used only by the generic derivation, where no convention
	// told us what role a directory plays. Sorting such groups into the role
	// columns would assert a layering nobody derived.
	ColModules = "Modules"
)

func allColumns() []string {
	return []string{ColEntry, ColModules, ColFeature, ColDomain, ColInfra}
}

// Derivation names how the subsystems were arrived at, so the page can describe
// itself accurately instead of always claiming an architecture was understood.
const (
	DerivationConvention = "convention" // a known layout matched (today: Rails)
	DerivationDirectory  = "directory"  // fallback: grouped by directory shape
)

// columnsFor returns the columns that actually hold a subsystem, in fixed
// left-to-right order.
//
// Not every layout populates every column: a classic Rails app like Redmine has
// no app/services or app/queries, so "Feature areas" is structurally empty
// there. Rendering the header anyway draws a labelled hole in the architecture
// and implies the layer exists but was missed, which is a claim about the
// codebase that is not true.
func columnsFor(subs []Subsystem) []string {
	used := map[string]bool{}
	for _, s := range subs {
		used[s.Column] = true
	}
	var out []string
	for _, c := range allColumns() {
		if used[c] {
			out = append(out, c)
		}
	}
	return out
}

// lensFor picks the lens governing a path, falling back to generic when no
// marker claimed it. A repository that matched nothing still gets an
// architecture map derived from its structure.
func lensFor(set *lens.Set, instances []lens.Instance, p string) (*lens.Lens, bool) {
	if set == nil {
		return nil, false
	}
	if in, ok := lens.Resolve(instances, p); ok {
		return in.Lens, true
	}
	return set.Get(lens.Generic)
}

// siteColumn maps a lens column onto the heading the theme displays.
func siteColumn(c string) string {
	switch c {
	case lens.ColumnEntry:
		return ColEntry
	case lens.ColumnFeature:
		return ColFeature
	case lens.ColumnDomain:
		return ColDomain
	case lens.ColumnInfra:
		return ColInfra
	default:
		return ColModules
	}
}

// DeriveSubsystems groups the mapped files into architecture nodes. The third
// return value names how the grouping was arrived at (DerivationConvention or
// DerivationDirectory) so the page can describe itself accurately.
func DeriveSubsystems(
	files []store.File,
	symbols []protocol.Symbol,
	imports []protocol.Import,
	signals []store.GitSignal,
	entrypoints []protocol.Entrypoint,
) ([]Subsystem, map[string]string, string) {
	churn := map[string]store.GitSignal{}
	for _, s := range signals {
		churn[s.Path] = s
	}

	type group struct {
		name    string
		column  string
		paths   []string
		commits int
	}
	groups := map[string]*group{}
	subsystemOf := map[string]string{} // repo path -> subsystem name

	// Only code composes a subsystem; a YAML locale file is not architecture.
	var code []store.File
	for _, f := range files {
		if f.Language == "" || f.Language == "markdown" || f.Language == "json" {
			continue
		}
		code = append(code, f)
	}

	add := func(name, column, path string, g *group) {
		if g == nil {
			g = &group{name: name, column: column}
			groups[name] = g
		}
		g.paths = append(g.paths, path)
		g.commits += churn[path].Churn
		subsystemOf[path] = name
	}

	// Which lens governs each file. Detection is scoped, so a repository can be
	// several kinds of project at once (docs/lenses.md).
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	set, err := lens.Builtins()
	if err != nil {
		// Embedded data failing to parse is a build-time mistake, not something
		// a user can act on; fall back to structure rather than failing a tour.
		set = nil
	}
	var instances []lens.Instance
	if set != nil {
		instances = lens.Detect(set, paths)
	}

	for _, f := range code {
		l, ok := lensFor(set, instances, f.Path)
		if !ok {
			continue
		}
		rel, root := f.Path, ""
		if in, found := lens.Resolve(instances, f.Path); found {
			rel, root = in.Rel(f.Path), in.Root
		}
		col, name, ok := l.RoleFor(rel)
		if !ok {
			continue
		}
		// A scoped instance names its subsystems relative to itself, so two
		// Ruby components in one repo both produce "Library". Qualify anything
		// off the repository root, or they silently merge into one node.
		if root != "" {
			name = root + ": " + name
		}
		add(name, siteColumn(col), f.Path, groups[name])
	}

	// The derivation describes the repository, so it is decided by what governs
	// the root. A Go project containing one vendored Ruby gem is still a Go
	// project, and reporting "convention" because a nested marker matched would
	// claim an understanding of the whole that nobody has.
	derivation := DerivationDirectory
	if in, ok := lens.Resolve(instances, "."); ok && in.Root == "" {
		derivation = DerivationConvention
	}

	// Dependencies: lift the map's file-level import edges to the group level.
	deps := map[string]map[string]bool{}
	for _, im := range imports {
		from, ok := subsystemOf[im.Path]
		if !ok {
			continue
		}
		to, ok := subsystemOf[im.Target]
		if !ok || to == from {
			continue
		}
		if deps[from] == nil {
			deps[from] = map[string]bool{}
		}
		deps[from][to] = true
	}

	// Churn is reported 0–100 relative to the busiest subsystem, since the theme
	// draws it as a proportional rule.
	maxCommits := 1
	for _, g := range groups {
		if g.commits > maxCommits {
			maxCommits = g.commits
		}
	}

	entryFor := map[string]string{} // subsystem -> a representative entrypoint
	for _, e := range entrypoints {
		if name, ok := subsystemOf[e.Path]; ok {
			if _, seen := entryFor[name]; !seen {
				entryFor[name] = e.Path
			}
		}
	}

	out := make([]Subsystem, 0, len(groups))
	for _, g := range groups {
		sort.Strings(g.paths)

		// Key files are the busiest ones: where the work actually happens is a
		// better introduction to a subsystem than alphabetical order.
		key := append([]string(nil), g.paths...)
		sort.SliceStable(key, func(i, j int) bool {
			return churn[key[i]].Churn > churn[key[j]].Churn
		})
		if len(key) > 5 {
			key = key[:5]
		}

		entry := entryFor[g.name]
		if entry == "" && len(key) > 0 {
			entry = key[0]
		}

		var depNames []string
		for d := range deps[g.name] {
			depNames = append(depNames, d)
		}
		sort.Strings(depNames)

		out = append(out, Subsystem{
			ID:       slugFor(g.name),
			Name:     g.name,
			Column:   g.column,
			Files:    len(g.paths),
			Commits:  g.commits,
			Churn:    g.commits * 100 / maxCommits,
			Entry:    entry,
			Desc:     describeSubsystem(g.name, len(g.paths), g.commits),
			Deps:     depNames,
			KeyFiles: key,
		})
	}

	// Order by column, then by weight within it, so the map reads left to right
	// and the most substantial node leads each column.
	colIndex := map[string]int{}
	for i, c := range allColumns() {
		colIndex[c] = i
	}
	sort.Slice(out, func(i, j int) bool {
		if ci, cj := colIndex[out[i].Column], colIndex[out[j].Column]; ci != cj {
			return ci < cj
		}
		if out[i].Files != out[j].Files {
			return out[i].Files > out[j].Files
		}
		return out[i].Name < out[j].Name
	})
	return out, subsystemOf, derivation
}

// describeSubsystem is the placeholder description used until the narrate pass
// replaces it. It states only what tds actually measured — inventing a purpose
// here is exactly the kind of confident-but-wrong text the draft avoids.
// It must not name a command that would not help: `--narrate` writes tour-stop
// prose and does not touch subsystems (TDS-59 is the pass that will).
func describeSubsystem(name string, files, commits int) string {
	return fmt.Sprintf("%d files, %s commits. Grouped by directory role; not yet described.",
		files, humanCount(commits))
}

// ReferenceCounts approximates how often each symbol is referenced, by counting
// how many files import the file it lives in.
//
// This is a proxy, not a call graph: tds has no cross-file resolution (design
// §12 puts that out of scope), so a symbol in a widely-imported file scores
// highly whether or not anyone calls it. It is good enough to rank a symbol
// index and is not presented as anything more.
func ReferenceCounts(symbols []protocol.Symbol, imports []protocol.Import) map[string]int {
	importedBy := map[string]int{}
	for _, im := range imports {
		importedBy[im.Target]++
	}
	refs := map[string]int{}
	for _, s := range symbols {
		refs[s.Symbol] = importedBy[s.Path]
	}
	return refs
}

// InvertImports builds the reverse edge list: path -> files that import it.
func InvertImports(imports []protocol.Import) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, im := range imports {
		key := im.Target + "\x00" + im.Path
		if seen[key] || im.Target == im.Path {
			continue
		}
		seen[key] = true
		out[im.Target] = append(out[im.Target], im.Path)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}
