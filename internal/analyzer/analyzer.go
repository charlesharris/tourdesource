// Package analyzer runs the language providers' analyzers over a mapped
// repository, resolves each finding to a symbol using the map, and writes the
// results to the store with provenance. This is the engine behind `tds analyze`
// (TDS-30).
//
// It reads the map rather than rebuilding it: findings are only meaningful at
// the commit the map was built from, so the map's commit is the run's commit and
// a mismatch is refused rather than silently producing findings pinned to the
// wrong source.
package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/charlesharris/tourdesource/internal/config"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/provider"
	"github.com/charlesharris/tourdesource/internal/store"
)

// Options configure an analyze run.
type Options struct {
	Root   string // repository root
	MapDir string // directory holding map.sqlite; default <root>/.tds
	Commit string // expected commit; default is whatever the map was built at

	// Analyzers optionally restricts which analyzers run, by name. Empty runs
	// everything each provider offers. A non-empty list here (from `--analyzer`)
	// wins over tds.toml's [analyze].enable.
	Analyzers []string
	// Disable removes analyzers by name, applied after the allowlist. From
	// tds.toml's [analyze].disable — "run everything except this."
	Disable []string
	// Config is opaque, provider-interpreted configuration keyed by provider
	// name, sourced from tds.toml (TDS-27).
	Config map[string]json.RawMessage

	// NoCache forces every analyzer to re-run, ignoring the findings cache.
	NoCache bool

	// Timeout bounds each provider request. Analysis is far slower than
	// structure extraction — brakeman alone walks the whole application — so
	// this defaults to DefaultTimeout rather than the provider package's much
	// shorter handshake budget.
	Timeout time.Duration

	Warnf func(format string, a ...any) // non-fatal diagnostics sink
}

// DefaultTimeout bounds one provider's analyze request. Whole-program scanners
// are minutes-scale on a large repository: Redmine's 1,109 Ruby files exceed the
// provider package's 30-second default before rubocop has finished, and a
// timeout there reads as "no findings", which is the worst possible failure.
const DefaultTimeout = 15 * time.Minute

// AnalyzerRun records what one advertised analyzer did, so the caller can report
// what ran and — just as usefully — what would have run had its tool been
// installed.
type AnalyzerRun struct {
	Provider    string
	Name        string
	Tool        string
	ToolVersion string
	Available   bool
	Findings    int
}

// Result summarizes a completed analyze run.
type Result struct {
	Root       string
	Commit     string
	SQLitePath string
	Providers  []string // providers whose analyze run succeeded
	// Attempted lists providers that advertised analyze and were asked. It
	// differs from Providers when a run failed or timed out — the difference
	// between "nothing here can analyze" and "the thing that can, broke".
	Attempted  []string
	Analyzers  []AnalyzerRun
	Findings   int
	Resolved   int // findings attributed to a symbol
	Unresolved int // findings that landed outside any known symbol
	// CacheHits counts (analyzer, file) pairs served without running a tool.
	CacheHits int
}

