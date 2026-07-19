package lens

import "testing"

func builtins(t *testing.T) *Set {
	t.Helper()
	set, err := Builtins()
	if err != nil {
		t.Fatalf("loading built-in lenses: %v", err)
	}
	return set
}

// TestBuiltinsLoad is the guard that matters most: the lens data is embedded
// TOML, so a typo in it is a runtime failure rather than a compile error.
func TestBuiltinsLoad(t *testing.T) {
	set := builtins(t)
	for _, want := range []string{"generic", "ruby", "rails"} {
		if _, ok := set.Get(want); !ok {
			t.Errorf("built-in lens %q missing; have %v", want, set.Names())
		}
	}
}

func TestExtendsInheritsRolesAndIgnores(t *testing.T) {
	set := builtins(t)
	rails, _ := set.Get("rails")

	// Inherited from ruby.
	if col, name, ok := rails.RoleFor("lib/redmine/access_control.rb"); !ok || name != "Library" || col != ColumnDomain {
		t.Errorf("rails should inherit ruby's lib/ role, got %q %q %v", col, name, ok)
	}
	if !rails.IsIgnored("test/unit/issue_test.rb") {
		t.Error("rails should inherit ruby's test/ ignore")
	}
	// Its own.
	if !rails.IsIgnored("db/schema.rb") {
		t.Error("rails declares db/schema.rb ignored")
	}
	if col, name, ok := rails.RoleFor("app/controllers/issues_controller.rb"); !ok || name != "Controllers" || col != ColumnEntry {
		t.Errorf("app/controllers = %q %q %v", col, name, ok)
	}
}

// TestLongestRoleWins pins the rule that lets a specific rule override a
// general one — app/models/concerns can be placed apart from app/models.
func TestLongestRoleWins(t *testing.T) {
	l := &Lens{Roles: []Role{
		{Path: "app", Column: ColumnInfra, Name: "App"},
		{Path: "app/models", Column: ColumnDomain, Name: "Domain models"},
	}}
	if _, name, _ := l.RoleFor("app/models/issue.rb"); name != "Domain models" {
		t.Errorf("longest match should win, got %q", name)
	}
	if _, name, _ := l.RoleFor("app/other/thing.rb"); name != "App" {
		t.Errorf("shorter match should apply elsewhere, got %q", name)
	}
}

func TestPerSegmentNamesEachChild(t *testing.T) {
	set := builtins(t)
	g, _ := set.Get("generic")

	col, name, ok := g.RoleFor("internal/site/data.go")
	if !ok || name != "site" {
		t.Errorf("internal/site/data.go = %q %q %v, want a node named \"site\"", col, name, ok)
	}
	if col != ColumnModules {
		t.Errorf("column = %q, want %q — no role was derived for a Go package", col, ColumnModules)
	}
	if _, name, _ := g.RoleFor("internal/cli/build.go"); name != "cli" {
		t.Errorf("sibling package should be its own node, got %q", name)
	}
	// A file sitting directly in the container, not in a package under it.
	if _, name, ok := g.RoleFor("internal/doc.go"); !ok || name != "internal" {
		t.Errorf("internal/doc.go = %q %v, want the container itself", name, ok)
	}
}

func TestIgnoredPathsHaveNoRole(t *testing.T) {
	set := builtins(t)
	g, _ := set.Get("generic")
	for _, p := range []string{"test/x_test.go", "node_modules/left-pad/index.js", "docs/design.md"} {
		if _, _, ok := g.RoleFor(p); ok {
			t.Errorf("%s should be ignored, not placed", p)
		}
	}
}

