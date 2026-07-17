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
	b.WriteString(`<div id="tds-app"></div>` + "\n")
	b.WriteString(`<script type="application/json" id="tds-data">`)
	b.Write(raw)
	b.WriteString("</script>\n<script>\n")
	b.WriteString(viewerJS)
	b.WriteString("\n</script>\n</body>\n</html>\n")
	return b.Bytes(), nil
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
