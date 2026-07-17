// Package tour parses a tour source file (`*.tour.md`) into a tour AST.
//
// The `.tour.md` format is the source of truth an author writes (design §4):
// YAML frontmatter for tour metadata, `# Chapter:` headings, and `::`-delimited
// directive blocks for stops and detours. It is deliberately line-oriented — a
// small scanner with a block stack, not a full Markdown parser. Prose is kept
// verbatim as raw Markdown strings; only the directive scaffolding is
// interpreted.
//
// The document shape is:
//
//	---
//	title: "A tour of Acme's billing service"
//	template: onboarding
//	---
//
//	# Chapter: The 30-second version
//
//	Chapter intro prose (raw Markdown).
//
//	::stop{anchor="app/models/invoice.rb::Invoice" focus="def finalize"}
//	Stop prose (raw Markdown).
//	::detour{title="If you're debugging a stuck invoice"}
//	Detour intro prose.
//	::stop{anchor="app/models/invoice.rb::Invoice#with_lock"}
//	Nested stop prose.
//	::
//	::
//	::
//
// Blocks nest chapter > stop > detour > stop. A lone `::` closes the innermost
// open block; unmatched or misnested directives are reported as errors that name
// the offending directive and its line.
package tour

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tour is a parsed tour: metadata plus an ordered list of chapters.
type Tour struct {
	Title    string
	Template string
	Repo     string
	Commit   string
	Audience string
	Intro    string // raw Markdown prose before the first chapter
	// Meta holds any frontmatter keys beyond the recognized ones, stringified.
	Meta     map[string]string
	Chapters []Chapter
}

// Chapter is a named, ordered group of stops (design §3).
type Chapter struct {
	Title string
	Intro string // raw Markdown prose at chapter level, outside any stop
	Stops []Stop
}

// Stop is the atomic unit: prose anchored into code, with optional detours.
type Stop struct {
	Anchor  string // required symbol/line anchor (design §4.2)
	Focus   string // optional highlighted sub-range within the anchor
	View    string // optional deep-linked view id
	Prose   string // raw Markdown body
	Detours []Detour
}

// Detour is a collapsible side-quest hanging off a stop; it may contain stops.
type Detour struct {
	Title string
	Intro string // raw Markdown prose before any nested stop
	Stops []Stop
}

// Recognized directive names and attribute keys.
const (
	directiveStop   = "stop"
	directiveDetour = "detour"

	attrAnchor = "anchor"
	attrFocus  = "focus"
	attrView   = "view"
	attrTitle  = "title"
)

const chapterPrefix = "# Chapter:"

// ParseFile reads path and parses it, wrapping any error with the path.
func ParseFile(path string) (*Tour, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading tour file %s: %w", path, err)
	}
	t, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing tour file %s: %w", path, err)
	}
	return t, nil
}

// Parse parses the bytes of a `.tour.md` file into a Tour.
func Parse(data []byte) (*Tour, error) {
	lines := splitLines(data)

	fmLines, body, err := splitFrontmatter(lines)
	if err != nil {
		return nil, err
	}

	tour := &Tour{Meta: map[string]string{}}
	if err := parseFrontmatter(fmLines, tour); err != nil {
		return nil, err
	}
	if err := parseBody(body, tour); err != nil {
		return nil, err
	}
	return tour, nil
}

// splitLines splits data into logical lines, dropping a trailing carriage
// return so the parser is CRLF-tolerant.
func splitLines(data []byte) []string {
	raw := strings.Split(string(data), "\n")
	for i, l := range raw {
		raw[i] = strings.TrimSuffix(l, "\r")
	}
	return raw
}

// splitFrontmatter peels off a leading `---`…`---` YAML block, if present,
// returning its inner lines and the remaining body lines.
func splitFrontmatter(lines []string) (fm, body []string, err error) {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, lines, nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return lines[1:i], lines[i+1:], nil
		}
	}
	return nil, nil, fmt.Errorf("unterminated frontmatter: opening `---` has no closing `---`")
}

