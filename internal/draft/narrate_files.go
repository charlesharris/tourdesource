package draft

// File summaries are the --full-narration pass: a paragraph for each file, shown
// in the explorer's "What this file is" panel.
//
// The bounding here is the whole design. Redmine's map holds 3,658 files, and
// the assistant is driven one request at a time, so describing everything is
// hours of wall clock and millions of tokens. Two things make that tractable:
// files are ranked by churn and capped, so the budget is spent where readers
// actually go; and every summary is cached against the file's content hash, so
// the expensive run happens once and later runs only revisit what changed.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charlesharris/tourdesource/internal/narration"
	"github.com/charlesharris/tourdesource/internal/orchestration"
	"github.com/charlesharris/tourdesource/internal/store"
)

// fileSummaryResponse is the assistant's answer, keyed by repo-relative path.
type fileSummaryResponse struct {
	Files map[string]string `json:"files"`
}

// summaryExcerptLines caps the code shown per file. A summary is a paragraph,
// not a review: the top of a file carries most of what identifies it, and a
// smaller excerpt buys more files per request.
const summaryExcerptLines = 60

// fileCandidate is a file in line for a summary, with the churn that ranked it.
type fileCandidate struct {
	path    string
	commits int
}

// narrateFiles writes per-file summaries into doc, returning how many it added.
func narrateFiles(
	ctx context.Context,
	files []store.File,
	signals []store.GitSignal,
	doc *narration.Doc,
	assistant orchestration.Assistant,
	opts NarrateOptions,
	root, projectName string,
	save func() error,
	logf, warnf func(string, ...any),
) (int, error) {
	ranked := rankFiles(files, signals, opts.MaxFiles)
	if len(ranked) == 0 {
		return 0, nil
	}

	// Read content first so the cache check is exact and the prompt carries the
	// same bytes the hash was taken over.
	type pending struct {
		path    string
		hash    string
		excerpt string
	}
	var todo []pending
	cached := 0
	for _, c := range ranked {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.path)))
		if err != nil {
			continue
		}
		hash := narration.Hash(content)
		if doc.Fresh(c.path, hash) {
			cached++
			continue
		}
		todo = append(todo, pending{
			path:    c.path,
			hash:    hash,
			excerpt: headLines(string(content), summaryExcerptLines),
		})
	}

	if cached > 0 {
		logf("%d file summary(ies) already current, skipping", cached)
	}
	if len(todo) == 0 {
		return 0, nil
	}

	// Batch under the prompt budget.
	var batches [][]pending
	var cur []pending
	size := 0
	for _, p := range todo {
		entry := len(p.path) + len(p.excerpt) + 64
		if len(cur) > 0 && size+entry > opts.MaxPromptBytes {
			batches = append(batches, cur)
			cur, size = nil, 0
		}
		cur = append(cur, p)
		size += entry
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}

	logf("summarising %d file(s) in %d request(s)", len(todo), len(batches))

	written := 0
	for i, batch := range batches {
		var b strings.Builder
		b.WriteString("You are writing one-paragraph summaries of source files for a code explorer.\n\n")
		fmt.Fprintf(&b, "Repository: %s\n\n", projectName)
		b.WriteString(`## Your task

For each file below, write what that file IS — the thing a reader wants to know
before opening it.

Rules:
- 1-3 sentences per file. Prose, not bullet points.
- Say what the file is responsible for and how it fits the system. Do not
  narrate its syntax or list its methods — the reader can see the code.
- You are shown the TOP of each file, not all of it. Describe what that
  supports and no more. Never invent behaviour you cannot see.
- If a file is boilerplate, generated, or genuinely uninteresting, say so
  plainly in one short sentence rather than dressing it up.
- Markdown is allowed (backticks, **bold**). Do NOT use headings.

`)
		b.WriteString("## Output format\n\nWrite a single JSON object to the answer file:\n\n")
		b.WriteString("{\n  \"files\": {\n    \"<path>\": \"<summary>\"\n  }\n}\n\n")
		b.WriteString("Use exactly the paths given below. Include every one of them. ")
		b.WriteString("Do not add any other keys.\n\n")
		b.WriteString("## The files\n\n")
		for _, p := range batch {
			fmt.Fprintf(&b, "### %s\n\n```\n%s\n```\n\n", p.path, p.excerpt)
		}

		raw, err := assistant.Ask(ctx, orchestration.Request{
			Name:    fmt.Sprintf("summaries-%d", i+1),
			Prompt:  b.String(),
			Timeout: opts.Timeout,
		})
		if err != nil {
			warnf("summary request %d/%d failed, those files keep no summary: %v", i+1, len(batches), err)
			continue
		}

		var resp fileSummaryResponse
		if err := orchestration.DecodeJSON(raw, &resp); err != nil {
			warnf("summary request %d/%d returned unusable output: %v", i+1, len(batches), err)
			continue
		}

		hashOf := map[string]string{}
		for _, p := range batch {
			hashOf[p.path] = p.hash
		}
		n, rejected := acceptSummaries(resp.Files, hashOf, doc)
		written += n
		for _, r := range rejected {
			warnf("file summary: %s", r)
		}

		// Persist per batch. This pass is the long one, and an interrupted run
		// that discarded an hour of completed work would make the whole feature
		// not worth starting.
		if save != nil {
			if err := save(); err != nil {
				warnf("could not save narration after request %d/%d: %v", i+1, len(batches), err)
			}
		}
		logf("summaries: %d/%d requests done, %d written", i+1, len(batches), written)
	}
	return written, nil
}

// acceptSummaries is the validation gate: only files we asked about, only
// non-empty prose.
func acceptSummaries(
	got map[string]string,
	hashOf map[string]string,
	doc *narration.Doc,
) (accepted int, rejected []string) {
	paths := make([]string, 0, len(got))
	for p := range got {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		summary := strings.TrimSpace(got[p])
		hash, ok := hashOf[p]
		if !ok {
			rejected = append(rejected, fmt.Sprintf("ignoring summary for unrequested file %q", p))
			continue
		}
		if summary == "" {
			rejected = append(rejected, fmt.Sprintf("empty summary for %q", p))
			continue
		}
		doc.Files[p] = narration.FileNote{Hash: hash, Summary: summary}
		accepted++
	}
	return accepted, rejected
}

// rankFiles orders files by churn and takes the top max. Churn is the honest
// proxy for "what will a reader open": the files a team keeps changing are the
// ones a newcomer meets first. Files with no source language are skipped —
// there is nothing useful to say about a binary or a lockfile.
func rankFiles(files []store.File, signals []store.GitSignal, max int) []fileCandidate {
	commits := map[string]int{}
	for _, s := range signals {
		commits[s.Path] = s.Churn
	}

	out := make([]fileCandidate, 0, len(files))
	for _, f := range files {
		if f.Language == "" {
			continue
		}
		out = append(out, fileCandidate{path: f.Path, commits: commits[f.Path]})
	}

	// Ties break on path so a run is reproducible.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].commits != out[j].commits {
			return out[i].commits > out[j].commits
		}
		return out[i].path < out[j].path
	})

	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// headLines returns the first n lines of src, numbered, noting any truncation
// so the assistant knows it is not seeing the whole file.
func headLines(src string, n int) string {
	lines := strings.Split(src, "\n")
	truncated := false
	if len(lines) > n {
		lines = lines[:n]
		truncated = true
	}
	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%d: %s\n", i+1, l)
	}
	if truncated {
		fmt.Fprintf(&b, "... (file continues past line %d)\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}