// Run executes the analyze pipeline against an existing map and returns a
// summary. A provider that fails, times out, or reports a per-analyzer error is
// non-fatal (reported via Warnf); a missing or stale map is fatal, because
// findings resolved against the wrong source are worse than no findings.
func Run(ctx context.Context, opts Options) (*Result, error) {
	warnf := opts.Warnf
	if warnf == nil {
		warnf = func(string, ...any) {}
	}

	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}

	// tds.toml narrows what runs. An explicit --analyzer (opts.Analyzers) wins
	// over [analyze].enable; disable and per-provider config come from the file.
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	if len(opts.Analyzers) == 0 {
		opts.Analyzers = cfg.Analyze.Enable
	}
	// Disable is a denylist, so the flag adds to the file rather than replacing
	// it: --disable is "also skip this," not "skip only this."
	opts.Disable = append(append([]string{}, opts.Disable...), cfg.Analyze.Disable...)
	if opts.Config == nil {
		if opts.Config, err = cfg.ProviderConfigJSON(); err != nil {
			return nil, err
		}
	}

	mapDir := opts.MapDir
	if mapDir == "" {
		mapDir = filepath.Join(root, ".tds")
	}
	sqlitePath := filepath.Join(mapDir, "map.sqlite")
	if _, err := os.Stat(sqlitePath); err != nil {
		return nil, fmt.Errorf("no map at %s: run `tds map` first", sqlitePath)
	}

	st, err := store.Open(sqlitePath)
	if err != nil {
		return nil, err
	}
	defer st.Close()

	commit, err := st.Meta("commit")
	if err != nil {
		return nil, fmt.Errorf("reading the map's commit: %w", err)
	}
	// Findings carry line numbers, so they are only valid against the exact
	// source the map indexed. Analyzing a different commit would silently
	// attribute findings to the wrong lines.
	if opts.Commit != "" && commit != "" && opts.Commit != commit {
		return nil, fmt.Errorf(
			"map is stale: built at commit %s but analyzing %s; re-run `tds map`",
			short(commit), short(opts.Commit))
	}
	if commit == "" {
		commit = opts.Commit
	}

	files, err := st.Files()
	if err != nil {
		return nil, fmt.Errorf("reading mapped files: %w", err)
	}
	symbols, err := st.Symbols()
	if err != nil {
		return nil, fmt.Errorf("reading mapped symbols: %w", err)
	}
	index := newSymbolIndex(symbols)

	specs, err := provider.Discover(root)
	if err != nil {
		warnf("provider discovery: %v", err)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	host := provider.Open(ctx, specs, provider.Options{Warnf: warnf, Timeout: timeout})
	defer host.Close()

	var (
		findings  []protocol.Finding
		runs      []AnalyzerRun
		providers []string
		attempted []string
		cacheHits int
	)

	for _, p := range host.Providers() {
		if !supportsAnalyze(p.Caps) {
			continue
		}
		batch := filesFor(p, host, files)
		if len(batch) == 0 {
			continue
		}
		attempted = append(attempted, p.Spec.Name)

		// Analyzers are grouped by the file set they still need, so identical
		// work shares one call: with nothing changed the incremental analyzers
		// have empty sets and are not called at all, while the whole-program
		// ones share a single call over everything — which is exactly the
		// shape of the request before caching existed.
		hashes := hashFiles(root, batch)
		plans := planAnalyzers(st, p, batch, hashes, opts.Analyzers, opts.Disable, opts.NoCache)
		ok := false

		// This provider's own findings, so the per-analyzer tally is not
		// polluted by another provider that reports the same tool name.
		var mine []protocol.Finding
		for _, pl := range plans {
			cacheHits += pl.hits
			mine = append(mine, pl.cached...)
			if pl.hits > 0 {
				ok = true
			}
		}

		for _, grp := range groupByStale(plans) {
			res, err := p.Analyze(ctx, protocol.AnalyzeParams{
				Root:      root,
				Commit:    commit,
				Files:     grp.stale,
				Analyzers: grp.names(),
				Config:    opts.Config[p.Spec.Name],
			})
			if err != nil {
				warnf("provider %q analyze failed: %v", p.Spec.Name, err)
				continue
			}
			for _, ae := range res.AnalyzerErrors {
				warnf("provider %q analyzer %q: %s", p.Spec.Name, ae.Analyzer, ae.Message)
			}
			for _, pl := range grp.plans {
				recordCache(st, pl, res.Findings, hashes, warnf)
			}
			mine = append(mine, res.Findings...)
			ok = true
		}

		findings = append(findings, mine...)
		if ok {
			runs = append(runs, analyzerRuns(p, mine, opts.Analyzers, opts.Disable)...)
		}
		if ok {
			providers = append(providers, p.Spec.Name)
		}
	}

	// Attribute each finding to the innermost symbol containing it, unless the
	// provider already resolved it — the provider knows its own language best.
	var resolved int
	for i := range findings {
		if findings[i].Symbol == "" {
			findings[i].Symbol = index.resolve(findings[i].Path, findings[i].StartLine)
		}
		if findings[i].Symbol != "" {
			resolved++
		}
	}

	// An analyze run replaces the previous one rather than accumulating on it.
	if err := st.ClearFindings(); err != nil {
		return nil, err
	}
	if err := st.PutFindings(findings); err != nil {
		return nil, err
	}
	if err := exportJSON(st, filepath.Join(mapDir, "map.json")); err != nil {
		return nil, err
	}

	sort.Strings(providers)
	sort.Strings(attempted)
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Provider != runs[j].Provider {
			return runs[i].Provider < runs[j].Provider
		}
		return runs[i].Name < runs[j].Name
	})

	return &Result{
		Root:       root,
		Commit:     commit,
		SQLitePath: sqlitePath,
		Providers:  providers,
		Attempted:  attempted,
		Analyzers:  runs,
		Findings:   len(findings),
		Resolved:   resolved,
		Unresolved: len(findings) - resolved,
		CacheHits:  cacheHits,
	}, nil
}

