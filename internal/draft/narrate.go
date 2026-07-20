package draft

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charlesharris/tourdesource/internal/orchestration"
)

// Narration is the second drafting pass (TDS-42). The first pass — choosing what
// to point at — is done deterministically from the map, and is strictly better
// grounded than asking a model to pick: it cannot name a symbol that does not
// exist. So narration is handed a finished skeleton and asked only for prose.
//
// The assistant never sees an opportunity to change an anchor. It receives stop
// *ids* and returns prose keyed by those ids; anchors stay in the plan and are
// never part of the response. That makes anchor hallucination structurally
// impossible rather than something to detect afterwards, which is the strongest
// form of TDS-43's validation gate. What the gate still checks is that the
// response only names stops we asked about, and that its prose cannot break the
// tour format.

// NarrateOptions configure the narration pass.
type NarrateOptions struct {
	// Root is the repository, used to read the code each stop anchors.
	Root string
	// WorkDir holds prompt/answer files. Defaults to a temp dir.
	WorkDir string
	// Command overrides the assistant command (defaults to Claude Code).
	Command []string
	// Timeout bounds a single batch request.
	Timeout time.Duration
	// MaxPromptBytes caps a batch's prompt. Stops are grouped until adding the
	// next would exceed this, so a large tour becomes a few requests rather than
	// one oversized one.
	MaxPromptBytes int
	// MaxExcerptLines caps the code shown per stop. A 2000-line god-class would
	// otherwise crowd out every other stop in the batch.
	MaxExcerptLines int

	// FromFile replays a previously saved assistant response instead of asking
	// again. Stop ids are derived from anchors and so are stable across runs,
	// which makes a saved response re-playable: it recovers a clobbered tour,
	// makes a narrated draft reproducible, and lets the merge and validation
	// path be debugged without spending tokens.
	FromFile string

	// FullNarration additionally writes a summary for each of the busiest
	// files, shown in the explorer. Subsystem naming happens under plain
	// --narrate because it is a handful of requests; this is the pass that can
	// run for hours on a large repository, so it is opted into separately.
	FullNarration bool
	// MaxFiles caps how many files FullNarration describes, busiest first. A
	// large repository has thousands of files and the assistant is driven one
	// request at a time, so an uncapped pass is not a feature anyone can wait
	// for. Summaries are cached by content hash, so raising this later only
	// costs the difference.
	MaxFiles int

	// Assistant injects a stand-in; nil starts Claude in tmux.
	Assistant orchestration.Assistant

	Logf func(format string, a ...any)
}

func (o NarrateOptions) withDefaults() NarrateOptions {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Minute
	}
	if o.MaxPromptBytes <= 0 {
		o.MaxPromptBytes = 60_000
	}
	if o.MaxExcerptLines <= 0 {
		o.MaxExcerptLines = 120
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = 250
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	return o
}

// narrateResponse is the assistant's answer: prose keyed by stop id. Anchors are
// deliberately absent from this shape.
type narrateResponse struct {
	Stops map[string]string `json:"stops"`
}

// narrate fills the plan's prose. It returns how many stops were narrated.
// Failure to narrate is not fatal: a stop keeps its TODO, which is a worse tour
// but an honest one.
func narrate(
	ctx context.Context,
	plan *Plan,
	dctx *Context,
	opts NarrateOptions,
	logf, warnf func(string, ...any),
) (int, error) {
	opts = opts.withDefaults()
	if opts.Root == "" {
		opts.Root = dctx.Root
	}
	if opts.WorkDir == "" {
		dir, err := os.MkdirTemp("", "tds-narrate-*")
		if err != nil {
			return 0, err
		}
		defer os.RemoveAll(dir)
		opts.WorkDir = dir
	}
	opts.Logf = logf

	stops := plan.allStops()
	if len(stops) == 0 {
		return 0, nil
	}

	// Replay path: no assistant, no tmux, no tokens — but the same validation
	// gate, so a hand-edited or stale response cannot smuggle anything in.
	if opts.FromFile != "" {
		return replay(plan, opts, logf, warnf)
	}

	assistant := opts.Assistant
	if assistant == nil {
		a, err := assistantFor(ctx, opts)
		if err != nil {
			return 0, err
		}
		defer a.Close()
		assistant = a
	}

	batches := batchStops(stops, opts, plan, dctx)
	logf("narrating %d stops in %d request(s)", len(stops), len(batches))

	byID := plan.stopByID()
	narrated := 0

	for i, batch := range batches {
		prompt := buildNarratePrompt(plan, dctx, batch, opts)
		logf("request %d/%d: %d stops, %d bytes", i+1, len(batches), len(batch), len(prompt))

		raw, err := assistant.Ask(ctx, orchestration.Request{
			Name:    fmt.Sprintf("narrate-%d", i+1),
			Prompt:  prompt,
			Timeout: opts.Timeout,
		})
		if err != nil {
			// One failed batch leaves those stops as TODO rather than failing the
			// whole draft: a partially narrated tour is still useful.
			warnf("narration request %d/%d failed, leaving those stops as TODO: %v", i+1, len(batches), err)
			continue
		}

		var resp narrateResponse
		if err := orchestration.DecodeJSON(raw, &resp); err != nil {
			warnf("narration request %d/%d returned unusable output, leaving those stops as TODO: %v", i+1, len(batches), err)
			continue
		}

		requested := map[string]bool{}
		for _, s := range batch {
			requested[s.ID] = true
		}
		accepted, rejected := acceptNarration(resp.Stops, requested, byID)
		narrated += accepted
		for _, r := range rejected {
			warnf("narration: %s", r)
		}
	}

	return narrated, nil
}