// parseFrontmatter decodes the YAML frontmatter, assigning recognized keys to
// typed fields and stashing the rest, stringified, in Meta.
func parseFrontmatter(fmLines []string, tour *Tour) error {
	if len(fmLines) == 0 {
		return nil
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(fmLines, "\n")), &raw); err != nil {
		return fmt.Errorf("parsing frontmatter: %w", err)
	}
	for key, val := range raw {
		s := stringifyScalar(val)
		switch key {
		case "title":
			tour.Title = s
		case "template":
			tour.Template = s
		case "repo":
			tour.Repo = s
		case "commit":
			tour.Commit = s
		case "audience":
			tour.Audience = s
		default:
			tour.Meta[key] = s
		}
	}
	return nil
}

// stringifyScalar renders a YAML scalar as a string for Meta / typed fields.
func stringifyScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

// frame is one open block on the parse stack: a stop or a detour under
// construction, plus the raw prose lines collected directly inside it.
type frame struct {
	isStop    bool
	stop      *Stop
	detour    *Detour
	directive string // trimmed opening directive, for error messages
	line      int    // 1-based line of the opening directive
	prose     []string
}

// parseBody runs the line-oriented scanner over the body, filling in tour.
func parseBody(lines []string, tour *Tour) error {
	var (
		stack        []*frame
		cur          *Chapter // chapter under construction
		chapterIntro []string
		tourIntro    []string
	)

	finalizeChapter := func() {
		if cur != nil {
			cur.Intro = joinProse(chapterIntro)
			tour.Chapters = append(tour.Chapters, *cur)
			cur = nil
			chapterIntro = nil
		}
	}

	addProse := func(line string) {
		switch {
		case len(stack) > 0:
			top := stack[len(stack)-1]
			top.prose = append(top.prose, line)
		case cur == nil:
			tourIntro = append(tourIntro, line)
		default:
			chapterIntro = append(chapterIntro, line)
		}
	}

	for i, line := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "::":
			if err := closeBlock(&stack, cur, lineNo); err != nil {
				return err
			}

		case strings.HasPrefix(trimmed, "::"):
			name, attrs, err := parseOpener(trimmed, lineNo)
			if err != nil {
				return err
			}
			if err := openBlock(&stack, cur, name, attrs, trimmed, lineNo); err != nil {
				return err
			}

		case len(stack) == 0 && strings.HasPrefix(trimmed, chapterPrefix):
			// A `# Chapter:` heading only starts a chapter at the top level;
			// inside an open block it is ordinary prose (a Markdown heading).
			finalizeChapter()
			cur = &Chapter{Title: strings.TrimSpace(trimmed[len(chapterPrefix):])}

		default:
			addProse(line)
		}
	}

	if len(stack) > 0 {
		top := stack[len(stack)-1]
		return fmt.Errorf("unclosed block: %s opened at line %d has no closing `::`", top.directive, top.line)
	}
	finalizeChapter()

	tour.Intro = joinProse(tourIntro)
	return nil
}

// openBlock opens a stop or detour, validating its nesting context.
func openBlock(stack *[]*frame, cur *Chapter, name, attrs, directive string, lineNo int) error {
	switch name {
	case directiveStop:
		return openStop(stack, cur, attrs, directive, lineNo)
	case directiveDetour:
		return openDetour(stack, cur, attrs, directive, lineNo)
	default:
		return fmt.Errorf("unknown directive %q at line %d", "::"+name, lineNo)
	}
}

func openStop(stack *[]*frame, cur *Chapter, attrs, directive string, lineNo int) error {
	if len(*stack) == 0 {
		if cur == nil {
			return fmt.Errorf("stop at line %d appears before any `# Chapter:`", lineNo)
		}
	} else if top := (*stack)[len(*stack)-1]; top.isStop {
		return fmt.Errorf("stop at line %d cannot nest inside another stop (opened at line %d); close it with `::` first", lineNo, top.line)
	}

	parsed, err := parseAttrs(attrs)
	if err != nil {
		return fmt.Errorf("stop at line %d: %w", lineNo, err)
	}
	anchor, ok := parsed[attrAnchor]
	if !ok || anchor == "" {
		return fmt.Errorf("stop at line %d is missing required attribute %q", lineNo, attrAnchor)
	}
	st := &Stop{Anchor: anchor, Focus: parsed[attrFocus], View: parsed[attrView]}
	*stack = append(*stack, &frame{isStop: true, stop: st, directive: directive, line: lineNo})
	return nil
}

