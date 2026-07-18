// Package viewer renders a compiled tour manifest into a self-contained HTML
// page: a two-pane app (narrative rail + code pane) with the manifest, the
// highlighted code, and the viewer CSS/JS all inlined.
//
// Everything is inlined into one document and read from the DOM (never fetched),
// so the page opens directly from disk via file:// with no server and no network
// — consistent with the "shareable artifact" goal (design §8). The build (TDS-22)
// supplies the highlighted code and the highlighter stylesheet.
package viewer

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"

	"github.com/charlesharris/tourdesource/internal/manifest"
)

//go:embed assets/viewer.css
var viewerCSS string

//go:embed assets/viewer.js
var viewerJS string

// Input is everything the viewer needs to render one tour page.
type Input struct {
	Manifest *manifest.Manifest
	// Code maps a file path to its build-time-highlighted HTML (from package
	// highlight). Only files a stop anchors need be present.
	Code map[string]string
	// HighlightCSS is the highlighter's stylesheet (highlight.StylesheetCSS()).
	HighlightCSS string
	// Title overrides the page <title>; defaults to the manifest title.
	Title string
}

// payload is the JSON blob inlined for the viewer script to read.
type payload struct {
	Manifest *manifest.Manifest `json:"manifest"`
	Code     map[string]string  `json:"code"`
}

// Render produces the complete, self-contained index.html for the tour.
func Render(in Input) ([]byte, error) {
	if in.Manifest == nil {
		return nil, fmt.Errorf("viewer: nil manifest")
	}

	title := in.Title
	if title == "" {
		title = in.Manifest.Title
	}
	if title == "" {
		title = "tour-de-source"
	}

	raw, err := json.Marshal(payload{Manifest: in.Manifest, Code: in.Code})
	if err != nil {
		return nil, fmt.Errorf("viewer: encoding data: %w", err)
	}
	// Keep the JSON from breaking out of the <script> element. Replacing "</"
	// with "<\/" is a no-op for JSON semantics ("\/" decodes to "/") but stops a
	// literal </script> (or any close tag) inside prose/code from ending the tag.
	raw = bytes.ReplaceAll(raw, []byte("</"), []byte(`<\/`))

	var b bytes.Buffer
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString(`<meta charset="utf-8">` + "\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", html.EscapeString(title))
	b.WriteString("<style>\n")
	b.WriteString(in.HighlightCSS)
	b.WriteString("\n</style>\n<style>\n")
	b.WriteString(viewerCSS)
	b.WriteString("\n</style>\n</head>\n<body>\n")
	// The static rendering is the page's content with JavaScript disabled, and
	// the script replaces it wholesale on boot. Shipping an empty root would
	// leave a JS-off reader — or a crawler — staring at a blank page, when the
	// narrative is plain prose that needs no interactivity to be worth reading.
	b.WriteString(`<div id="tds-app">` + "\n")
	b.Write(renderStatic(in.Manifest))
	b.WriteString("</div>\n")
	b.WriteString(`<script type="application/json" id="tds-data">`)
	b.Write(raw)
	b.WriteString("</script>\n<script>\n")
	b.WriteString(viewerJS)
	b.WriteString("\n</script>\n</body>\n</html>\n")
	return b.Bytes(), nil
}

// chapterID and stopAnchorID are the fragment ids the outline links to and the
// script restores from, so a shared URL lands on the same place with or without
// JavaScript.
func chapterID(i int) string        { return fmt.Sprintf("chapter-%d", i+1) }
func stopAnchorID(id string) string { return "stop-" + id }

