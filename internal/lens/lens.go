// Package lens holds what tds knows about a kind of project.
//
// A lens is declarative data: which directories play which architectural role,
// what is not architecture at all, and in time which analyzers and entrypoint
// conventions apply. Recognising a new ecosystem is a data file rather than
// another branch in a Go function.
//
// The rule the package turns on:
//
//	Lenses interpret paths. Providers interpret code.
//
// A lens needs no runtime, so a Rails architecture map does not require Ruby to
// be installed. A provider parses source and does. See docs/lenses.md.
package lens

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Column identifiers. These are the lens-facing names; the site maps them onto
// its display headings.
const (
	ColumnEntry   = "entry"
	ColumnFeature = "feature"
	ColumnDomain  = "domain"
	ColumnModules = "modules"
	ColumnInfra   = "infra"
)

// Generic is the name of the built-in lens that applies when nothing else
// matches. It claims only what is near-universal.
const Generic = "generic"

// Lens is one project shape.
type Lens struct {
	Name string `toml:"-"`
	// Extends names a lens whose rules are inherited, then overridden.
	Extends string `toml:"extends"`
	// Priority breaks ties when two lenses match at the same root; higher wins.
	// Frameworks should outrank the languages they are written in.
	Priority int `toml:"priority"`
	// Detect holds the markers that activate this lens.
	Detect Markers `toml:"detect"`
	// Ignore lists path prefixes that describe the system rather than compose
	// it — tests, docs, vendored code.
	Ignore []string `toml:"ignore"`
	// Roles map a directory to its architectural role.
	Roles []Role `toml:"role"`
}

// Markers declare when a lens applies. All of All must be present; any one of
// Any suffices. A lens with neither never activates by detection, which is how
// the generic fallback and user-forced lenses are expressed.
type Markers struct {
	All []string `toml:"all"`
	Any []string `toml:"any"`
}

// Role assigns a directory an architectural role.
type Role struct {
	// Path is relative to the lens root, e.g. "app/controllers".
	Path string `toml:"path"`
	// Column is one of the Column* constants.
	Column string `toml:"column"`
	// Name is the subsystem's display name. Ignored when PerSegment is set,
	// since each child directory names itself.
	Name string `toml:"name"`
	// PerSegment yields one subsystem per child directory rather than one for
	// the whole tree: `internal/` in a Go repo holds fifteen packages, and
	// collapsing them into a node called "internal" helps nobody.
	PerSegment bool `toml:"per-segment"`
}

// Instance is a lens activated at a particular directory. Lenses are scoped
// rather than global: this repository is a Go project that contains a real Ruby
// gem under providers/, so "which lens" is a question about a path, not a repo.
type Instance struct {
	Lens *Lens
	// Root is the directory the marker matched, relative to the repo root, or
	// "" for the repository root itself.
	Root string
}

// Covers reports whether the instance's scope contains a path.
func (in Instance) Covers(p string) bool {
	if in.Root == "" {
		return true
	}
	return strings.HasPrefix(p, in.Root+"/")
}

// Rel converts a repo-relative path into a path relative to the lens root.
func (in Instance) Rel(p string) string {
	if in.Root == "" {
		return p
	}
	return strings.TrimPrefix(p, in.Root+"/")
}

// Set is a resolved collection of lenses, keyed by name, with inheritance
// already applied.
type Set struct {
	byName map[string]*Lens
}

// Get returns a lens by name.
func (s *Set) Get(name string) (*Lens, bool) {
	l, ok := s.byName[name]
	return l, ok
}

// Names returns every lens name, sorted.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.byName))
	for n := range s.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// resolve applies Extends, so callers never have to walk the chain. A lens
// inherits its parent's ignores and roles; its own roles take precedence
// because path resolution prefers the longest match, and equal-length matches
// prefer the child.
func (s *Set) resolve() error {
	// Depth-first with a visiting set, so a cycle is reported rather than
	// hanging the build.
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("lens %q: extends cycle", name)
		case 2:
			return nil
		}
		state[name] = 1
		l := s.byName[name]
		if l.Extends != "" {
			parent, ok := s.byName[l.Extends]
			if !ok {
				return fmt.Errorf("lens %q extends unknown lens %q", name, l.Extends)
			}
			if err := visit(l.Extends); err != nil {
				return err
			}
			l.Ignore = append(append([]string{}, parent.Ignore...), l.Ignore...)
			l.Roles = append(append([]Role{}, parent.Roles...), l.Roles...)
		}
		state[name] = 2
		return nil
	}
	for _, n := range s.Names() {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}

// IsIgnored reports whether a lens-relative path describes the system rather
// than composing it.
func (l *Lens) IsIgnored(rel string) bool {
	for _, ig := range l.Ignore {
		ig = strings.TrimSuffix(ig, "/")
		if rel == ig || strings.HasPrefix(rel, ig+"/") {
			return true
		}
	}
	return false
}

// RoleFor returns the subsystem column and name for a lens-relative path.
//
// The longest matching Path wins, so a rule for `app/models/concerns` can place
// files differently from one for `app/models`. Among equal-length matches the
// last declared wins, which is what makes an inheriting lens able to override
// the lens it extends.
func (l *Lens) RoleFor(rel string) (column, name string, ok bool) {
	if l.IsIgnored(rel) {
		return "", "", false
	}
	best, bestLen := -1, -2
	for i, r := range l.Roles {
		rp := strings.TrimSuffix(r.Path, "/")
		n := roleSpecificity(rp, rel)
		if n < 0 || n < bestLen {
			continue
		}
		// Ties go to the later rule, which is what lets a lens override the one
		// it extends.
		if n >= bestLen {
			best, bestLen = i, n
		}
	}
	if best < 0 {
		return "", "", false
	}
	r := l.Roles[best]
	if r.PerSegment {
		return r.Column, perSegmentName(strings.TrimSuffix(r.Path, "/"), rel), true
	}
	return r.Column, r.Name, true
}

// roleSpecificity scores how well a role path matches, or -1 for no match.
// Longer prefixes are more specific; "*" is the catch-all and always loses to a
// real prefix; "." matches only files sitting directly at the lens root.
func roleSpecificity(rolePath, rel string) int {
	switch rolePath {
	case "*":
		return 0
	case ".", "":
		if strings.Contains(rel, "/") {
			return -1
		}
		return 1
	}
	if rel != rolePath && !strings.HasPrefix(rel, rolePath+"/") {
		return -1
	}
	return len(rolePath) + 1
}

// perSegmentName names one node per child of a container directory.
//
// The name is the child's full path, not its last segment: `internal/site` and
// `pkg/site` are different packages, and naming both "site" would silently merge
// them into one subsystem.
func perSegmentName(rolePath, rel string) string {
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	depth := 1
	if rolePath != "*" && rolePath != "." && rolePath != "" {
		depth = strings.Count(rolePath, "/") + 2
	}
	parts := strings.Split(dir, "/")
	if len(parts) > depth {
		parts = parts[:depth]
	}
	return strings.Join(parts, "/")
}
