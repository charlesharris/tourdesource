package draft

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charlesharris/tourdesource/internal/orchestration"
	"github.com/charlesharris/tourdesource/internal/protocol"
	"github.com/charlesharris/tourdesource/internal/store"
)

// Options configure a draft.
type Options struct {
	Root     string // repository root
	MapDir   string // directory holding map.sqlite; default <root>/.tds
	Out      string // output .tour.md path; default <map-dir>/<project>.tour.md
	Audience string // frontmatter audience line
	Template Template
	Assemble AssembleOptions

	// Narrate fills each stop's prose using an assistant instead of leaving a
	// TODO. Nil leaves the draft unnarrated, which is the default: generation
	// costs the author tokens and should be opted into.
	Narrate *NarrateOptions

	Warnf func(format string, a ...any)
	Logf  func(format string, a ...any)
}

// Result summarizes a generated draft.
type Result struct {
	Path      string
	Commit    string
	Template  string
	Chapters  int
	Stops     int
	Anchors   int // stops carrying a symbol anchor
	Landmarks int
	Hotspots  int
	Narrated  int // stops whose prose came from the assistant
	// NarrateRequested records that --narrate was asked for, so the caller can
	// distinguish "narrated nothing" from "never tried".
	NarrateRequested bool
}

// Generate assembles context from the map, plans a tour, optionally narrates it,
// and writes the result.
//
// Every anchor it emits names a symbol that exists in the map. That is the
// anti-hallucination lever (design §6.2), and it holds whether or not narration
// runs: the plan fixes anchors before an assistant is involved, and narration
// only ever supplies prose keyed by stop id.
func Generate(ctx context.Context, opts Options) (*Result, error) {
	warnf, logf := opts.Warnf, opts.Logf
	if warnf == nil {
		warnf = func(string, ...any) {}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opts.Template.Name == "" {
		opts.Template = Onboarding()
	}

	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
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

	dctx, err := Assemble(st, root, opts.Assemble)
	if err != nil {
		return nil, err
	}
	if len(dctx.Landmarks) == 0 {
		warnf("no landmarks found: the map has no class or module symbols, so the draft will be thin")
	}

	if opts.Audience == "" {
		opts.Audience = "engineers new to " + dctx.ProjectName
	}
	plan := buildPlan(dctx, opts)

	res := &Result{
		Commit:    dctx.Commit,
		Template:  opts.Template.Name,
		Chapters:  len(plan.Chapters),
		Landmarks: dctx.landmarksUsed,
		Hotspots:  len(dctx.Hotspots),
	}
	for _, s := range plan.allStops() {
		res.Stops++
		if s.Symbol != "" {
			res.Anchors++
		}
	}

	res.NarrateRequested = opts.Narrate != nil
	if opts.Narrate != nil {
		n, err := narrate(ctx, plan, dctx, *opts.Narrate, logf, warnf)
		if err != nil {
			return nil, fmt.Errorf("narrating: %w", err)
		}
		res.Narrated = n
		plan.Narrated = n > 0
	}

	out := opts.Out
	if out == "" {
		out = filepath.Join(mapDir, dctx.ProjectName+".tour.md")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}
	if err := os.WriteFile(out, serialize(plan), 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", out, err)
	}

	res.Path = out
	return res, nil
}

// buildPlan pours the assembled context into the template.
func buildPlan(dctx *Context, opts Options) *Plan {
	title := dctx.ProjectName
	if dctx.Readme.Title != "" && !strings.EqualFold(dctx.Readme.Title, dctx.ProjectName) {
		title = fmt.Sprintf("%s — %s", dctx.ProjectName, dctx.Readme.Title)
	}

	p := &Plan{
		Title:    "A tour of " + title,
		Audience: opts.Audience,
		Commit:   dctx.Commit,
		Template: opts.Template.Name,
	}
	if dctx.Readme.Lead != "" {
		p.Intro = dctx.Readme.Lead
		p.IntroNote = "from " + dctx.Readme.Path + " — rewrite in your own words"
	} else {
		p.Intro = "TODO: one paragraph on what this project is."
	}

	used := map[string]bool{} // stop ids
	seen := map[string]bool{} // anchors, to avoid pointing at the same symbol twice

	for _, spec := range opts.Template.Chapters {
		ch := PlanChapter{Title: spec.Title, Guidance: spec.Guidance}
		switch spec.Kind {
		case SectionOverview:
			planOverview(&ch, dctx, used, seen)
		case SectionSlice:
			planSlice(&ch, dctx, used, seen)
		case SectionLandmarks:
			dctx.landmarksUsed = planLandmarks(&ch, dctx, used, seen)
		case SectionConventions:
			planConventions(&ch, dctx, used, seen)
		case SectionSideQuests:
			planSideQuests(&ch, dctx, used, seen)
		}
		p.Chapters = append(p.Chapters, ch)
	}
	return p
}

// addStop appends a stop unless its anchor was already used earlier in the tour.
func addStop(ch *PlanChapter, used, seen map[string]bool, anchor, symbol, task, evidence string) *PlanStop {
	if seen[anchor] {
		return nil
	}
	seen[anchor] = true
	ch.Stops = append(ch.Stops, PlanStop{
		ID:       makeID(anchor, used),
		Anchor:   anchor,
		Symbol:   symbol,
		Task:     task,
		Evidence: evidence,
	})
	return &ch.Stops[len(ch.Stops)-1]
}

func planOverview(ch *PlanChapter, dctx *Context, used, seen map[string]bool) {
	if len(dctx.Languages) > 0 {
		var parts []string
		for i, l := range dctx.Languages {
			if i >= 5 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s (%d files)", l.Language, l.Files))
		}
		ch.Notes = append(ch.Notes, "languages: "+strings.Join(parts, ", "))
	}
	if s := entrypointSummary(dctx); s != "" {
		ch.Notes = append(ch.Notes, "entrypoints: "+s)
	}

	// The routes table is the single best answer to "what can this system do?".
	if routes := dctx.Entrypoints["rails-routes"]; len(routes) > 0 {
		addStop(ch, used, seen, routes[0].Path+":1-40", "",
			"the surface area of the app — what are the main things a user can do?", "")
	}
	if models := dctx.Entrypoints["rails-model"]; len(models) > 0 {
		addStop(ch, used, seen, models[0].Path+"::"+models[0].Name, models[0].Name,
			"the central record this system is organised around, and what it represents", "")
	}
}

func planSlice(ch *PlanChapter, dctx *Context, used, seen map[string]bool) {
	if dctx.Slice.Reason != "" {
		ch.Notes = append(ch.Notes, "proposed trace: "+dctx.Slice.Reason)
	}
	if dctx.Slice.Entry == nil && len(dctx.Slice.Steps) == 0 {
		ch.Notes = append(ch.Notes, "tds could not propose a trace: no entrypoints in the map")
		return
	}
	if e := dctx.Slice.Entry; e != nil {
		addStop(ch, used, seen, anchorFor(e.Symbol), e.Symbol.Symbol,
			"where the operation begins — what arrives here, and what does this layer decide?", "")
	}
	for _, s := range dctx.Slice.Steps {
		addStop(ch, used, seen, anchorFor(s.Symbol), s.Symbol.Symbol,
			"what happens at this step, and what it hands on", s.Reason)
	}
}

// planLandmarks fills the chapter to its limit from the ranked pool, skipping
// anything an earlier chapter already anchored. Returns how many it placed.
func planLandmarks(ch *PlanChapter, dctx *Context, used, seen map[string]bool) int {
	limit := dctx.LandmarkLimit
	if limit <= 0 {
		limit = len(dctx.Landmarks)
	}
	n := 0
	for _, l := range dctx.Landmarks {
		if n >= limit {
			break
		}
		if addStop(ch, used, seen, anchorFor(l.Symbol), l.Symbol.Symbol,
			"why this exists and the one thing to know about it", l.Reason) != nil {
			n++
		}
	}
	return n
}

func planConventions(ch *PlanChapter, dctx *Context, used, seen map[string]bool) {
	for _, c := range dctx.Conventions {
		ch.Body = append(ch.Body, fmt.Sprintf("**%s.** %s", c.Title, c.Detail))
	}
	if dctx.Readme.Path != "" {
		addStop(ch, used, seen, dctx.Readme.Path+":1-30", "",
			"how to get it running locally, and anything the README gets wrong", "")
	}
}

// planSideQuests makes each side-quest a stop with the extra examples folded
// into a detour beneath it — the chapter > stop > detour > stop nesting the tour
// format defines.
func planSideQuests(ch *PlanChapter, dctx *Context, used, seen map[string]bool) {
	for _, q := range []struct{ kind, label, task string }{
		{"rails-job", "background jobs", "when work ends up in a job rather than inline, and who runs them"},
		{"rails-mailer", "outbound mail", "what this system sends, and what triggers it"},
	} {
		eps := dctx.Entrypoints[q.kind]
		if len(eps) == 0 {
			continue
		}
		lead, rest := eps[0], eps[1:]
		if len(rest) > 3 {
			rest = rest[:3]
		}

		stop := addStop(ch, used, seen, lead.Path+"::"+lead.Name, lead.Name,
			fmt.Sprintf("if you're working on %s: %s", q.label, q.task), "")
		if stop == nil {
			continue
		}

		var nested []PlanStop
		for _, e := range rest {
			anchor := e.Path + "::" + e.Name
			if seen[anchor] {
				continue
			}
			seen[anchor] = true
			nested = append(nested, PlanStop{
				ID:     makeID(anchor, used),
				Anchor: anchor,
				Symbol: e.Name,
				Task:   "what this one does, and what triggers it",
			})
		}
		if len(nested) > 0 {
			stop.Detour = &PlanDetour{Title: "The other " + q.label, Stops: nested}
		}
	}

	if len(dctx.Hotspots) > 0 {
		lead := dctx.Hotspots[0]
		var sb strings.Builder
		sb.WriteString("These files change most often, so this is where work tends to land:\n\n")
		for _, h := range dctx.Hotspots {
			sb.WriteString(fmt.Sprintf("- `%s` — %d commits", h.Path, h.Churn))
			if a := authorPhrase(h.Authors); a != "" {
				sb.WriteString(", " + a)
			}
			sb.WriteString("\n")
		}
		addStop(ch, used, seen, fmt.Sprintf("%s:1-40", lead.Path), "",
			"if you're here to fix a bug: which of these are genuinely hot, and which just churn?",
			strings.TrimSpace(sb.String()))
	}
}

// serialize writes the plan as a `.tour.md`.
func serialize(p *Plan) []byte {
	var b strings.Builder

	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %q\n", p.Title)
	fmt.Fprintf(&b, "template: %s\n", p.Template)
	fmt.Fprintf(&b, "audience: %q\n", p.Audience)
	if p.Commit != "" {
		fmt.Fprintf(&b, "commit: %s\n", p.Commit)
	}
	b.WriteString("---\n\n")
	b.WriteString(bannerFor(p) + "\n")

	fmt.Fprintf(&b, "%s\n\n", p.Intro)
	if p.IntroNote != "" {
		fmt.Fprintf(&b, "<!-- %s -->\n\n", p.IntroNote)
	}

	for _, ch := range p.Chapters {
		fmt.Fprintf(&b, "# Chapter: %s\n\n", ch.Title)
		fmt.Fprintf(&b, "<!-- %s -->\n\n", ch.Guidance)
		for _, n := range ch.Notes {
			fmt.Fprintf(&b, "<!-- %s -->\n\n", n)
		}
		for _, body := range ch.Body {
			fmt.Fprintf(&b, "%s\n\n", body)
		}
		for _, st := range ch.Stops {
			writePlanStop(&b, st)
		}
	}
	return []byte(b.String())
}

func writePlanStop(b *strings.Builder, st PlanStop) {
	fmt.Fprintf(b, "::stop{anchor=%q}\n", st.Anchor)
	prose := st.Prose
	if strings.TrimSpace(prose) == "" {
		prose = st.todoProse()
	}
	fmt.Fprintf(b, "%s\n", prose)

	if d := st.Detour; d != nil && len(d.Stops) > 0 {
		fmt.Fprintf(b, "::detour{title=%q}\n", d.Title)
		if strings.TrimSpace(d.Intro) != "" {
			fmt.Fprintf(b, "%s\n", d.Intro)
		}
		for _, ds := range d.Stops {
			fmt.Fprintf(b, "::stop{anchor=%q}\n", ds.Anchor)
			p := ds.Prose
			if strings.TrimSpace(p) == "" {
				p = ds.todoProse()
			}
			fmt.Fprintf(b, "%s\n", p)
			b.WriteString("::\n")
		}
		b.WriteString("::\n") // close the detour
	}
	b.WriteString("::\n\n") // close the stop
}

// bannerFor states what the document is. A narrated draft and a placeholder
// draft need reviewing for different reasons, and saying "TODO" in a file with
// no TODOs in it just teaches the reader to ignore the banner.
func bannerFor(p *Plan) string {
	if p.Narrated {
		return "<!-- DRAFT generated by `tds draft --narrate`.\n" +
			"Anchors name symbols that exist in the map, so they resolve; they were\n" +
			"chosen from the map, not written by the assistant. The prose WAS written by\n" +
			"an assistant from the anchored code and has not been reviewed — read it for\n" +
			"claims the code does not support before sharing this tour. -->\n"
	}
	return "<!-- DRAFT generated by `tds draft`.\n" +
		"Anchors below name symbols that exist in the map, so they resolve. Prose marked\n" +
		"TODO is not written yet: each carries the evidence tds has. Curating this means\n" +
		"fixing, cutting, and reordering — not starting from a blank page. -->\n"
}

// anchorFor builds the symbol anchor for a mapped symbol.
func anchorFor(s protocol.Symbol) string {
	return s.Path + "::" + s.Symbol
}

func entrypointSummary(dctx *Context) string {
	if len(dctx.Entrypoints) == 0 {
		return ""
	}
	kinds := make([]string, 0, len(dctx.Entrypoints))
	for k := range dctx.Entrypoints {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s %d", k, len(dctx.Entrypoints[k])))
	}
	return strings.Join(parts, ", ")
}

// assistantFor is overridable in tests so narration can be exercised without
// tmux or tokens.
var assistantFor = func(ctx context.Context, opts NarrateOptions) (orchestration.Assistant, error) {
	return orchestration.Start(ctx, orchestration.Options{
		WorkDir: opts.WorkDir,
		Session: "tds-draft",
		Command: opts.Command,
		Logf:    opts.Logf,
	})
}
