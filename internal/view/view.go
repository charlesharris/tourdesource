// Package view groups analyzer findings into the toggleable layers the site
// renders over a tour (design §9.5).
//
// A view is `{id, title, kind, provenance, findings}`. Views are derived from
// findings already in the store rather than recomputed, so what a reader sees is
// exactly what `tds analyze` recorded, pinned to the same commit.
//
// Provenance is not decoration. A view asserts things about code, and the reader
// needs to know which tool at which version said so, at which commit — otherwise
// a finding is an anonymous accusation.
package view

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charlesharris/tourdesource/internal/protocol"
)

// View is one toggleable layer of findings.
type View struct {
	ID         string             `json:"id"`
	Title      string             `json:"title"`
	Kind       string             `json:"kind"` // protocol.View* constants
	Provenance Provenance         `json:"provenance"`
	Findings   []protocol.Finding `json:"findings"`
	// Counts summarise the findings by severity so a switcher can show weight
	// without loading the whole list.
	Counts Counts `json:"counts"`
	// Files is set for heatmap views only: one row per file, ranked by its worst
	// measurement. A heatmap is a measurement rather than a list of defects, and
	// flog emits a score per method — 1,390 of them on Redmine — so the per-file
	// aggregate is the readable unit. Computed here rather than in the template
	// because it is data processing, and templates are bad at it.
	Files []HeatFile `json:"files,omitempty"`
}

// HeatFile is one file's worth of a heatmap view.
type HeatFile struct {
	Path string `json:"path"`
	// Peak is the highest value measured in the file, and what it is ranked by.
	Peak float64 `json:"peak"`
	// Entries is how many measurements the file contributed.
	Entries int `json:"entries"`
	// Pct is Peak as a percentage of the highest peak in the view, so the
	// template can draw a bar without knowing the scale of the underlying
	// metric — flog scores and coverage percentages are not comparable.
	Pct int `json:"pct"`
}

// Provenance says who produced a view and against what.
type Provenance struct {
	Tool        string `json:"tool"`
	ToolVersion string `json:"tool_version,omitempty"`
	Commit      string `json:"commit,omitempty"`
}

// Counts is a severity tally.
type Counts struct {
	Total    int `json:"total"`
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Info     int `json:"info"`
}

// Severity classes. Kept in step with internal/draft: a tool's own vocabulary
// varies, and the reader should see one scale.
func classify(sev string) string {
	switch strings.ToLower(sev) {
	case "error", "critical", "high", "fatal":
		return "error"
	case "warning", "medium", "warn":
		return "warning"
	default:
		return "info"
	}
}

// Build groups findings into views, one per (tool, kind) pair.
//
// A tool may emit more than one kind — brakeman contributes both inline
// annotations and a browsable panel — so the pair is the identity, not the tool
// alone. Views with no findings are not produced: an empty layer in a switcher
// is a promise the data does not keep.
func Build(findings []protocol.Finding, commit string) []View {
	if len(findings) == 0 {
		return nil
	}
	type key struct{ tool, kind string }
	groups := map[key][]protocol.Finding{}
	versions := map[string]string{}

	for _, f := range findings {
		kind := f.View
		if !validKind(kind) {
			// A provider that omits or mistypes the kind still has something
			// worth showing; a panel is the least presumptuous default.
			kind = protocol.ViewPanel
		}
		k := key{tool: f.Tool, kind: kind}
		groups[k] = append(groups[k], f)
		if f.ToolVersion != "" {
			versions[f.Tool] = f.ToolVersion
		}
	}

	out := make([]View, 0, len(groups))
	for k, fs := range groups {
		sort.SliceStable(fs, func(i, j int) bool {
			if fs[i].Path != fs[j].Path {
				return fs[i].Path < fs[j].Path
			}
			return fs[i].StartLine < fs[j].StartLine
		})
		v := View{
			ID:    id(k.tool, k.kind),
			Title: title(k.tool, k.kind),
			Kind:  k.kind,
			Provenance: Provenance{
				Tool: k.tool, ToolVersion: versions[k.tool], Commit: commit,
			},
			Findings: fs,
		}
		for _, f := range fs {
			v.Counts.Total++
			switch classify(f.Severity) {
			case "error":
				v.Counts.Errors++
			case "warning":
				v.Counts.Warnings++
			default:
				v.Counts.Info++
			}
		}
		if v.Kind == protocol.ViewHeatmap {
			v.Files = heatFiles(fs)
		}
		out = append(out, v)
	}

	// Most serious first, so a switcher's default order matches what matters.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Counts.Errors != b.Counts.Errors {
			return a.Counts.Errors > b.Counts.Errors
		}
		if a.Counts.Warnings != b.Counts.Warnings {
			return a.Counts.Warnings > b.Counts.Warnings
		}
		return a.ID < b.ID
	})
	return out
}

