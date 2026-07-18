package site

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charlesharris/tourdesource/internal/anchor"
	"github.com/charlesharris/tourdesource/internal/manifest"
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
	skipped := 0
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
		source[f.Path] = string(src)
	}
	if skipped > 0 {
		warnf("%d file(s) larger than %d bytes have pages without code", skipped, opts.MaxSourceBytes)
	}

	return Input{
		Manifest:    m,
		Files:       files,
		Symbols:     symbols,
		Imports:     imports,
		Signals:     signals,
		Entrypoints: entrypoints,
		Source:      source,
		ProjectName: filepath.Base(repo),
		Blurb:       blurbFrom(m),
		Commit:      commit,
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

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
