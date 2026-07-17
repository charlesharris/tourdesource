// Package manifest compiles a parsed tour plus the repo map into the JSON
// manifest the viewer consumes.
//
// It is the convergence point of the format and the map: it walks the tour AST
// (package tour), resolves every stop's anchor to a concrete line range (package
// anchor), and renders prose Markdown to HTML at build time (so the viewer needs
// no runtime Markdown renderer and stays readable with JS disabled). Anchors
// that don't resolve are carried through flagged, and also collected into
// Warnings, rather than failing the compile — the author fixes them, the reader
// never sees a broken tour silently.
package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/charlesharris/tourdesource/internal/anchor"
	"github.com/charlesharris/tourdesource/internal/tour"
)

// Version is the manifest schema version the viewer checks for compatibility.
const Version = 1

// Manifest is the compiled, viewer-ready tour.
type Manifest struct {
	Version  int               `json:"version"`
	Title    string            `json:"title"`
	Template string            `json:"template,omitempty"`
	Audience string            `json:"audience,omitempty"`
	Repo     string            `json:"repo,omitempty"`
	Commit   string            `json:"commit,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
	Intro    string            `json:"intro,omitempty"` // rendered HTML
	Chapters []Chapter         `json:"chapters"`
	// Warnings collects human-readable notes (e.g. unresolved anchors) surfaced
	// during compilation. Empty on a clean compile.
	Warnings []string `json:"warnings,omitempty"`
}

// Chapter is a named group of stops.
type Chapter struct {
	Title string `json:"title"`
	Intro string `json:"intro,omitempty"` // rendered HTML
	Stops []Stop `json:"stops"`
}

// Stop is one narration unit with a resolved anchor and rendered prose.
type Stop struct {
	ID      string   `json:"id"` // stable id for deep-linking
	Anchor  Anchor   `json:"anchor"`
	Focus   string   `json:"focus,omitempty"` // raw focus hint; resolved by the viewer
	View    string   `json:"view,omitempty"`
	Prose   string   `json:"prose"` // rendered HTML
	Detours []Detour `json:"detours,omitempty"`
}

// Detour is a collapsible side-quest that may contain stops.
type Detour struct {
	Title string `json:"title"`
	Intro string `json:"intro,omitempty"` // rendered HTML
	Stops []Stop `json:"stops"`
}

// Anchor is a stop's resolved (or flagged) code location.
type Anchor struct {
	Raw       string `json:"raw"` // the original anchor string from the tour
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Symbol    string `json:"symbol,omitempty"`
	Kind      string `json:"kind"` // symbol | line-range | unresolved
	Resolved  bool   `json:"resolved"`
	Loose     bool   `json:"loose,omitempty"`  // matched via #/. loose fallback
	Reason    string `json:"reason,omitempty"` // why unresolved
}

// markdown is the shared renderer: CommonMark + GitHub extensions, with raw HTML
// left escaped (prose is authored, but we don't grant it script injection).
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// Compile turns a parsed tour into a manifest, resolving anchors against r and
// rendering prose to HTML. It only errors on a rendering failure; unresolved
// anchors are flagged and collected in Warnings.
func Compile(t *tour.Tour, r *anchor.Resolver) (*Manifest, error) {
	m := &Manifest{
		Version:  Version,
		Title:    t.Title,
		Template: t.Template,
		Audience: t.Audience,
		Repo:     t.Repo,
		Commit:   t.Commit,
		Meta:     t.Meta,
	}
	var err error
	if m.Intro, err = render(t.Intro); err != nil {
		return nil, err
	}

	var warnings []string
	for ci, ch := range t.Chapters {
		mch := Chapter{Title: ch.Title}
		if mch.Intro, err = render(ch.Intro); err != nil {
			return nil, err
		}
		for si, st := range ch.Stops {
			ms, w, err := compileStop(st, r, fmt.Sprintf("c%ds%d", ci+1, si+1))
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, w...)
			mch.Stops = append(mch.Stops, ms)
		}
		m.Chapters = append(m.Chapters, mch)
	}
	m.Warnings = warnings
	return m, nil
}

func compileStop(st tour.Stop, r *anchor.Resolver, id string) (Stop, []string, error) {
	prose, err := render(st.Prose)
	if err != nil {
		return Stop{}, nil, err
	}
	a, warnings := resolveAnchor(st.Anchor, r, id)
	ms := Stop{ID: id, Anchor: a, Focus: st.Focus, View: st.View, Prose: prose}

	for di, dt := range st.Detours {
		md := Detour{Title: dt.Title}
		if md.Intro, err = render(dt.Intro); err != nil {
			return Stop{}, nil, err
		}
		for sj, dst := range dt.Stops {
			dms, w, err := compileStop(dst, r, fmt.Sprintf("%sd%ds%d", id, di+1, sj+1))
			if err != nil {
				return Stop{}, nil, err
			}
			warnings = append(warnings, w...)
			md.Stops = append(md.Stops, dms)
		}
		ms.Detours = append(ms.Detours, md)
	}
	return ms, warnings, nil
}

// resolveAnchor resolves one anchor string, returning the manifest Anchor plus
// any warnings for an unresolved/malformed anchor.
func resolveAnchor(raw string, r *anchor.Resolver, stopID string) (Anchor, []string) {
	res, err := r.Resolve(raw)
	if err != nil {
		reason := err.Error()
		return Anchor{Raw: raw, Kind: string(anchor.KindUnresolved), Reason: reason},
			[]string{fmt.Sprintf("stop %s: unparseable anchor %q: %s", stopID, raw, reason)}
	}

	a := Anchor{
		Raw:       raw,
		Path:      res.Path,
		StartLine: res.StartLine,
		EndLine:   res.EndLine,
		Symbol:    res.Symbol,
		Kind:      string(res.Kind),
		Loose:     res.Loose,
	}
	if res.Kind == anchor.KindUnresolved {
		a.Reason = res.Reason
		return a, []string{fmt.Sprintf("stop %s: %s", stopID, res.Reason)}
	}
	a.Resolved = true
	return a, nil
}

// render converts Markdown prose to HTML. Empty input yields "".
func render(md string) (string, error) {
	if md == "" {
		return "", nil
	}
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(md), &buf); err != nil {
		return "", fmt.Errorf("rendering prose: %w", err)
	}
	return buf.String(), nil
}

// WriteJSON writes the manifest as indented JSON.
func (m *Manifest) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // prose is already HTML; don't double-escape
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	return nil
}