func openDetour(stack *[]*frame, cur *Chapter, attrs, directive string, lineNo int) error {
	if len(*stack) == 0 {
		if cur == nil {
			return fmt.Errorf("detour at line %d appears before any `# Chapter:`", lineNo)
		}
		return fmt.Errorf("detour at line %d must appear inside a stop", lineNo)
	}
	if top := (*stack)[len(*stack)-1]; !top.isStop {
		return fmt.Errorf("detour at line %d must appear inside a stop, not inside another detour (opened at line %d)", lineNo, top.line)
	}

	parsed, err := parseAttrs(attrs)
	if err != nil {
		return fmt.Errorf("detour at line %d: %w", lineNo, err)
	}
	dt := &Detour{Title: parsed[attrTitle]}
	*stack = append(*stack, &frame{isStop: false, detour: dt, directive: directive, line: lineNo})
	return nil
}

// closeBlock pops the innermost open block and attaches it to its parent.
func closeBlock(stack *[]*frame, cur *Chapter, lineNo int) error {
	if len(*stack) == 0 {
		return fmt.Errorf("unexpected `::` at line %d: no open block to close", lineNo)
	}
	fr := (*stack)[len(*stack)-1]
	*stack = (*stack)[:len(*stack)-1]

	if fr.isStop {
		fr.stop.Prose = joinProse(fr.prose)
		if len(*stack) == 0 {
			cur.Stops = append(cur.Stops, *fr.stop) // cur is non-nil (checked at open)
		} else {
			parent := (*stack)[len(*stack)-1]
			parent.detour.Stops = append(parent.detour.Stops, *fr.stop)
		}
		return nil
	}

	fr.detour.Intro = joinProse(fr.prose)
	parent := (*stack)[len(*stack)-1] // a detour's parent is always a stop (checked at open)
	parent.stop.Detours = append(parent.stop.Detours, *fr.detour)
	return nil
}

// parseOpener splits a `::name{attrs}` directive into its name and raw attrs.
func parseOpener(s string, lineNo int) (name, attrs string, err error) {
	body := s[2:] // strip leading "::"
	open := strings.IndexByte(body, '{')
	if open < 0 || !strings.HasSuffix(body, "}") {
		return "", "", fmt.Errorf("malformed directive %q at line %d: expected `::name{...}`", s, lineNo)
	}
	name = strings.TrimSpace(body[:open])
	if name == "" {
		return "", "", fmt.Errorf("malformed directive %q at line %d: missing directive name", s, lineNo)
	}
	return name, body[open+1 : len(body)-1], nil
}

// parseAttrs parses space-separated `key="value"` pairs. Values are always
// double-quoted and may contain spaces; keys are unquoted. An empty input
// yields an empty map.
func parseAttrs(s string) (map[string]string, error) {
	attrs := map[string]string{}
	i, n := 0, len(s)
	for i < n {
		for i < n && isAttrSpace(s[i]) {
			i++
		}
		if i >= n {
			break
		}

		keyStart := i
		for i < n && s[i] != '=' && !isAttrSpace(s[i]) {
			i++
		}
		key := s[keyStart:i]
		if key == "" {
			return nil, fmt.Errorf("malformed attributes %q: expected a key", s)
		}

		for i < n && isAttrSpace(s[i]) {
			i++
		}
		if i >= n || s[i] != '=' {
			return nil, fmt.Errorf("malformed attributes %q: expected `=` after key %q", s, key)
		}
		i++ // consume '='
		for i < n && isAttrSpace(s[i]) {
			i++
		}
		if i >= n || s[i] != '"' {
			return nil, fmt.Errorf("malformed attributes %q: expected a quoted value for key %q", s, key)
		}
		i++ // consume opening quote

		valStart := i
		for i < n && s[i] != '"' {
			i++
		}
		if i >= n {
			return nil, fmt.Errorf("malformed attributes %q: unterminated quoted value for key %q", s, key)
		}
		attrs[key] = s[valStart:i]
		i++ // consume closing quote
	}
	return attrs, nil
}

func isAttrSpace(b byte) bool { return b == ' ' || b == '\t' }

// joinProse joins collected prose lines and trims surrounding blank lines,
// preserving interior Markdown formatting.
func joinProse(lines []string) string {
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