// replay merges a saved response file, applying the same gate a live response
// gets. Every stop in the plan is treated as requested, since a saved file may
// have come from a run that batched differently.
func replay(plan *Plan, opts NarrateOptions, logf, warnf func(string, ...any)) (int, error) {
	raw, err := os.ReadFile(opts.FromFile)
	if err != nil {
		return 0, fmt.Errorf("reading saved narration: %w", err)
	}
	var resp narrateResponse
	if err := orchestration.DecodeJSON(raw, &resp); err != nil {
		return 0, fmt.Errorf("saved narration in %s is unusable: %w", opts.FromFile, err)
	}

	byID := plan.stopByID()
	requested := map[string]bool{}
	for id := range byID {
		requested[id] = true
	}

	accepted, rejected := acceptNarration(resp.Stops, requested, byID)
	for _, r := range rejected {
		warnf("narration replay: %s", r)
	}
	logf("replayed %d of %d stops from %s", accepted, len(byID), opts.FromFile)
	if accepted == 0 {
		return 0, fmt.Errorf("no stops in %s matched this plan: the tour structure has "+
			"changed since it was saved, so the prose no longer lines up", opts.FromFile)
	}
	return accepted, nil
}

// acceptNarration is the validation gate. It merges prose for stops we asked
// about and refuses everything else, returning how many were accepted and why
// any were not.
func acceptNarration(
	got map[string]string,
	requested map[string]bool,
	byID map[string]*PlanStop,
) (accepted int, rejected []string) {
	ids := make([]string, 0, len(got))
	for id := range got {
		ids = append(ids, id)
	}
	sortStringsStable(ids)

	for _, id := range ids {
		prose := strings.TrimSpace(got[id])

		// A stop we did not ask about is either a hallucinated id or a crossed
		// response; either way it must not enter the tour.
		if !requested[id] {
			rejected = append(rejected, fmt.Sprintf("ignoring prose for unknown stop %q", id))
			continue
		}
		if prose == "" {
			rejected = append(rejected, fmt.Sprintf("empty prose for stop %q, keeping its TODO", id))
			continue
		}
		// Prose containing directives would restructure the tour — reopen or
		// close blocks the plan did not intend. The format is ours to emit.
		if line, bad := containsDirective(prose); bad {
			rejected = append(rejected, fmt.Sprintf(
				"prose for stop %q contains the tour directive %q, keeping its TODO", id, line))
			continue
		}

		st, ok := byID[id]
		if !ok {
			rejected = append(rejected, fmt.Sprintf("no such stop %q in the plan", id))
			continue
		}
		st.Prose = prose
		accepted++
	}
	return accepted, rejected
}

// containsDirective reports whether prose contains a line that the tour parser
// would read as a directive.
func containsDirective(prose string) (string, bool) {
	for _, line := range strings.Split(prose, "\n") {
		t := strings.TrimSpace(line)
		if t == "::" || strings.HasPrefix(t, "::stop") || strings.HasPrefix(t, "::detour") {
			return t, true
		}
		if strings.HasPrefix(t, "# Chapter:") {
			return t, true
		}
	}
	return "", false
}

