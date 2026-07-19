package site

import (
	"fmt"
	"sort"
	"strings"

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

// containerDirs hold modules rather than being one themselves: grouping all of
// `internal/` as a single node would produce one blob where a Go developer sees
// fifteen packages.
var containerDirs = map[string]bool{
	"internal": true, "pkg": true, "src": true, "lib": true, "libs": true,
	"packages": true, "apps": true, "modules": true, "services": true,
	"components": true, "crates": true, "providers": true, "cmd": true,
}

// genericGroup names the subsystem a path belongs to when no convention applies:
// its directory, descending one level into container directories.
//
// This is the honest floor — structure is all we have, so structure is all it
// claims. Every future lens degrades to this when its markers are absent.
func genericGroup(path string) (name string, ok bool) {
	if notArchitecture(path) {
		return "", false
	}
	parts := strings.Split(path, "/")
	switch {
	case len(parts) == 1:
		// A file at the repo root — main.go, setup.py.
		return "(root)", true
	case containerDirs[parts[0]] && len(parts) > 2:
		return parts[0] + "/" + parts[1], true
	default:
		return parts[0], true
	}
}

// genericColumn places a generic group. Only entry points are claimed, because
// `cmd/` and a root main file are near-universal and unambiguous; everything
// else stays in the unlabelled Modules column.
func genericColumn(name string) string {
	if name == "(root)" || name == "cmd" || strings.HasPrefix(name, "cmd/") ||
		name == "bin" || strings.HasPrefix(name, "bin/") {
		return ColEntry
	}
	return ColModules
}

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

// dirRole maps a directory prefix to the column its files belong in, and a
// readable name. Longest prefix wins, so `app/models/concerns` can be placed
// differently from `app/models`.
var dirRoles = []struct {
	prefix string
	column string
	name   string
}{
	{"app/controllers", ColEntry, "Controllers"},
	{"app/api", ColEntry, "API"},
	{"config/routes", ColEntry, "Routing"},
	{"app/models", ColDomain, "Domain models"},
	{"app/validators", ColDomain, "Validators"},
	{"app/services", ColFeature, "Services"},
	{"app/queries", ColFeature, "Queries"},
	{"app/operations", ColFeature, "Operations"},
	{"app/interactors", ColFeature, "Interactors"},
	{"app/policies", ColFeature, "Policies"},
	{"app/forms", ColFeature, "Forms"},
	{"app/presenters", ColFeature, "Presenters"},
	{"app/serializers", ColFeature, "Serializers"},
	{"app/decorators", ColFeature, "Decorators"},
	{"app/jobs", ColInfra, "Background jobs"},
	{"app/mailers", ColInfra, "Mailers"},
	{"app/helpers", ColInfra, "View helpers"},
	{"app/views", ColInfra, "Views"},
	{"app/assets", ColInfra, "Assets"},
	{"app/javascript", ColInfra, "Frontend"},
	// lib/ is a project's own shared code, not plumbing it stands on: Redmine's
	// lib/redmine holds access control, activity streams, field formats and the
	// plugin hook system. Filing it under Infrastructure buried the second
	// largest body of domain logic in the repo.
	{"lib", ColDomain, "Library"},
	{"config", ColInfra, "Configuration"},
	{"db", ColInfra, "Database"},
	{"public", ColInfra, "Static assets"},
	{"extra", ColInfra, "Extras"},
}

// notArchitecture reports whether a path describes the system rather than
// composing it. Excluded from every derivation, conventional or generic:
// including tests buries the real subsystems under the largest directories in
// the repo.
func notArchitecture(path string) bool {
	for _, skip := range []string{"test/", "tests/", "spec/", "doc/", "docs/", "vendor/", "node_modules/", "third_party/"} {
		if strings.HasPrefix(path, skip) {
			return true
		}
	}
	return false
}

// roleFor returns the column and display name for a path, or ok=false when no
// convention matches it.
func roleFor(path string) (column, name string, ok bool) {
	if notArchitecture(path) {
		return "", "", false
	}
	best := -1
	for i, r := range dirRoles {
		if strings.HasPrefix(path, r.prefix+"/") || path == r.prefix {
			if best < 0 || len(r.prefix) > len(dirRoles[best].prefix) {
				best = i
			}
		}
	}
	if best < 0 {
		return "", "", false
	}
	return dirRoles[best].column, dirRoles[best].name, true
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

	for _, f := range code {
		column, name, ok := roleFor(f.Path)
		if !ok {
			continue
		}
		add(name, column, f.Path, groups[name])
	}

	// Fall back for the whole repo rather than per file: mixing a couple of
	// convention hits with directory guesses for everything else would read as
	// one derivation when it is two. Either a layout was recognised or it was
	// not (TDS-67; the lens work generalises this).
	derivation := DerivationConvention
	if len(groups) == 0 {
		derivation = DerivationDirectory
		for _, f := range code {
			name, ok := genericGroup(f.Path)
			if !ok {
				continue
			}
			add(name, genericColumn(name), f.Path, groups[name])
		}
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
