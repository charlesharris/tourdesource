// Package highlight renders source code to pre-tokenized, class-based HTML at
// build time (design §6.4 step 3).
//
// The bundle ships highlighted HTML plus one shared stylesheet, so there is no
// runtime syntax highlighting: the viewer serves static markup and lets the CSS
// do the styling. Output is class-based (never inline styles) and per-line
// addressable — each source line is wrapped in its own element carrying an
// id="L<n>" anchor — so the viewer can scroll to and highlight a resolved line
// range.
//
// Highlighting is best-effort: an unknown language falls back to plaintext
// rather than failing, so a bundle never breaks on a language chroma can't
// tokenize.
package highlight

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// DefaultStyle is the chroma style used for the shared stylesheet and the token
// classes emitted by Highlight. "github" is a light, high-contrast theme that
// reads well for embedded code snippets.
const DefaultStyle = "github"

// lineIDPrefix prefixes the per-line anchor id (e.g. id="L12"). The viewer
// resolves a 1-based line number N to the element with id "L"+N.
const lineIDPrefix = "L"

// languageAliases maps the language names used across tourdesource to the
// canonical chroma lexer name/alias. Entries are lowercased on lookup; a
// language absent here is still passed to lexers.Get, which understands many
// aliases on its own.
var languageAliases = map[string]string{
	"ruby":       "ruby",
	"javascript": "javascript",
	"typescript": "typescript",
	"go":         "go",
	"python":     "python",
	"markdown":   "markdown",
	"json":       "json",
	"yaml":       "yaml",
	"html":       "html",
	"css":        "css",
}

// Result is the outcome of highlighting a single source file.
type Result struct {
	// HTML is the rendered, class-based markup. It contains one addressable
	// element per source line (id="L<n>").
	HTML string
	// Lines is the number of source lines rendered, matching the number of
	// per-line elements in HTML.
	Lines int
}

// Highlighter renders source using a fixed chroma style. The zero value is not
// usable; construct one with New. Most callers want the package-level Highlight
// and StylesheetCSS, which use the DefaultStyle.
type Highlighter struct {
	style     *chroma.Style
	formatter *html.Formatter
}

// defaultHighlighter is used by the package-level Highlight and StylesheetCSS.
var defaultHighlighter = New(DefaultStyle)

// New returns a Highlighter for the named chroma style. An unknown style name
// falls back to chroma's built-in default, so New never fails.
func New(styleName string) *Highlighter {
	return &Highlighter{
		style: styles.Get(styleName),
		formatter: html.New(
			// Class-based output: styling comes from the shared
			// stylesheet (StylesheetCSS), not per-span inline styles.
			html.WithClasses(true),
			// Emit a line-number span per line so each line is
			// individually wrapped and, combined with the linkable
			// option below, carries an anchor id.
			html.WithLineNumbers(true),
			// Keep line numbers inline with the code (not in a
			// separate table) so each line stays a single element the
			// viewer can address directly.
			html.LineNumbersInTable(false),
			// Give each line an id="L<n>" anchor so the viewer can
			// find and scroll to a 1-based line number.
			html.WithLinkableLineNumbers(true, lineIDPrefix),
		),
	}
}

// Highlight renders source in the given language to class-based HTML. The
// language is mapped to a chroma lexer by name or alias; an unknown or empty
// language falls back to plaintext and never returns an error for that reason.
func (h *Highlighter) Highlight(source, language string) (Result, error) {
	lexer := lexerFor(language)

	tokens, err := chroma.Tokenise(lexer, nil, source)
	if err != nil {
		return Result{}, fmt.Errorf("highlight %q: tokenise: %w", language, err)
	}
	lineCount := len(chroma.SplitTokensIntoLines(tokens))

	var buf strings.Builder
	if err := h.formatter.Format(&buf, h.style, chroma.Literator(tokens...)); err != nil {
		return Result{}, fmt.Errorf("highlight %q: format: %w", language, err)
	}
	return Result{HTML: buf.String(), Lines: lineCount}, nil
}

// StylesheetCSS returns the shared stylesheet for this Highlighter's style: the
// chroma token classes (.chroma, .k, .line, …) that the HTML from Highlight
// references. The result is non-empty.
func (h *Highlighter) StylesheetCSS() string {
	var buf strings.Builder
	if err := h.formatter.WriteCSS(&buf, h.style); err != nil {
		// WriteCSS only fails on writer errors; a strings.Builder never
		// returns one, so this is unreachable in practice.
		return ""
	}
	return buf.String()
}

// lexerFor resolves a tourdesource language name to a chroma lexer, falling
// back to plaintext when the language is unknown. The returned lexer is
// coalesced so adjacent same-type tokens merge, which keeps the emitted markup
// compact.
func lexerFor(language string) chroma.Lexer {
	name := strings.ToLower(strings.TrimSpace(language))
	if alias, ok := languageAliases[name]; ok {
		name = alias
	}
	lexer := lexers.Get(name)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	return chroma.Coalesce(lexer)
}

// Highlight renders source in the given language using the DefaultStyle. See
// Highlighter.Highlight.
func Highlight(source, language string) (Result, error) {
	return defaultHighlighter.Highlight(source, language)
}

// StylesheetCSS returns the shared stylesheet for the DefaultStyle. See
// Highlighter.StylesheetCSS.
func StylesheetCSS() string {
	return defaultHighlighter.StylesheetCSS()
}
