// Package mapper builds the repo "map": it walks the repository, gathers git
// signals, drives the language providers for structure, and writes the result
// to a SQLite store plus a JSON export. This is the engine behind `tds map`.
package mapper

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charlesharris/tourdesource/internal/gitsignals"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/provider"
	"github.com/charlesharris/tourdesource/internal/scan"
	"github.com/charlesharris/tourdesource/internal/store"
)

// Options configure a map build.
type Options struct {
	Root   string                        // repository root (or a subdirectory)
	OutDir string                        // output dir for map.sqlite/map.json; default <root>/.tds
	Commit string                        // pinned commit; default resolves HEAD
	Warnf  func(format string, a ...any) // non-fatal diagnostics sink
}

// Result summarizes a completed build.
type Result struct {
	Root        string
	Commit      string
	SQLitePath  string
	JSONPath    string
	Providers   []string
	Languages   map[string]int
	Files       int
	Symbols     int
	Imports     int
	Entrypoints int
}

// Build runs the full map pipeline and writes the store + JSON export. Missing
// providers, a non-git root, and per-file provider errors are non-fatal
// (reported via Warnf); only structural failures (bad root, store I/O) error.
func Build(ctx context.Context, opts Options) (*Result, error) {
	warnf := opts.Warnf
	if warnf == nil {
		warnf = func(string, ...any) {}
	}

	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", opts.Root)
	}

	outDir := opts.OutDir
	if outDir == "" {
		outDir = filepath.Join(root, ".tds")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	commit := opts.Commit
	if commit == "" {
		commit = resolveHead(root)
	}

	// 1. Enumerate files (respecting .gitignore).
	files, err := scan.Scan(root)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	// 2. Git signals (best effort).
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	signals, err := gitsignals.Compute(root, paths)
	if err != nil {
		warnf("git signals unavailable: %v", err)
	}

	// 3. Providers → structure, batched per provider by language.
	specs, err := provider.Discover(root)
	if err != nil {
		warnf("provider discovery: %v", err)
	}
	host := provider.Open(ctx, specs, provider.Options{Warnf: warnf})
	defer host.Close()

	batches := map[*provider.Provider][]string{}
	for _, f := range files {
		if f.Language == "" {
			continue
		}
		if p := host.ForLanguage(f.Language); p != nil {
			batches[p] = append(batches[p], f.Path)
		}
	}

	var symbols []protocol.Symbol
	var imports []protocol.Import
	var entrypoints []protocol.Entrypoint
	for p, batch := range batches {
		res, err := p.Structure(ctx, protocol.StructureParams{Root: root, Commit: commit, Files: batch})
		if err != nil {
			warnf("provider %q structure failed: %v", p.Spec.Name, err)
			continue
		}
		symbols = append(symbols, res.Symbols...)
		imports = append(imports, res.Imports...)
		entrypoints = append(entrypoints, res.Entrypoints...)
		for _, fe := range res.FileErrors {
			warnf("%s: %s", fe.Path, fe.Message)
		}
	}

	// 4. Persist to a fresh store + JSON export.
	sqlitePath := filepath.Join(outDir, "map.sqlite")
	_ = os.Remove(sqlitePath) // the map is regenerated each run
	st, err := store.Open(sqlitePath)
	if err != nil {
		return nil, err
	}
	defer st.Close()

	if err := persist(st, root, commit, files, signals, symbols, imports, entrypoints); err != nil {
		return nil, err
	}

	jsonPath := filepath.Join(outDir, "map.json")
	if err := exportJSON(st, jsonPath); err != nil {
		return nil, err
	}

	languages := map[string]int{}
	for _, f := range files {
		languages[f.Language]++
	}
	providerNames := make([]string, 0, len(host.Providers()))
	for _, p := range host.Providers() {
		providerNames = append(providerNames, p.Spec.Name)
	}
	sort.Strings(providerNames)

	return &Result{
		Root:        root,
		Commit:      commit,
		SQLitePath:  sqlitePath,
		JSONPath:    jsonPath,
		Providers:   providerNames,
		Languages:   languages,
		Files:       len(files),
		Symbols:     len(symbols),
		Imports:     len(imports),
		Entrypoints: len(entrypoints),
	}, nil
}

func persist(
	st *store.Store, root, commit string,
	files []scan.File, signals map[string]gitsignals.Signal,
	symbols []protocol.Symbol, imports []protocol.Import, entrypoints []protocol.Entrypoint,
) error {
	storeFiles := make([]store.File, len(files))
	for i, f := range files {
		storeFiles[i] = store.File{Path: f.Path, Language: f.Language, Size: f.Size}
	}
	if err := st.PutFiles(storeFiles); err != nil {
		return err
	}

	if len(signals) > 0 {
		gs := make([]store.GitSignal, 0, len(signals))
		for _, s := range signals {
			gs = append(gs, store.GitSignal{
				Path:        s.Path,
				Churn:       s.Churn,
				FirstCommit: s.FirstCommit.Format(time.RFC3339),
				LastCommit:  s.LastCommit.Format(time.RFC3339),
				AgeDays:     s.AgeDays,
				Authors:     s.Authors,
			})
		}
		if err := st.PutGitSignals(gs); err != nil {
			return err
		}
	}

	if err := st.PutSymbols(symbols); err != nil {
		return err
	}
	if err := st.PutImports(imports); err != nil {
		return err
	}
	if err := st.PutEntrypoints(entrypoints); err != nil {
		return err
	}

	if err := st.SetMeta("root", root); err != nil {
		return err
	}
	if err := st.SetMeta("commit", commit); err != nil {
		return err
	}
	return nil
}

func exportJSON(st *store.Store, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	if err := st.ExportJSON(f); err != nil {
		return err
	}
	return nil
}

// resolveHead returns the current commit SHA, or "" if root is not a git repo.
func resolveHead(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
