package lens

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed lenses/*.toml
var builtinFS embed.FS

// Builtins loads the lens definitions embedded in the binary. These are the
// common ecosystems: they must work with no runtime and no install, which is
// why they live in the core rather than in providers.
func Builtins() (*Set, error) {
	return loadFS(builtinFS, "lenses")
}

func loadFS(fsys fs.FS, dir string) (*Set, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	set := &Set{byName: map[string]*Lens{}}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var file struct {
			Lens map[string]*Lens `toml:"lens"`
		}
		if err := toml.Unmarshal(b, &file); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		for name, l := range file.Lens {
			if _, dup := set.byName[name]; dup {
				return nil, fmt.Errorf("lens %q defined twice", name)
			}
			l.Name = name
			set.byName[name] = l
		}
	}
	if err := set.resolve(); err != nil {
		return nil, err
	}
	return set, nil
}

// Detect finds which lenses apply to a repository and where.
//
// Every lens is tested at every directory that could root it, so a marker deep
// in the tree activates a scoped instance: providers/ruby/Gemfile makes the Ruby
// lens apply to providers/ruby and nothing else.
//
// paths is the repo-relative path of every file, as the map already has them —
// detection needs no filesystem access, which keeps it testable and lets it run
// against a pinned snapshot rather than the working tree.
func Detect(set *Set, paths []string) []Instance {
	present := make(map[string]bool, len(paths))
	dirs := map[string]bool{"": true}
	for _, p := range paths {
		present[p] = true
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}

	var out []Instance
	for _, name := range set.Names() {
		l := set.byName[name]
		if len(l.Detect.All) == 0 && len(l.Detect.Any) == 0 {
			continue // never activates by detection (generic, user-forced)
		}
		for dir := range dirs {
			if matches(l, dir, present) {
				out = append(out, Instance{Lens: l, Root: dir})
			}
		}
	}

	// Deepest root first, then priority, then name: the order Resolve relies on.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if da, db := depth(a.Root), depth(b.Root); da != db {
			return da > db
		}
		if a.Lens.Priority != b.Lens.Priority {
			return a.Lens.Priority > b.Lens.Priority
		}
		return a.Lens.Name < b.Lens.Name
	})
	return out
}

func matches(l *Lens, dir string, present map[string]bool) bool {
	at := func(marker string) bool {
		want := marker
		if dir != "" {
			want = dir + "/" + marker
		}
		if !strings.ContainsAny(marker, "*?[") {
			return present[want]
		}
		// A glob marker — `*.gemspec`, `*.csproj` — matches any file in that
		// directory. path.Match does not cross separators, so this stays scoped.
		for p := range present {
			if ok, err := path.Match(want, p); err == nil && ok {
				return true
			}
		}
		return false
	}
	for _, m := range l.Detect.All {
		if !at(m) {
			return false
		}
	}
	if len(l.Detect.Any) > 0 {
		for _, m := range l.Detect.Any {
			if at(m) {
				return true
			}
		}
		return false
	}
	return len(l.Detect.All) > 0
}

func depth(root string) int {
	if root == "" {
		return 0
	}
	return strings.Count(root, "/") + 1
}

// Resolve picks the instance that governs a path.
//
// The most specific scope wins — longest root, then priority, then name — so a
// file under providers/ruby resolves to the Ruby lens even though the Go lens
// at the repository root also covers it. Instances must be sorted as Detect
// leaves them.
func Resolve(instances []Instance, p string) (Instance, bool) {
	for _, in := range instances {
		if in.Covers(p) {
			return in, true
		}
	}
	return Instance{}, false
}