// maxHeatFiles bounds a heatmap table. The tail of a complexity ranking is
// every ordinary method in the repository, which is a map of nothing; each
// file's own page carries its full scores regardless.
const maxHeatFiles = 60

// heatFiles aggregates a heatmap view's findings to one row per file, ranked by
// the worst measurement in each.
func heatFiles(fs []protocol.Finding) []HeatFile {
	peak := map[string]float64{}
	count := map[string]int{}
	for _, f := range fs {
		count[f.Path]++
		if f.Value != nil && *f.Value > peak[f.Path] {
			peak[f.Path] = *f.Value
		}
	}
	out := make([]HeatFile, 0, len(peak))
	for p, v := range peak {
		out = append(out, HeatFile{Path: p, Peak: v, Entries: count[p]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Peak != out[j].Peak {
			return out[i].Peak > out[j].Peak
		}
		return out[i].Path < out[j].Path
	})
	if len(out) == 0 {
		return nil
	}
	// Bars are relative to the view's own ceiling: a flog score and a coverage
	// percentage share no scale, so an absolute one would be meaningless.
	ceiling := out[0].Peak
	if ceiling <= 0 {
		ceiling = 1
	}
	for i := range out {
		out[i].Pct = int(out[i].Peak / ceiling * 100)
	}
	if len(out) > maxHeatFiles {
		out = out[:maxHeatFiles]
	}
	return out
}

func validKind(k string) bool {
	switch k {
	case protocol.ViewAnnotations, protocol.ViewHeatmap, protocol.ViewPanel, protocol.ViewBadge:
		return true
	}
	return false
}

func id(tool, kind string) string {
	return strings.ToLower(tool + "-" + kind)
}

func title(tool, kind string) string {
	switch kind {
	case protocol.ViewAnnotations:
		return fmt.Sprintf("%s annotations", tool)
	case protocol.ViewHeatmap:
		return fmt.Sprintf("%s heatmap", tool)
	case protocol.ViewBadge:
		return fmt.Sprintf("%s badges", tool)
	default:
		return fmt.Sprintf("%s findings", tool)
	}
}

// ByPath indexes every view's findings by file, for the per-file annotations a
// file page renders. Findings keep their view id so a page can say which layer
// each came from.
func ByPath(views []View) map[string][]protocol.Finding {
	out := map[string][]protocol.Finding{}
	for _, v := range views {
		for _, f := range v.Findings {
			out[f.Path] = append(out[f.Path], f)
		}
	}
	for p := range out {
		fs := out[p]
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].StartLine < fs[j].StartLine })
		out[p] = fs
	}
	return out
}

// BySymbol indexes findings by the symbol they were attributed to, for the
// badge a tour stop shows against its anchor.
func BySymbol(views []View) map[string][]protocol.Finding {
	out := map[string][]protocol.Finding{}
	for _, v := range views {
		for _, f := range v.Findings {
			if f.Symbol == "" {
				continue
			}
			out[f.Symbol] = append(out[f.Symbol], f)
		}
	}
	return out
}
