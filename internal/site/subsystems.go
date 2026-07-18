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
)

func columns() []string { return []string{ColEntry, ColFeature, ColDomain, ColInfra} }

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
	{"app/services", ColFeature, "Services"},
	{"app/queries", ColFeature, "Queries"},
	{"app/jobs", ColInfra, "Background jobs"},
	{"app/mailers", ColInfra, "Mailers"},
	{"app/helpers", ColInfra, "View helpers"},
	{"app/views", ColInfra, "Views"},
	{"app/assets", ColInfra, "Assets"},
	{"app/javascript", ColInfra, "Frontend"},
	{"lib", ColInfra, "Library"},
	{"config", ColInfra, "Configuration"},
	{"db", ColInfra, "Database"},
	{"public", ColInfra, "Static assets"},
	{"extra", ColInfra, "Extras"},
}

// roleFor returns the column and display name for a path, or ok=false when the
// path belongs to nothing worth showing (tests, docs, vendored code).
func roleFor(path string) (column, name string, ok bool) {
	// Tests and docs are excluded from the architecture map: they describe the
	// system rather than compose it, and including them buries the real
	// subsystems under the largest directories in the repo.
	for _, skip := range []string{"test/", "spec/", "doc/", "docs/", "vendor/", "node_modules/"} {
		if strings.HasPrefix(path, skip) {
			return "", "", false
		}
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

// DeriveSubsystems groups the mapped files into architecture nodes.
func DeriveSubsystems(
	files []store.File,
	symbols []protocol.Symbol,
	imports []protocol.Import,
	signals []store.GitSignal,
	entrypoints []protocol.Entrypoint,
) ([]Subsystem, map[string]string) {
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

	for _, f := range files {
		// Only code composes a subsystem; a YAML locale file is not architecture.
		if f.Language == "" || f.Language == "markdown" || f.Language == "json" {
			continue
		}
		column, name, ok := roleFor(f.Path)
		if !ok {
			continue
		}
		g := groups[name]
		if g == nil {
			g = &group{name: name, column: column}
			groups[name] = g
		}
		g.paths = append(g.paths, f.Path)
		g.commits += churn[f.Path].Churn
		subsystemOf[f.Path] = name
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
	for i, c := range columns() {
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
	return out, subsystemOf
}

// describeSubsystem is the placeholder description used until the narrate pass
// replaces it. It states only what tds actually measured — inventing a purpose
// here is exactly the kind of confident-but-wrong text the draft avoids.
func describeSubsystem(name string, files, commits int) string {
	return fmt.Sprintf("%d files, %s commits. Description not yet written — run `tds draft --narrate` to have these named and described.",
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
