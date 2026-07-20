package draft

// Subsystem narration is the second half of TDS-59. The first half — deciding
// which files group together and what column they sit in — is deterministic and
// lives in internal/site: it groups by directory role, counts files and commits,
// and picks key files by churn. What it cannot do is say what a group is *for*,
// so it names groups mechanically ("app/models") and describes them by stating
// only what was measured.
//
// This pass upgrades the name and writes the description. It deliberately does
// not touch the grouping, the column, or any number: those are measured facts,
// and an assistant that could revise them could quietly misreport the codebase.
// The gate below enforces exactly that boundary.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charlesharris/tourdesource/internal/narration"
	"github.com/charlesharris/tourdesource/internal/orchestration"
	"github.com/charlesharris/tourdesource/internal/site"
)

// subsystemResponse is the assistant's answer, keyed by subsystem id. Counts,
// columns and file lists are absent from this shape by design — the assistant
// is being asked for words, not for facts it could get wrong.
type subsystemResponse struct {
	Subsystems map[string]struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	} `json:"subsystems"`
}

// maxSubsystemName bounds a proposed name. The architecture map lays these out
// in fixed-width columns; a sentence-length "name" would break the layout.
const maxSubsystemName = 40

// narrateSubsystems names and describes the derived subsystems, writing results
// into doc. It returns how many were described.
//
// Failure is not fatal, for the same reason a failed stop batch is not: the
// mechanical name and the measured description are a worse architecture map but
// an honest one.
func narrateSubsystems(
	ctx context.Context,
	subs []site.Subsystem,
	doc *narration.Doc,
	assistant orchestration.Assistant,
	opts NarrateOptions,
	projectName string,
	save func() error,
	logf, warnf func(string, ...any),
) (int, error) {
	if len(subs) == 0 {
		return 0, nil
	}

	batches := batchSubsystems(subs, opts)
	logf("narrating %d subsystem(s) in %d request(s)", len(subs), len(batches))

	byID := map[string]site.Subsystem{}
	for _, s := range subs {
		byID[s.ID] = s
	}

	described := 0
	for i, batch := range batches {
		prompt := buildSubsystemPrompt(batch, projectName, opts)

		raw, err := assistant.Ask(ctx, orchestration.Request{
			Name:    fmt.Sprintf("subsystems-%d", i+1),
			Prompt:  prompt,
			Timeout: opts.Timeout,
		})
		if err != nil {
			warnf("subsystem request %d/%d failed, leaving those groups as measured: %v", i+1, len(batches), err)
			continue
		}

		var resp subsystemResponse
		if err := orchestration.DecodeJSON(raw, &resp); err != nil {
			warnf("subsystem request %d/%d returned unusable output, leaving those groups as measured: %v",
				i+1, len(batches), err)
			continue
		}

		requested := map[string]bool{}
		for _, s := range batch {
			requested[s.ID] = true
		}
		n, rejected := acceptSubsystems(resp, requested, byID, doc)
		described += n
		for _, r := range rejected {
			warnf("subsystem narration: %s", r)
		}

		// Persist per batch: an interrupted run keeps what it already paid for.
		if save != nil {
			if err := save(); err != nil {
				warnf("could not save narration after request %d/%d: %v", i+1, len(batches), err)
			}
		}
	}
	return described, nil
}

// acceptSubsystems is the validation gate. It takes a name and a description
// for groups we asked about and refuses everything else.
func acceptSubsystems(
	resp subsystemResponse,
	requested map[string]bool,
	byID map[string]site.Subsystem,
	doc *narration.Doc,
) (accepted int, rejected []string) {
	ids := make([]string, 0, len(resp.Subsystems))
	for id := range resp.Subsystems {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		got := resp.Subsystems[id]
		name := strings.TrimSpace(got.Name)
		desc := strings.TrimSpace(got.Desc)

		if !requested[id] {
			rejected = append(rejected, fmt.Sprintf("ignoring prose for unknown subsystem %q", id))
			continue
		}
		if desc == "" {
			rejected = append(rejected, fmt.Sprintf("empty description for subsystem %q, keeping the measured one", id))
			continue
		}
		// A description carrying a tour directive would corrupt nothing here —
		// it lands in JSON, not the tour file — but it signals a confused
		// response, and the same text is rendered into the architecture cards.
		if line, bad := containsDirective(desc); bad {
			rejected = append(rejected, fmt.Sprintf(
				"description for subsystem %q contains %q, keeping the measured one", id, line))
			continue
		}

		// Names are optional and heavily constrained: the column layout depends
		// on them being short, and a multi-line or sentence-shaped "name" is a
		// misunderstanding of the request rather than a useful rename.
		if name != "" {
			if strings.ContainsAny(name, "\n\r") || len(name) > maxSubsystemName {
				rejected = append(rejected, fmt.Sprintf(
					"name for subsystem %q is not a short label, keeping %q", id, byID[id].Name))
				name = ""
			}
		}

		doc.Subsystems[id] = narration.Subsystem{Name: name, Desc: desc}
		accepted++
	}
	return accepted, rejected
}

