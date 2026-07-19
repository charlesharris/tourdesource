package site

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

//go:embed all:theme
var themeFS embed.FS

// minHugoVersion is what the theme needs: GroupByParam and transform.Highlight
// are extended-only, and the templates assume 0.128 semantics.
const minHugoVersion = "0.128.0"

// Options configure a site build.
type Options struct {
	// OutDir receives the finished site.
	OutDir string
	// WorkDir holds the generated Hugo project. Defaults to a temp dir that is
	// removed afterwards; set it to inspect or iterate on the generated input.
	WorkDir string
	// KeepProject leaves the generated Hugo project in place, so the theme can
	// be iterated on with `hugo server` against real data.
	KeepProject bool
	// HugoPath overrides the hugo binary.
	HugoPath string

	// MaxSymbols bounds the symbol index. The full set for a large repo is tens
	// of thousands, which makes the page unusable rather than thorough.
	MaxSymbols int

	Warnf func(format string, a ...any)
	Logf  func(format string, a ...any)
}

// Result summarizes a completed site build.
type Result struct {
	OutDir      string
	ProjectDir  string
	Pages       int
	Subsystems  int
	Symbols     int
	TourStops   int
	HugoVersion string
}

// Build emits the Hugo project for a tour and renders it.
func Build(ctx context.Context, in Input, opts Options) (*Result, error) {
	warnf, logf := opts.Warnf, opts.Logf
	if warnf == nil {
		warnf = func(string, ...any) {}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opts.MaxSymbols <= 0 {
		opts.MaxSymbols = 2000
	}

	hugo, version, err := findHugo(opts.HugoPath)
	if err != nil {
		return nil, err
	}
	logf("using %s (%s)", hugo, version)

	workDir := opts.WorkDir
	if workDir == "" {
		dir, err := os.MkdirTemp("", "tds-site-*")
		if err != nil {
			return nil, err
		}
		if !opts.KeepProject {
			defer os.RemoveAll(dir)
		}
		workDir = dir
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating project dir: %w", err)
	}

	if err := writeTheme(workDir); err != nil {
		return nil, err
	}
	res, err := writeData(in, workDir, opts)
	if err != nil {
		return nil, err
	}

	outDir, err := filepath.Abs(opts.OutDir)
	if err != nil {
		return nil, err
	}
	logf("rendering %d pages with hugo", res.Pages)

	// Not --quiet: it suppresses the error text too, turning a template or
	// front-matter failure into a bare "exit status 1".
	cmd := exec.CommandContext(ctx, hugo, "--destination", outDir, "--cleanDestinationDir", "--logLevel", "warn")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("hugo build failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	res.OutDir = outDir
	res.ProjectDir = workDir
	res.HugoVersion = version
	if opts.KeepProject {
		logf("kept the generated Hugo project at %s (run `hugo server` there to iterate on the theme)", workDir)
	}
	return res, nil
}

// findHugo locates a usable hugo binary and checks it is new enough. A missing
// or old hugo is reported with what to do about it, not as a bare exec error.
func findHugo(override string) (path, version string, err error) {
	bin := override
	if bin == "" {
		bin = "hugo"
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		return "", "", fmt.Errorf(
			"hugo is required to build the site format but was not found on PATH.\n"+
				"Install it (macOS: `brew install hugo`, or https://gohugo.io/installation/) — "+
				"the extended build of %s or newer is needed.\n"+
				"The single-file bundle format needs no external tools.", minHugoVersion)
	}
	out, err := exec.Command(resolved, "version").Output()
	if err != nil {
		return "", "", fmt.Errorf("running `%s version`: %w", resolved, err)
	}
	version = strings.TrimSpace(string(out))

	if !strings.Contains(version, "extended") {
		return "", "", fmt.Errorf(
			"this hugo is not the extended build, which the theme requires:\n  %s\n"+
				"Install the extended build (macOS: `brew install hugo`).", version)
	}
	if v := hugoSemver(version); v != "" && compareVersions(v, minHugoVersion) < 0 {
		return "", "", fmt.Errorf("hugo %s is older than the required %s:\n  %s", v, minHugoVersion, version)
	}
	return resolved, version, nil
}

var hugoVersionRe = regexp.MustCompile(`v(\d+\.\d+\.\d+)`)

func hugoSemver(s string) string {
	if m := hugoVersionRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, y := 0, 0
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// writeTheme unpacks the embedded theme into the project directory.
func writeTheme(dir string) error {
	return fs.WalkDir(themeFS, "theme", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, "theme")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := themeFS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// writeData emits data/*.json and one content page per source file.
func writeData(in Input, dir string, opts Options) (*Result, error) {
	subs, subsystemOf := DeriveSubsystems(in.Files, in.Symbols, in.Imports, in.Signals, in.Entrypoints)
	refs := ReferenceCounts(in.Symbols, in.Imports)
	importedBy := InvertImports(in.Imports)

	manifestJSON := buildManifest(in, subs, columnsFor(subs))
	tour := buildTour(in.Manifest)
	symbols := buildSymbols(in, subsystemOf, refs, opts.MaxSymbols)

	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	for name, payload := range map[string]any{
		"manifest.json": manifestJSON,
		"tour.json":     tour,
		"symbols.json":  symbols,
	} {
		if err := os.WriteFile(filepath.Join(dataDir, name), mustJSON(payload), 0o644); err != nil {
			return nil, fmt.Errorf("writing data/%s: %w", name, err)
		}
	}

	pages, err := writeFilePages(in, dir, subsystemOf, importedBy, tour)
	if err != nil {
		return nil, err
	}

	stops := 0
	walkSiteStops(tour, func(TourStop) { stops++ })
	return &Result{
		Pages:      pages,
		Subsystems: len(subs),
		Symbols:    len(symbols.Symbols),
		TourStops:  stops,
	}, nil
}

// writeFilePages writes content/files/<slug>.md for every source file.
func writeFilePages(
	in Input, dir string,
	subsystemOf map[string]string,
	importedBy map[string][]string,
	tour SiteTour,
) (int, error) {
	filesDir := filepath.Join(dir, "content", "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return 0, err
	}

	churn := map[string]store.GitSignal{}
	for _, s := range in.Signals {
		churn[s.Path] = s
	}
	symbolsByPath := map[string][]protocol.Symbol{}
	for _, s := range in.Symbols {
		symbolsByPath[s.Path] = append(symbolsByPath[s.Path], s)
	}
	importsByPath := map[string][]string{}
	for _, im := range in.Imports {
		importsByPath[im.Path] = append(importsByPath[im.Path], im.Target)
	}
	// Which tour stops touch a file, so a file page can point back at the tour.
	// Detour stops count too — a file reached only by a side-quest is still
	// visited by the tour, and its page should say so.
	stopsByPath := map[string][]string{}
	firstHL := map[string]string{}
	walkSiteStops(tour, func(s TourStop) {
		stopsByPath[s.File] = append(stopsByPath[s.File], s.ID)
		if _, ok := firstHL[s.File]; !ok {
			firstHL[s.File] = s.HL
		}
	})

	now := time.Now()
	count := 0
	for _, f := range in.Files {
		// Only files with code get a page: the explorer is for reading source,
		// and a page per binary asset is noise.
		if f.Language == "" {
			continue
		}
		src, ok := in.Source[f.Path]
		if !ok {
			continue
		}

		var syms []PageSymbol
		for _, s := range symbolsByPath[f.Path] {
			syms = append(syms, PageSymbol{Name: s.Symbol, Kind: s.Kind})
		}
		imps := dedupe(importsByPath[f.Path])
		sort.Strings(imps)

		page := FilePage{
			Title:      f.Path,
			Path:       f.Path,
			Folder:     folderOf(f.Path),
			Lang:       f.Language,
			Lines:      strings.Count(src, "\n") + 1,
			Commits:    churn[f.Path].Churn,
			Touched:    humanAge(churn[f.Path].LastCommit, now),
			Subsystem:  subsystemOf[f.Path],
			HL:         firstHL[f.Path],
			Symbols:    syms,
			Imports:    imps,
			ImportedBy: importedBy[f.Path],
			TourStops:  stopsByPath[f.Path],
			Code:       src,
		}

		name := filepath.Join(filesDir, slugFor(f.Path)+".md")
		if err := os.WriteFile(name, renderFrontmatter(page), 0o644); err != nil {
			return count, fmt.Errorf("writing %s: %w", name, err)
		}
		count++
	}
	return count, nil
}

// renderFrontmatter writes the page as YAML frontmatter.
//
// Custom fields go under `params:` rather than at the top level. Hugo removed
// `path` and `lang` as front-matter keys in 0.144 (they were built-ins), and
// emitting them at the top level is a hard error on a current Hugo. Nesting
// keeps `.Params.path` resolving exactly as before, so the theme's templates
// need no change.
//
// Written by hand rather than with a YAML library because the one field that
// matters is `code`, a literal block whose content must survive byte for byte —
// a marshaller would fold, quote or re-indent source and silently corrupt it.
func renderFrontmatter(p FilePage) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlString(p.Title))
	b.WriteString("params:\n")
	fmt.Fprintf(&b, "  path: %s\n", yamlString(p.Path))
	fmt.Fprintf(&b, "  folder: %s\n", yamlString(p.Folder))
	fmt.Fprintf(&b, "  lang: %s\n", yamlString(p.Lang))
	fmt.Fprintf(&b, "  lines: %d\n", p.Lines)
	fmt.Fprintf(&b, "  commits: %d\n", p.Commits)
	if p.Touched != "" {
		fmt.Fprintf(&b, "  touched: %s\n", yamlString(p.Touched))
	}
	if p.Subsystem != "" {
		fmt.Fprintf(&b, "  subsystem: %s\n", yamlString(p.Subsystem))
	}
	if p.HL != "" {
		fmt.Fprintf(&b, "  hl: %s\n", yamlString(p.HL))
	}
	if p.Summary != "" {
		fmt.Fprintf(&b, "  summary: %s\n", yamlString(p.Summary))
	}

	if len(p.Symbols) > 0 {
		b.WriteString("  symbols:\n")
		for _, s := range p.Symbols {
			fmt.Fprintf(&b, "    - name: %s\n      kind: %s\n", yamlString(s.Name), yamlString(s.Kind))
		}
	}
	writeList(&b, "imports", p.Imports)
	writeList(&b, "importedBy", p.ImportedBy)
	writeList(&b, "tourStops", p.TourStops)

	// The literal block scalar keeps the source verbatim. Every line is indented
	// under the key; blank lines stay blank so the indentation is not a lie.
	b.WriteString("  code: |\n")
	for _, line := range strings.Split(strings.TrimRight(p.Code, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("---\n")
	return []byte(b.String())
}

func writeList(b *strings.Builder, key string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s:\n", key)
	for _, it := range items {
		fmt.Fprintf(b, "    - %s\n", yamlString(it))
	}
}

// yamlString quotes a scalar so punctuation cannot be read as YAML syntax.
func yamlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func folderOf(path string) string {
	if i := strings.LastIndex(path, "/"); i > 0 {
		return path[:i]
	}
	return "."
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