// renderStatic renders the tour as plain, readable HTML: an outline of the
// chapters followed by every chapter's stops in order. It is what a JS-off
// reader sees, and it is deliberately complete rather than a teaser — the prose
// is the tour; the two-pane app is a better way to read it, not the only way.
func renderStatic(m *manifest.Manifest) []byte {
	var b bytes.Buffer

	b.WriteString(`<header class="tds-header">` + "\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(orDefault(m.Title, "Tour")))
	if m.Audience != "" {
		fmt.Fprintf(&b, `<p class="tds-audience">For %s</p>`+"\n", html.EscapeString(m.Audience))
	}
	if m.Commit != "" {
		fmt.Fprintf(&b, `<p class="tds-commit">@ %s</p>`+"\n", html.EscapeString(shortCommit(m.Commit)))
	}
	b.WriteString("</header>\n")

	b.WriteString(`<main class="tds-main"><nav class="tds-rail">` + "\n")
	if m.Intro != "" {
		fmt.Fprintf(&b, `<div class="tds-intro">%s</div>`+"\n", m.Intro)
	}
	b.Write(renderOutline(m))

	for i, ch := range m.Chapters {
		fmt.Fprintf(&b, `<section class="tds-chapter" id="%s">`+"\n", chapterID(i))
		fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(ch.Title))
		if ch.Intro != "" {
			fmt.Fprintf(&b, `<div class="tds-chapter-intro">%s</div>`+"\n", ch.Intro)
		}
		for _, st := range ch.Stops {
			b.Write(renderStaticStop(st))
		}
		b.WriteString("</section>\n")
	}
	b.WriteString("</nav></main>\n")
	return b.Bytes()
}

// renderOutline is the tour's table of contents: one entry per chapter with the
// number of stops it holds. On a whole-project tour the chapters are the
// subsystems, and without this the reader has one long scroll and no way to see
// what the tour covers or jump to the part they came for.
func renderOutline(m *manifest.Manifest) []byte {
	if len(m.Chapters) == 0 {
		return nil
	}
	var b bytes.Buffer
	b.WriteString(`<nav class="tds-toc" id="tds-toc" aria-label="Tour contents">` + "\n")
	b.WriteString(`<h2 class="tds-toc-title">Contents</h2>` + "\n<ol>\n")
	for i, ch := range m.Chapters {
		n := countStops(ch.Stops)
		fmt.Fprintf(&b, `<li><a href="#%s">%s</a> <span class="tds-toc-count">%s</span></li>`+"\n",
			chapterID(i), html.EscapeString(ch.Title), pluralStops(n))
	}
	b.WriteString("</ol>\n</nav>\n")
	return b.Bytes()
}

// countStops counts a chapter's stops including those nested in detours, so the
// outline reflects how much there is to read rather than the top-level count.
func countStops(stops []manifest.Stop) int {
	n := 0
	for _, st := range stops {
		n++
		for _, d := range st.Detours {
			n += countStops(d.Stops)
		}
	}
	return n
}

func renderStaticStop(st manifest.Stop) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, `<article class="tds-stop" id="%s">`+"\n", stopAnchorID(st.ID))

	cls := "tds-stop-loc"
	if !st.Anchor.Resolved {
		cls += " tds-unresolved"
	}
	fmt.Fprintf(&b, `<div class="%s">%s</div>`+"\n", cls, html.EscapeString(staticLocationLabel(st.Anchor)))
	fmt.Fprintf(&b, `<div class="tds-prose">%s</div>`+"\n", st.Prose)

	for _, d := range st.Detours {
		b.WriteString(`<details class="tds-detour">` + "\n")
		fmt.Fprintf(&b, "<summary>%s</summary>\n", html.EscapeString(orDefault(d.Title, "Detour")))
		if d.Intro != "" {
			fmt.Fprintf(&b, `<div class="tds-prose">%s</div>`+"\n", d.Intro)
		}
		for _, ds := range d.Stops {
			b.Write(renderStaticStop(ds))
		}
		b.WriteString("</details>\n")
	}
	b.WriteString("</article>\n")
	return b.Bytes()
}

// staticLocationLabel mirrors the script's locationLabel so both renderings name
// a stop's code the same way.
func staticLocationLabel(a manifest.Anchor) string {
	if a.Symbol != "" {
		return a.Symbol
	}
	if a.Path != "" {
		if a.StartLine == a.EndLine {
			return fmt.Sprintf("%s:%d", a.Path, a.StartLine)
		}
		return fmt.Sprintf("%s:%d-%d", a.Path, a.StartLine, a.EndLine)
	}
	return a.Raw
}

func pluralStops(n int) string {
	if n == 1 {
		return "1 stop"
	}
	return fmt.Sprintf("%d stops", n)
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Assets returns the standalone viewer asset files (name → contents), for a
// build that prefers linking them rather than inlining. Render inlines these
// itself; this is for the directory-bundle layout.
func Assets() map[string][]byte {
	return map[string][]byte{
		"viewer.css": []byte(viewerCSS),
		"viewer.js":  []byte(viewerJS),
	}
}