// TestDetectScopesToMarkerDirectory is the case this repository is: a Go
// project containing a real Ruby gem under providers/.
func TestDetectScopesToMarkerDirectory(t *testing.T) {
	set := builtins(t)
	paths := []string{
		"go.mod",
		"main.go",
		"internal/site/data.go",
		"providers/ruby/Gemfile",
		"providers/ruby/lib/tds/structure.rb",
	}
	got := Detect(set, paths)

	if len(got) != 1 {
		t.Fatalf("want one instance (ruby at providers/ruby), got %d: %+v", len(got), got)
	}
	if got[0].Lens.Name != "ruby" || got[0].Root != "providers/ruby" {
		t.Fatalf("instance = %s @ %q, want ruby @ providers/ruby", got[0].Lens.Name, got[0].Root)
	}
	if !got[0].Covers("providers/ruby/lib/tds/structure.rb") {
		t.Error("the instance should cover files beneath its root")
	}
	if got[0].Covers("internal/site/data.go") {
		t.Error("a scoped instance must not cover the rest of the repository")
	}
	if rel := got[0].Rel("providers/ruby/lib/tds/structure.rb"); rel != "lib/tds/structure.rb" {
		t.Errorf("Rel = %q, want the path relative to the lens root", rel)
	}
}

// TestDetectPrefersFrameworkOverLanguage is Redmine: both ruby and rails match
// at the root, and priority decides.
func TestDetectPrefersFrameworkOverLanguage(t *testing.T) {
	set := builtins(t)
	paths := []string{"Gemfile", "config/routes.rb", "app/models/issue.rb"}
	got := Detect(set, paths)

	if len(got) != 2 {
		t.Fatalf("both ruby and rails should match at the root, got %+v", got)
	}
	in, ok := Resolve(got, "app/models/issue.rb")
	if !ok || in.Lens.Name != "rails" {
		t.Fatalf("resolved to %+v, want rails to outrank ruby at the same root", in)
	}
}

// TestResolvePrefersLongestRoot pins the scoping rule directly.
func TestResolvePrefersLongestRoot(t *testing.T) {
	set := builtins(t)
	paths := []string{
		"Gemfile", "config/routes.rb", // rails @ root
		"engines/billing/Gemfile", // ruby @ engines/billing
		"engines/billing/lib/billing.rb",
		"app/models/issue.rb",
	}
	got := Detect(set, paths)

	in, ok := Resolve(got, "engines/billing/lib/billing.rb")
	if !ok || in.Root != "engines/billing" {
		t.Errorf("nested instance should win, resolved to %+v", in)
	}
	in, ok = Resolve(got, "app/models/issue.rb")
	if !ok || in.Root != "" || in.Lens.Name != "rails" {
		t.Errorf("root file should resolve to the root instance, got %+v", in)
	}
}

func TestResolveFindsNothingWhenNoLensMatches(t *testing.T) {
	set := builtins(t)
	got := Detect(set, []string{"main.go", "internal/site/data.go"})
	if len(got) != 0 {
		t.Fatalf("a Go repo matches no built-in lens yet, got %+v", got)
	}
	if _, ok := Resolve(got, "main.go"); ok {
		t.Error("Resolve should report nothing, leaving the caller to use generic")
	}
}

// TestGlobMarkers — a gem is identified by `*.gemspec`, not by a fixed name,
// so detection has to handle patterns and must not let them cross directories.
func TestGlobMarkers(t *testing.T) {
	set := builtins(t)
	got := Detect(set, []string{
		"providers/ruby/tds-provider-ruby.gemspec",
		"providers/ruby/lib/tds.rb",
		"main.go",
	})
	if len(got) != 1 || got[0].Lens.Name != "ruby" || got[0].Root != "providers/ruby" {
		t.Fatalf("a *.gemspec should root the ruby lens at its own directory, got %+v", got)
	}

	// The pattern must not match through a separator: a gemspec nested deeper
	// should not activate a lens at the shallower directory.
	got = Detect(set, []string{"a/b/thing.gemspec"})
	for _, in := range got {
		if in.Root == "a" {
			t.Errorf("glob matched across a separator: instance rooted at %q", in.Root)
		}
	}
}

func TestExtendsCycleIsReported(t *testing.T) {
	set := &Set{byName: map[string]*Lens{
		"a": {Name: "a", Extends: "b"},
		"b": {Name: "b", Extends: "a"},
	}}
	if err := set.resolve(); err == nil {
		t.Error("a cycle should be an error, not a hang")
	}
}

func TestExtendsUnknownIsReported(t *testing.T) {
	set := &Set{byName: map[string]*Lens{"a": {Name: "a", Extends: "nope"}}}
	if err := set.resolve(); err == nil {
		t.Error("extending a lens that does not exist should be an error")
	}
}
