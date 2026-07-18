package draft

import (
	"fmt"
	"strings"
)

// A draft is built in three steps: plan the structure from the map, optionally
// narrate it (filling prose via an assistant), then serialize to `.tour.md`.
// Keeping the structure addressable in between is what lets narration replace
// prose without ever touching an anchor — the anchors are decided here, from
// the map, and narration never sees a chance to change them.

// Plan is a tour's structure with its prose still open.
type Plan struct {
	Title     string
	Audience  string
	Commit    string
	Template  string
	Intro     string // opening prose (from the README, or a TODO)
	IntroNote string
	Chapters  []PlanChapter
	// Narrated records that an assistant wrote the prose, so the document can
	// say what it actually is rather than claiming TODOs it does not have.
	Narrated bool
}

// PlanChapter is one chapter: heading, curator guidance, evidence notes, any
// free-form body, and its stops.
type PlanChapter struct {
	Title    string
	Guidance string
	// Notes are emitted as HTML comments — evidence tds gathered that a curator
	// (or the narrate pass) should see but a reader should not.
	Notes []string
	// Body is free-form Markdown emitted before the stops (the conventions
	// chapter is prose, not stops).
	Body  []string
	Stops []PlanStop
}

// PlanStop is a single stop. Prose is what narration replaces; Evidence is what
// tds knows about the anchor and is what makes a TODO actionable and a narrate
// prompt grounded.
type PlanStop struct {
	ID       string
	Anchor   string
	Symbol   string // empty for a line-range anchor
	Task     string // what the prose at this stop needs to accomplish
	Evidence string // ranking signals: kind, size, churn, authors
	Prose    string // filled at serialize time: TODO placeholder or narration
	Detour   *PlanDetour
}

// PlanDetour is a collapsible side-quest nested inside a stop.
type PlanDetour struct {
	Title string
	Intro string
	Stops []PlanStop
}

// allStops walks every stop in the plan, including those nested in detours, in
// document order. Narration and validation both need the full set.
func (p *Plan) allStops() []*PlanStop {
	var out []*PlanStop
	var walk func(stops []PlanStop)
	walk = func(stops []PlanStop) {
		for i := range stops {
			out = append(out, &stops[i])
			if d := stops[i].Detour; d != nil {
				walk(d.Stops)
			}
		}
	}
	for i := range p.Chapters {
		// Index rather than range-copy so the returned pointers address the plan.
		walk(p.Chapters[i].Stops)
	}
	return out
}

// stopByID indexes the plan's stops for merging a narration response.
func (p *Plan) stopByID() map[string]*PlanStop {
	out := map[string]*PlanStop{}
	for _, s := range p.allStops() {
		out[s.ID] = s
	}
	return out
}

// todoProse is the placeholder written when a stop has not been narrated. It
// carries the task and the evidence so a human curator has something to work
// from rather than a bare marker.
func (s *PlanStop) todoProse() string {
	if s.Evidence == "" {
		return "TODO: " + s.Task
	}
	return fmt.Sprintf("TODO: %s (%s)", s.Task, s.Evidence)
}

// makeID derives a stable, unique stop id from its anchor. Ids key the narration
// response back to the plan, so they must survive a round trip through JSON and
// must not collide.
func makeID(anchor string, used map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(anchor) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	base := strings.Trim(collapseDashes(b.String()), "-")
	if base == "" {
		base = "stop"
	}
	if len(base) > 60 {
		base = strings.Trim(base[:60], "-")
	}
	id := base
	for i := 2; used[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	used[id] = true
	return id
}

func collapseDashes(s string) string {
	var b strings.Builder
	var prevDash bool
	for _, r := range s {
		if r == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		b.WriteRune(r)
	}
	return b.String()
}
