package draft

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/narration"
	"github.com/charlesharris/tourdesource/internal/store"
)

// loadSidecar reads what a narration run left for `tds build`.
func loadSidecar(t *testing.T, root string) *narration.Doc {
	t.Helper()
	doc, err := narration.Load(narration.Path(filepath.Join(root, ".tds")))
	if err != nil {
		t.Fatalf("loading narration sidecar: %v", err)
	}
	return doc
}

// answerSubsystems describes every group it is asked about.
func answerSubsystems(ids []string) string {
	subs := map[string]any{}
	for _, id := range ids {
		subs[id] = map[string]string{
			"name": "Named " + id,
			"desc": "Describes " + id + ".",
		}
	}
	b, _ := json.Marshal(map[string]any{"subsystems": subs})
	return string(b)
}

// TestSubsystemsAreDescribedUnderNarrate is the headline behaviour of TDS-59's
// second half: the architecture map stops saying "not yet described".
func TestSubsystemsAreDescribedUnderNarrate(t *testing.T) {
	root := newFixtureMap(t)
	a := &scriptedAssistant{reply: answerAll, replySubsystems: answerSubsystems}

	res, _ := generate(t, root, Options{Narrate: &NarrateOptions{
		Assistant: a, Root: root, WorkDir: t.TempDir(),
	}})

	if len(a.subsystemPrompts) == 0 {
		t.Fatal("the subsystem pass never ran under --narrate")
	}
	if res.Subsystems == 0 {
		t.Error("no subsystems were described")
	}

	doc := loadSidecar(t, root)
	if len(doc.Subsystems) != res.Subsystems {
		t.Errorf("sidecar holds %d subsystems, result reported %d", len(doc.Subsystems), res.Subsystems)
	}
	for id, s := range doc.Subsystems {
		if s.Desc == "" {
			t.Errorf("subsystem %q was recorded with no description", id)
		}
	}
}

// TestSubsystemNarrationIsOptional covers the ordinary failure: an assistant
// that answers stops but has nothing to say about the architecture. The draft
// must still succeed, and the map must fall back to what was measured.
func TestSubsystemNarrationIsOptional(t *testing.T) {
	root := newFixtureMap(t)
	// replySubsystems is nil, so the stand-in returns an empty object.
	a := &scriptedAssistant{reply: answerAll}

	res, md := generate(t, root, Options{Narrate: &NarrateOptions{
		Assistant: a, Root: root, WorkDir: t.TempDir(),
	}})

	if res.Subsystems != 0 {
		t.Errorf("described %d subsystems from an empty response", res.Subsystems)
	}
	// The tour itself is unaffected — the two passes are independent.
	if res.Narrated != res.Stops {
		t.Errorf("narrated %d of %d stops; a quiet subsystem pass must not affect them",
			res.Narrated, res.Stops)
	}
	if strings.Contains(md, "TODO:") {
		t.Error("the tour should still be fully narrated")
	}
}

// TestSubsystemGateRefusesJunk pins the validation boundary. The gate exists
// because Subsystem.Name drives the architecture map's layout: a sentence where
// a label belongs breaks the columns, and an id nobody asked about is either a
// hallucination or a crossed response.
func TestSubsystemGateRefusesJunk(t *testing.T) {
	root := newFixtureMap(t)

	hostile := &scriptedAssistant{
		reply: answerAll,
		replySubsystems: func(ids []string) string {
			subs := map[string]any{
				// A group nobody asked about.
				"totally-made-up": map[string]string{"name": "Ghost", "desc": "Not real."},
			}
			for i, id := range ids {
				switch i {
				case 0:
					// A "name" that is really a sentence: the description is
					// still usable, but the name must not be taken.
					subs[id] = map[string]string{
						"name": "This is the subsystem that handles all of the models and more besides",
						"desc": "A usable description.",
					}
				case 1:
					// Empty description: nothing to merge.
					subs[id] = map[string]string{"name": "Fine", "desc": "   "}
				default:
					subs[id] = map[string]string{"name": "", "desc": "Plain description."}
				}
			}
			b, _ := json.Marshal(map[string]any{"subsystems": subs})
			return string(b)
		},
	}

	generate(t, root, Options{Narrate: &NarrateOptions{
		Assistant: hostile, Root: root, WorkDir: t.TempDir(),
	}})

	doc := loadSidecar(t, root)
	if _, ok := doc.Subsystems["totally-made-up"]; ok {
		t.Error("a subsystem id that was never requested reached the sidecar")
	}
	for id, s := range doc.Subsystems {
		if len(s.Name) > maxSubsystemName {
			t.Errorf("subsystem %q kept an over-long name %q", id, s.Name)
		}
		if strings.TrimSpace(s.Desc) == "" {
			t.Errorf("subsystem %q was recorded with a blank description", id)
		}
	}
}

