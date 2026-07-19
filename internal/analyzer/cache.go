package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/provider"
	"github.com/charlesharris/tourdesource/internal/store"
)

// The findings cache (TDS-26).
//
// Analyzers run against the working tree, not the commit the map was built at,
// so the cache is keyed by file content rather than by commit: a dirty checkout
// must not be served answers computed for a clean one.
//
// Caching is per analyzer, not per provider, because it is only sound for
// analyzers whose findings about a file depend on nothing but that file.
// Brakeman's verdict on a controller changes when a model changes, and sorbet's
// when anything in the type graph does; caching those per file would serve a
// stale answer wearing a current one's clothes. The provider says which of its
// analyzers qualify (AnalyzerInfo.Incremental), and the answer defaults to no.

// hashFiles reads and hashes each path. A file that cannot be read gets no
// entry, which makes it a permanent cache miss — the safe direction.
func hashFiles(root string, paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		out[p] = hex.EncodeToString(sum[:])
	}
	return out
}

// plan is one analyzer's share of a provider's work.
type plan struct {
	info protocol.AnalyzerInfo
	// stale are the files this analyzer must actually be run over. For a
	// whole-program analyzer it is every file, always.
	stale []string
	// cached are findings served without running anything.
	cached []protocol.Finding
	// hits counts files answered from cache.
	hits int
}

// planAnalyzers decides, for each of a provider's analyzers, what still needs
// running and what the cache already knows.
func planAnalyzers(
	st *store.Store,
	p *provider.Provider,
	files []string,
	hashes map[string]string,
	only []string,
	noCache bool,
) []plan {
	var out []plan
	for _, a := range p.Caps.Analyzers {
		if !a.Available || !wanted(a.Name, only) {
			continue
		}
		pl := plan{info: a}
		// A whole-program analyzer, an uncacheable one, or --no-cache: run it
		// over everything.
		if !a.Incremental || noCache || a.ToolVersion == "" {
			pl.stale = files
			out = append(out, pl)
			continue
		}
		for _, f := range files {
			h, ok := hashes[f]
			if !ok {
				pl.stale = append(pl.stale, f)
				continue
			}
			hit, found := st.CachedFindings(a.Tool, a.ToolVersion, f, h)
			if !found {
				pl.stale = append(pl.stale, f)
				continue
			}
			pl.cached = append(pl.cached, hit...)
			pl.hits++
		}
		out = append(out, pl)
	}
	return out
}

// recordCache stores a completed analyzer run's findings per file, so the next
// run can skip the files that did not change.
//
// Every file that was analyzed gets an entry, including files with no findings:
// a clean file that is never cached is re-analyzed forever, which is most of
// them and therefore most of the cost.
func recordCache(
	st *store.Store,
	pl plan,
	findings []protocol.Finding,
	hashes map[string]string,
	warnf func(string, ...any),
) {
	if !pl.info.Incremental || pl.info.ToolVersion == "" {
		return
	}
	// Only this analyzer's findings. One call can carry several analyzers, and
	// storing the whole batch under each one's key makes every cached file
	// return the union — which reads as correct until the second run, when the
	// findings silently double.
	byPath := make(map[string][]protocol.Finding, len(pl.stale))
	for _, f := range findings {
		if f.Tool != pl.info.Tool {
			continue
		}
		byPath[f.Path] = append(byPath[f.Path], f)
	}
	for _, f := range pl.stale {
		h, ok := hashes[f]
		if !ok {
			continue
		}
		if err := st.PutCachedFindings(pl.info.Tool, pl.info.ToolVersion, f, h, byPath[f]); err != nil {
			warnf("caching %s findings for %s: %v", pl.info.Name, f, err)
			return // one failure means the store is unhappy; stop hammering it
		}
	}
}

func wanted(name string, only []string) bool {
	if len(only) == 0 {
		return true
	}
	for _, n := range only {
		if n == name {
			return true
		}
	}
	return false
}

// group is a set of analyzers that need the same files, so they can share one
// provider call.
type group struct {
	stale []string
	plans []plan
}

func (g group) names() []string {
	out := make([]string, 0, len(g.plans))
	for _, p := range g.plans {
		out = append(out, p.info.Name)
	}
	return out
}

// groupByStale buckets plans by the exact file set they still need. Plans with
// nothing left to do are dropped: their answers came from the cache.
func groupByStale(plans []plan) []group {
	var out []group
	for _, pl := range plans {
		if len(pl.stale) == 0 {
			continue
		}
		key := strings.Join(pl.stale, "\x00")
		placed := false
		for i := range out {
			if strings.Join(out[i].stale, "\x00") == key {
				out[i].plans = append(out[i].plans, pl)
				placed = true
				break
			}
		}
		if !placed {
			out = append(out, group{stale: pl.stale, plans: []plan{pl}})
		}
	}
	return out
}