// batchSubsystems groups subsystems under the prompt budget. In practice a
// repository has a dozen or two, so this is usually one request — but the
// budget is enforced anyway rather than assumed.
func batchSubsystems(subs []site.Subsystem, opts NarrateOptions) [][]site.Subsystem {
	var batches [][]site.Subsystem
	var cur []site.Subsystem
	size := 0

	for _, s := range subs {
		entry := len(subsystemBrief(s))
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

// buildSubsystemPrompt writes the instruction for one batch.
func buildSubsystemPrompt(batch []site.Subsystem, projectName string, opts NarrateOptions) string {
	var b strings.Builder

	b.WriteString("You are describing the subsystems of a codebase for an architecture map.\n\n")
	fmt.Fprintf(&b, "Repository: %s\n\n", projectName)

	b.WriteString(`## Your task

Each group below was derived mechanically, by directory and file role. You are
given its file count, commit count, entry point and busiest files. Write what
each group IS FOR.

Rules:
- The description is 1-2 sentences. Say what this part of the system does and
  why someone new would care. Prose, not bullet points.
- Do NOT restate the file or commit counts. The card already shows them, and
  repeating them wastes the only two sentences you get.
- Be concrete and specific to the files you are shown. If they do not tell you
  what the group is for, say what can be said honestly and stop. Never invent
  a purpose, a framework, or behaviour you cannot see.
- The name is OPTIONAL. Supply one only when the mechanical name is genuinely
  unhelpful and you have a better short label for the same group. It must be a
  noun phrase under 40 characters, in sentence case ("Background jobs", not
  "BACKGROUND JOBS" or "the background job subsystem"). If the existing name is
  already fine, return an empty string and it will be kept.
- Do NOT propose a name that changes what the group appears to contain. You are
  relabelling a fixed set of files, not regrouping them.
- Markdown is allowed (backticks, **bold**). Do NOT use headings.

`)

	fmt.Fprintf(&b, "## Output format\n\nWrite a single JSON object to the answer file:\n\n")
	b.WriteString("{\n  \"subsystems\": {\n    \"<subsystem-id>\": {\n" +
		"      \"name\": \"<short label, or empty string to keep the current one>\",\n" +
		"      \"desc\": \"<1-2 sentences on what this group is for>\"\n    }\n  }\n}\n\n")
	b.WriteString("Use exactly the subsystem ids given below. Include every one of them. ")
	b.WriteString("Do not add any other keys.\n\n")

	b.WriteString("## The subsystems\n\n")
	for _, s := range batch {
		b.WriteString(subsystemBrief(s))
	}
	return b.String()
}

// subsystemBrief renders one group's section of the prompt. It shows the paths
// that characterise the group; the assistant infers purpose from those names
// rather than from source, which keeps this pass cheap enough to run always.
func subsystemBrief(s site.Subsystem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### subsystem id: %s\n\n", s.ID)
	fmt.Fprintf(&b, "- Current (mechanical) name: %s\n", s.Name)
	fmt.Fprintf(&b, "- Layer: %s\n", s.Column)
	fmt.Fprintf(&b, "- Size: %d files, %d commits\n", s.Files, s.Commits)
	if s.Entry != "" {
		fmt.Fprintf(&b, "- Entry point: `%s`\n", s.Entry)
	}
	if len(s.KeyFiles) > 0 {
		b.WriteString("- Busiest files:\n")
		for _, f := range s.KeyFiles {
			fmt.Fprintf(&b, "    - `%s`\n", f)
		}
	}
	if len(s.Deps) > 0 {
		fmt.Fprintf(&b, "- Depends on: %s\n", strings.Join(s.Deps, ", "))
	}
	b.WriteString("\n")
	return b.String()
}