// TestFullNarrationSummarisesAndCaches covers the pass that would otherwise run
// for hours. The cache is not an optimisation here — without it, every re-draft
// would re-describe a codebase that had not changed.
func TestFullNarrationSummarisesAndCaches(t *testing.T) {
	root := newFixtureMap(t)
	// The fixture keeps most files in the map only; write a couple to disk so
	// there is real content to summarise and to hash.
	for path, body := range map[string]string{
		"app/models/issue.rb": "class Issue < ActiveRecord::Base\n  belongs_to :project\nend\n",
		"app/models/user.rb":  "class User < Principal\n  has_many :issues\nend\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	answerFiles := func(paths []string) string {
		files := map[string]string{}
		for _, p := range paths {
			files[p] = "Summary of " + p + "."
		}
		b, _ := json.Marshal(map[string]any{"files": files})
		return string(b)
	}

	first := &scriptedAssistant{reply: answerAll, replyFiles: answerFiles}
	res, _ := generate(t, root, Options{Narrate: &NarrateOptions{
		Assistant: first, Root: root, WorkDir: t.TempDir(), FullNarration: true,
	}})

	if res.Summaries == 0 {
		t.Fatal("--full-narration summarised nothing")
	}
	doc := loadSidecar(t, root)
	if doc.Summary("app/models/issue.rb") == "" {
		t.Error("a file that exists on disk got no summary")
	}
	// A file in the map but absent from disk cannot be described honestly.
	if doc.Summary("app/jobs/mail_job.rb") != "" {
		t.Error("summarised a file that was never read")
	}

	// Second run, nothing changed: the assistant must not be asked again.
	second := &scriptedAssistant{reply: answerAll, replyFiles: answerFiles}
	res2, _ := generate(t, root, Options{Narrate: &NarrateOptions{
		Assistant: second, Root: root, WorkDir: t.TempDir(), FullNarration: true,
	}})
	if len(second.filePrompts) != 0 {
		t.Errorf("re-drafting an unchanged repo issued %d file request(s); the cache did not hold",
			len(second.filePrompts))
	}
	if res2.Summaries != 0 {
		t.Errorf("re-drafting rewrote %d summaries that were already current", res2.Summaries)
	}

	// Change one file: only that one is re-described.
	changed := filepath.Join(root, "app", "models", "issue.rb")
	if err := os.WriteFile(changed, []byte("class Issue\n  # rewritten\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := &scriptedAssistant{reply: answerAll, replyFiles: answerFiles}
	generate(t, root, Options{Narrate: &NarrateOptions{
		Assistant: third, Root: root, WorkDir: t.TempDir(), FullNarration: true,
	}})
	if len(third.filePrompts) != 1 {
		t.Fatalf("a one-file change should cost one request, got %d", len(third.filePrompts))
	}
	if !strings.Contains(third.filePrompts[0], "app/models/issue.rb") {
		t.Error("the re-described file was not the one that changed")
	}
	if strings.Contains(third.filePrompts[0], "app/models/user.rb") {
		t.Error("an unchanged file was re-described")
	}
}

// TestFileSummariesAreCapped pins the bound that makes this pass finishable.
// Without it a 3,658-file repository issues requests for hours.
func TestFileSummariesAreCapped(t *testing.T) {
	files := []store.File{
		{Path: "quiet.rb", Language: "ruby"},
		{Path: "busiest.rb", Language: "ruby"},
		{Path: "middling.rb", Language: "ruby"},
		{Path: "vendor/bundle.min.js.map", Language: ""}, // not source
	}
	signals := []store.GitSignal{
		{Path: "quiet.rb", Churn: 1},
		{Path: "busiest.rb", Churn: 500},
		{Path: "middling.rb", Churn: 50},
	}

	ranked := rankFiles(files, signals, 2)
	if len(ranked) != 2 {
		t.Fatalf("MaxFiles=2 yielded %d files", len(ranked))
	}
	// Churn is the proxy for "what will a reader open" — the busiest file must
	// survive the cap, the quietest must not.
	if ranked[0].path != "busiest.rb" || ranked[1].path != "middling.rb" {
		t.Errorf("cap kept the wrong files: %+v", ranked)
	}
	// A file with no source language has nothing worth summarising.
	for _, c := range ranked {
		if c.path == "vendor/bundle.min.js.map" {
			t.Error("a file with no source language was queued for a summary")
		}
	}
}
