package site

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charlesharris/tourdesource/internal/anchor"
	"github.com/charlesharris/tourdesource/internal/manifest"
	"github.com/charlesharris/tourdesource/internal/narration"
	"github.com/charlesharris/tourdesource/internal/repofs"
	"github.com/charlesharris/tourdesource/internal/store"
	"github.com/charlesharris/tourdesource/internal/tour"
)

// FromMapOptions locate the inputs for a site build.
type FromMapOptions struct {
	TourPath string // the *.tour.md
	Repo     string // repository root
	MapPath  string // map.sqlite; default <repo>/.tds/map.sqlite
	// MaxSourceBytes caps how large a file may be before its page ships without
	// code. A generated 5MB asset would otherwise dominate the site.
	MaxSourceBytes int64
	Warnf          func(format string, a ...any)
}

// LoadInput assembles a site Input from a tour file and its map, reading source
// from the pinned commit so the site matches what the map indexed.
func LoadInput(opts FromMapOptions) (Input, error) {
	warnf := opts.Warnf
	if warnf == nil {
		warnf = func(string, ...any) {}
	}
	if opts.MaxSourceBytes <= 0 {
		opts.MaxSourceBytes = 512 * 1024
	}

	repo, err := filepath.Abs(orDefault(opts.Repo, "."))
	if err != nil {
		return Input{}, err
	}
	mapPath := orDefault(opts.MapPath, filepath.Join(repo, ".tds", "map.sqlite"))
	if _, err := os.Stat(mapPath); err != nil {
		return Input{}, fmt.Errorf("map not found at %s (run `tds map` first)", mapPath)
	}

	st, err := store.Open(mapPath)
	if err != nil {
		return Input{}, err
	}
	defer st.Close()

	files, err := st.Files()
	if err != nil {
		return Input{}, err
	}
	symbols, err := st.Symbols()
	if err != nil {
		return Input{}, err
	}
	imports, err := st.Imports()
	if err != nil {
		return Input{}, err
	}
	// Findings are optional: a repo that has never been analyzed still builds,
	// it just ships no views.
	findings, err := st.Findings()
	if err != nil {
		return Input{}, fmt.Errorf("reading findings: %w", err)
	}
	entrypoints, err := st.Entrypoints()
	if err != nil {
		return Input{}, err
	}
	signals, err := st.GitSignals()
	if err != nil {
		return Input{}, err
	}
	commit, _ := st.Meta("commit")

	parsed, err := tour.ParseFile(opts.TourPath)
	if err != nil {
		return Input{}, err
	}
	m, err := manifest.Compile(parsed, anchor.NewResolver(symbols))
	if err != nil {
		return Input{}, err
	}

	// Read from the pinned snapshot so the site shows the source the map was
	// built from, not whatever the working tree happens to hold now.
	snap, err := repofs.Read(repo, orDefault(commit, "auto"))
	if err != nil {
		warnf("not reading a pinned snapshot (%v); using the working tree", err)
		snap = nil
	} else {
		commit = snap.Commit
	}

	source := make(map[string]string, len(files))
	skipped, minified := 0, 0
	for _, f := range files {
		if f.Language == "" {
			continue
		}
		if f.Size > opts.MaxSourceBytes {
			skipped++
			continue
		}
		src, err := readSource(snap, repo, f.Path)
		if err != nil {
			continue
		}
		// Binary content would corrupt the YAML block scalar and render as
		// noise; a page without code is better than a broken one.
		if !isText(src) {
			continue
		}
		// Minified bundles are the worst case for highlighting: Chroma wraps
		// every token in a span, so Redmine's 342KB jquery-ui produced a 3.1MB
		// page — under the byte cap, but nobody reads a tour of it anyway.
		if isMinified(src) {
			minified++
			continue
		}
		source[f.Path] = string(src)
	}
	if skipped > 0 {
		warnf("%d file(s) larger than %d bytes have pages without code", skipped, opts.MaxSourceBytes)
	}
	if minified > 0 {
		warnf("%d minified file(s) have pages without code", minified)
	}

	// Narration is optional: a repository that has never been narrated still
	// builds, its subsystems just describe themselves by what was measured.
	// A malformed sidecar is worth a warning rather than a failed build — the
	// site is still correct without it.
	narrated, err := narration.Load(narration.Path(filepath.Dir(mapPath)))
	if err != nil {
		warnf("not using narration (%v); subsystems will show what tds measured", err)
		narrated = nil
	}

	return Input{
		Manifest:    m,
		Files:       files,
		Symbols:     symbols,
		Imports:     imports,
		Signals:     signals,
		Entrypoints: entrypoints,
		Findings:    findings,
		Source:      source,
		ProjectName: filepath.Base(repo),
		Blurb:       blurbFrom(m),
		Commit:      commit,
		Narration:   narrated,
	}, nil
}

// blurbFrom takes the tour's own introduction as the site's blurb: the author
// already wrote a description of the project there.
func blurbFrom(m *manifest.Manifest) string {
	if s := htmlToText(m.Intro); s != "" {
		return s
	}
	return ""
}

func readSource(snap *repofs.Snapshot, repo, path string) ([]byte, error) {
	if snap != nil {
		if b, err := snap.Content(path); err == nil {
			return b, nil
		}
	}
	return os.ReadFile(filepath.Join(repo, filepath.FromSlash(path)))
}

// isText rejects content with NUL bytes, the cheap and reliable binary tell.
func isText(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	for _, c := range b[:n] {
		if c == 0 {
			return false
		}
	}
	return true
}

// minifiedAvgLineBytes is the average line length above which a file is treated
// as machine-generated. Hand-written code sits around 30 bytes/line across every
// language in Redmine; the minified bundles there average 14,000 and 42,000. The
// gap is wide enough that the exact threshold does not matter much — this one
// leaves two orders of magnitude of headroom for long-lined but authored code.
const minifiedAvgLineBytes = 500

// isMinified reports whether b looks machine-generated rather than authored.
//
// It measures average line length rather than matching paths: `*.min.js` and
// `vendor/` miss plenty of real cases — Redmine's worst offender is committed as
// `app/assets/javascripts/jquery-3.7.1-ui-1.13.3.js`, which no path rule would
// catch — and would wrongly strip readable vendored code that is fine to show.
func isMinified(b []byte) bool {
	if len(b) < minifiedAvgLineBytes {
		return false
	}
	lines := bytes.Count(b, []byte{'\n'}) + 1
	return len(b)/lines > minifiedAvgLineBytes
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