// supportsAnalyze reports whether a provider advertises the analyze op. The
// tree-sitter fallback, for instance, serves structure only.
func supportsAnalyze(caps protocol.Capabilities) bool {
	for _, op := range caps.Operations {
		if op == protocol.OpAnalyze {
			return true
		}
	}
	return false
}

// filesFor returns the mapped files this provider owns: those whose language it
// claims and for which it is the provider the host would route to. Routing
// through the host keeps the precedence rule in one place, so a provider never
// analyzes files a higher-priority provider is responsible for.
func filesFor(p *provider.Provider, host *provider.Host, files []store.File) []string {
	var batch []string
	for _, f := range files {
		if f.Language == "" {
			continue
		}
		if host.ForLanguage(f.Language) == p {
			batch = append(batch, f.Path)
		}
	}
	return batch
}

// analyzerRuns pairs a provider's advertised analyzers with the findings they
// produced. An analyzer whose tool isn't installed is still reported, with zero
// findings, so the caller can tell "clean" from "never ran".
func analyzerRuns(p *provider.Provider, findings []protocol.Finding, only, deny []string) []AnalyzerRun {
	requested := map[string]bool{}
	for _, name := range only {
		requested[name] = true
	}

	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Tool]++
	}

	var runs []AnalyzerRun
	for _, a := range p.Caps.Analyzers {
		if len(requested) > 0 && !requested[a.Name] {
			continue
		}
		// A disabled analyzer is off, not skipped: it should not appear in the
		// summary at all.
		if denied(a.Name, deny) {
			continue
		}
		runs = append(runs, AnalyzerRun{
			Provider:    p.Spec.Name,
			Name:        a.Name,
			Tool:        a.Tool,
			ToolVersion: a.ToolVersion,
			Available:   a.Available,
			Findings:    counts[a.Tool],
		})
	}
	return runs
}

// symbolIndex resolves a file position to the symbol that contains it.
type symbolIndex map[string][]protocol.Symbol

func newSymbolIndex(symbols []protocol.Symbol) symbolIndex {
	ix := symbolIndex{}
	for _, s := range symbols {
		ix[s.Path] = append(ix[s.Path], s)
	}
	return ix
}

// resolve returns the qualified name of the innermost symbol spanning line, or
// "" when the line falls outside every known symbol — a finding on an import
// block or a bare config file has no symbol to blame, and saying so beats
// attributing it to the enclosing class.
func (ix symbolIndex) resolve(path string, line int) string {
	var best protocol.Symbol
	found := false
	for _, s := range ix[path] {
		if line < s.StartLine || line > s.EndLine {
			continue
		}
		// Innermost wins: the narrowest span containing the line.
		if !found || (s.EndLine-s.StartLine) < (best.EndLine-best.StartLine) {
			best, found = s, true
		}
	}
	if !found {
		return ""
	}
	return best.Symbol
}

// exportJSON refreshes the JSON export so it reflects the findings just written.
func exportJSON(st *store.Store, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	return st.ExportJSON(f)
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
