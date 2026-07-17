// Package builder compiles a tour into a static bundle: it parses the tour,
// resolves anchors against the map, highlights the referenced code from the
// pinned repository snapshot, renders the viewer page, and writes the bundle.
// This is the engine behind `tds build`.
package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charlesharris/tourdesource/internal/anchor"
	"github.com/charlesharris/tourdesource/internal/highlight"
	"github.com/charlesharris/tourdesource/internal/manifest"
	"github.com/charlesharris/tourdesource/internal/repofs"
	"github.com/charlesharris/tourdesource/internal/scan"
	"github.com/charlesharris/tourdesource/internal/store"
	"github.com/charlesharris/tourdesource/internal/tour"
	"github.com/charlesharris/tourdesource/internal/viewer"
)

// Options configure a bundle build.
type Options struct {
	TourPath string                        // path to the *.tour.md
	Repo     string                        // repository root; default "."
	MapPath  string                        // map.sqlite; default <repo>/.tds/map.sqlite
	OutDir   string                        // bundle output dir; default <repo>/.tds/tour
	Warnf    func(format string, a ...any) // non-fatal diagnostics sink
}

// Result summarizes a completed build.
type Result struct {
	BundleDir  string
	IndexPath  string
	Commit     string
	Stops      int
	CodeFiles  int      // files highlighted into the bundle
	EmbedFiles int      // repo files embedded as pinned blobs (0 if not pinned)
	Warnings   []string // manifest + build warnings (e.g. unresolved anchors)
	Unresolved int
}

// Build compiles the tour and writes the bundle. Unresolved anchors and a
// non-git repo are non-fatal (flagged); only missing inputs and I/O errors fail.
func Build(_ context.Context, opts Options) (*Result, error) {
	warnf := opts.Warnf
	if warnf == nil {
		warnf = func(string, ...any) {}
	}

	repo, err := filepath.Abs(orDefault(opts.Repo, "."))
	if err != nil {
		return nil, err
	}
	mapPath := orDefault(opts.MapPath, filepath.Join(repo, ".tds", "map.sqlite"))
	outDir := orDefault(opts.OutDir, filepath.Join(repo, ".tds", "tour"))

	// 1. Parse the tour.
	parsed, err := tour.ParseFile(opts.TourPath)
	if err != nil {
		return nil, err
	}

	// 2. Load the map and build the anchor resolver.
	if _, err := os.Stat(mapPath); err != nil {
		return nil, fmt.Errorf("map not found at %s (run `tds map` first): %w", mapPath, err)
	}
	st, err := store.Open(mapPath)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	symbols, err := st.Symbols()
	if err != nil {
		return nil, err
	}
	resolver := anchor.NewResolver(symbols)
	mapCommit, _ := st.Meta("commit")

	// 3. Compile the manifest (anchors resolved to line ranges).
	m, err := manifest.Compile(parsed, resolver)
	if err != nil {
		return nil, err
	}

	// 4. Read the pinned repo snapshot (fall back to the working tree if the repo
	//    isn't a git repo), and highlight the files the tour references.
	snap, err := repofs.Read(repo, orDefault(mapCommit, "auto"))
	if err != nil {
		warnf("not embedding a pinned snapshot (%v); reading the working tree", err)
		snap = nil
	}
	if snap != nil {
		m.Commit = snap.Commit
	}

	code := map[string]string{}
	for _, path := range referencedPaths(m) {
		src, err := readSource(snap, repo, path)
		if err != nil {
			warnf("skipping code for %s: %v", path, err)
			continue
		}
		hl, err := highlight.Highlight(string(src), scan.DetectLanguage(path))
		if err != nil {
			warnf("highlighting %s: %v", path, err)
			continue
		}
		code[path] = hl.HTML
	}

	// 5. Render the viewer page.
	index, err := viewer.Render(viewer.Input{
		Manifest:     m,
		Code:         code,
		HighlightCSS: highlight.StylesheetCSS(),
	})
	if err != nil {
		return nil, err
	}

	// 6. Write the bundle: index.html (self-contained), manifest.json, and the
	//    pinned repo blobs (for free-browse and as a complete immutable artifact).
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating bundle dir: %w", err)
	}
	indexPath := filepath.Join(outDir, "index.html")
	if err := os.WriteFile(indexPath, index, 0o644); err != nil {
		return nil, fmt.Errorf("writing index.html: %w", err)
	}
	if err := writeManifest(m, filepath.Join(outDir, "manifest.json")); err != nil {
		return nil, err
	}
	embedFiles := 0
	if snap != nil {
		if _, err := snap.WriteBlobs(filepath.Join(outDir, "repo")); err != nil {
			warnf("embedding repo blobs: %v", err)
		} else {
			embedFiles = len(snap.Files)
		}
	}

	return &Result{
		BundleDir:  outDir,
		IndexPath:  indexPath,
		Commit:     m.Commit,
		Stops:      countStops(m),
		CodeFiles:  len(code),
		EmbedFiles: embedFiles,
		Warnings:   m.Warnings,
		Unresolved: len(m.Warnings),
	}, nil
}

// referencedPaths returns the distinct file paths of resolved anchors, in order.
func referencedPaths(m *manifest.Manifest) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(stops []manifest.Stop)
	walk = func(stops []manifest.Stop) {
		for _, s := range stops {
			if s.Anchor.Resolved && s.Anchor.Path != "" && !seen[s.Anchor.Path] {
				seen[s.Anchor.Path] = true
				out = append(out, s.Anchor.Path)
			}
			for _, d := range s.Detours {
				walk(d.Stops)
			}
		}
	}
	for _, ch := range m.Chapters {
		walk(ch.Stops)
	}
	return out
}

func countStops(m *manifest.Manifest) int {
	n := 0
	var walk func(stops []manifest.Stop)
	walk = func(stops []manifest.Stop) {
		for _, s := range stops {
			n++
			for _, d := range s.Detours {
				walk(d.Stops)
			}
		}
	}
	for _, ch := range m.Chapters {
		walk(ch.Stops)
	}
	return n
}

// readSource reads a file's bytes, preferring the pinned snapshot.
func readSource(snap *repofs.Snapshot, repo, path string) ([]byte, error) {
	if snap != nil {
		return snap.Content(path)
	}
	return os.ReadFile(filepath.Join(repo, filepath.FromSlash(path)))
}

func writeManifest(m *manifest.Manifest, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	return m.WriteJSON(f)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