// batchStops groups stops into requests under the prompt budget. Batching keeps
// round trips (and cost) down, and lets the assistant see neighbouring stops so
// the narration reads as one tour rather than a pile of independent blurbs.
func batchStops(stops []*PlanStop, opts NarrateOptions, plan *Plan, dctx *Context) [][]*PlanStop {
	var batches [][]*PlanStop
	var cur []*PlanStop
	size := 0

	for _, s := range stops {
		entry := len(stopBrief(s, opts, dctx))
		if len(cur) > 0 && size+entry > opts.MaxPromptBytes {
			batches = append(batches, cur)
			cur, size = nil, 0
		}
		cur = append(cur, s)
		size += entry
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

// buildNarratePrompt writes the instruction for one batch.
func buildNarratePrompt(plan *Plan, dctx *Context, batch []*PlanStop, opts NarrateOptions) string {
	var b strings.Builder

	b.WriteString("You are writing the narration for a guided code tour.\n\n")
	fmt.Fprintf(&b, "Tour: %s\n", plan.Title)
	fmt.Fprintf(&b, "Audience: %s\n", plan.Audience)
	fmt.Fprintf(&b, "Repository: %s (at commit %s)\n", dctx.ProjectName, shortCommit(plan.Commit))
	if dctx.Readme.Lead != "" {
		fmt.Fprintf(&b, "What the project says about itself: %s\n", dctx.Readme.Lead)
	}
	b.WriteString("\n")

	b.WriteString(`## Your task

For each stop below, write the prose a reader sees at that point in the tour.

Rules:
- Write 2-4 sentences per stop. Prose, not bullet points.
- Say what this code DOES and WHY it matters to someone new to the codebase.
  Do not narrate the syntax — the reader can see the code.
- Be concrete and specific to what you are shown. If the code does not tell you
  something, do not guess it. Never invent file names, classes or behaviour.
- If a stop's code does not support anything worth saying, return a short honest
  sentence rather than padding.
- Markdown is allowed (backticks, **bold**). Do NOT use headings.
- Do NOT write any line beginning with "::" or "# Chapter:" — those are tour
  directives and will corrupt the document.

`)

	fmt.Fprintf(&b, "## Output format\n\nWrite a single JSON object to the answer file:\n\n")
	b.WriteString("{\n  \"stops\": {\n    \"<stop-id>\": \"<prose for that stop>\"\n  }\n}\n\n")
	b.WriteString("Use exactly the stop ids given below. Include every one of them. ")
	b.WriteString("Do not add any other keys.\n\n")

	b.WriteString("## The stops\n\n")
	for _, s := range batch {
		b.WriteString(stopBrief(s, opts, dctx))
	}
	return b.String()
}

// stopBrief renders one stop's section of the prompt: what it is, why tds chose
// it, and the code it points at.
func stopBrief(s *PlanStop, opts NarrateOptions, dctx *Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### stop id: %s\n\n", s.ID)
	fmt.Fprintf(&b, "- Anchor: `%s`\n", s.Anchor)
	fmt.Fprintf(&b, "- Write about: %s\n", s.Task)
	if s.Evidence != "" {
		fmt.Fprintf(&b, "- What tds knows: %s\n", s.Evidence)
	}
	if code := excerptFor(s.Anchor, opts, dctx); code != "" {
		fmt.Fprintf(&b, "\n```\n%s\n```\n", code)
	} else {
		b.WriteString("\n(no source excerpt available for this anchor)\n")
	}
	b.WriteString("\n")
	return b.String()
}

// excerptFor reads the source an anchor points at, capped to MaxExcerptLines.
func excerptFor(anchorStr string, opts NarrateOptions, dctx *Context) string {
	path, start, end := dctx.resolveAnchor(anchorStr)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(opts.Root, filepath.FromSlash(path)))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return ""
	}
	if end-start+1 > opts.MaxExcerptLines {
		end = start + opts.MaxExcerptLines - 1
	}

	var b strings.Builder
	for i := start; i <= end && i <= len(lines); i++ {
		fmt.Fprintf(&b, "%d: %s\n", i, lines[i-1])
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "... (truncated at %d lines)\n", opts.MaxExcerptLines)
	}
	return strings.TrimRight(b.String(), "\n")
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// sortStringsStable orders ids so warnings come out deterministically.
func sortStringsStable(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
