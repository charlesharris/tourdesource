package draft

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/charlesharris/tourdesource/internal/orchestration"
)

// scriptedAssistant stands in for Claude. It records the prompts it was given
// and replies with whatever the test scripts, so the narrate pipeline —
// batching, decoding, the validation gate, merging — is exercised end to end
// without tmux and without spending tokens.
type scriptedAssistant struct {
	prompts []string
	// reply builds the response for one request, given the stop ids in it.
	reply  func(promptIDs []string) string
	closed bool
}

func (s *scriptedAssistant) Ask(_ context.Context, req orchestration.Request) ([]byte, error) {
	s.prompts = append(s.prompts, req.Prompt)
	return []byte(s.reply(stopIDsIn(req.Prompt))), nil
}

func (s *scriptedAssistant) Close() error { s.closed = true; return nil }

// stopIDsIn recovers the ids a prompt asked about, so a stand-in can answer them
// the way a real assistant would.
func stopIDsIn(prompt string) []string {
	var out []string
	for _, line := range strings.Split(prompt, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "### stop id: "); ok {
			out = append(out, strings.TrimSpace(rest))
		}
	}
	return out
}

// answerAll replies with distinct prose for every requested stop.
func answerAll(ids []string) string {
	stops := map[string]string{}
	for _, id := range ids {
		stops[id] = "Narrated prose for " + id + "."
	}
	b, _ := json.Marshal(narrateResponse{Stops: stops})
	return string(b)
}

func narrateFixture(t *testing.T, assistant orchestration.Assistant, opts NarrateOptions) (*Result, string) {
	t.Helper()
	root := newFixtureMap(t)
	opts.Assistant = assistant
	opts.Root = root
	opts.WorkDir = t.TempDir()
	return generate(t, root, Options{Narrate: &opts})
}

// TestNarrateFillsEveryStop is the headline behaviour of `--narrate`: TODO
// placeholders are replaced by real prose.
func TestNarrateFillsEveryStop(t *testing.T) {
	a := &scriptedAssistant{reply: answerAll}
	res, md := narrateFixture(t, a, NarrateOptions{})

	if res.Narrated != res.Stops {
		t.Errorf("narrated %d of %d stops, want all", res.Narrated, res.Stops)
	}
	if strings.Contains(md, "TODO:") {
		t.Error("a fully narrated draft should contain no TODO placeholders")
	}
	if !strings.Contains(md, "Narrated prose for ") {
		t.Error("narrated prose did not reach the document")
	}
	if len(a.prompts) == 0 {
		t.Fatal("assistant was never asked anything")
	}
}

// TestNarrateNeverAltersAnchors is the core safety property. The assistant is
// only ever given stop ids and only ever returns prose, so anchors cannot be
// changed — this pins that by diffing the anchors against an unnarrated run of
// the same plan.
func TestNarrateNeverAltersAnchors(t *testing.T) {
	root := newFixtureMap(t)

	_, plain := generate(t, root, Options{})
	// An assistant that tries as hard as it can to smuggle anchors back.
	hostile := &scriptedAssistant{reply: func(ids []string) string {
		stops := map[string]string{}
		for _, id := range ids {
			stops[id] = "See `app/models/Fabricated.rb::Fabricated#nope` for details."
		}
		b, _ := json.Marshal(map[string]any{
			"stops":   stops,
			"anchors": map[string]string{ids[0]: "app/models/evil.rb::Evil"},
		})
		return string(b)
	}}

	_, narrated := generate(t, root, Options{Narrate: &NarrateOptions{
		Assistant: hostile, Root: root, WorkDir: t.TempDir(),
	}})

	if !equalAnchors(anchorsIn(plain), anchorsIn(narrated)) {
		t.Errorf("narration changed the tour's anchors:\n plain    %v\n narrated %v",
			anchorsIn(plain), anchorsIn(narrated))
	}
	// Prose mentioning a fake path is the assistant's problem to get right, but
	// it must never become an anchor.
	for _, a := range anchorsIn(narrated) {
		if strings.Contains(a, "Fabricated") || strings.Contains(a, "evil.rb") {
			t.Errorf("a fabricated reference became an anchor: %q", a)
		}
	}
}

// TestNarrateRejectsUnknownStops covers the validation gate: prose for a stop we
// did not ask about is a hallucinated or crossed response and must not land.
func TestNarrateRejectsUnknownStops(t *testing.T) {
	a := &scriptedAssistant{reply: func(ids []string) string {
		stops := map[string]string{"totally-made-up-stop": "Prose for a stop that does not exist."}
		for _, id := range ids {
			stops[id] = "Real prose for " + id + "."
		}
		b, _ := json.Marshal(narrateResponse{Stops: stops})
		return string(b)
	}}

	var warnings []string
	root := newFixtureMap(t)
	res, md := generate(t, root, Options{
		Narrate: &NarrateOptions{Assistant: a, Root: root, WorkDir: t.TempDir()},
		Warnf:   func(f string, args ...any) { warnings = append(warnings, fmt.Sprintf(f, args...)) },
	})

	if strings.Contains(md, "does not exist") {
		t.Error("prose for an unknown stop was merged into the tour")
	}
	if res.Narrated != res.Stops {
		t.Errorf("real stops should still be narrated: %d of %d", res.Narrated, res.Stops)
	}
	if !containsSubstring(warnings, "unknown stop") {
		t.Errorf("the rejection should be reported, got warnings %v", warnings)
	}
}

// TestNarrateRejectsProseContainingDirectives stops the assistant from
// restructuring the document. A `::` line in prose would close a block early and
// silently reshape the tour.
func TestNarrateRejectsProseContainingDirectives(t *testing.T) {
	for _, bad := range []string{
		"Fine sentence.\n::\nSmuggled.",
		"::stop{anchor=\"a.rb::A\"}\nInjected stop.",
		"# Chapter: Injected chapter",
	} {
		t.Run(strings.SplitN(bad, "\n", 2)[0], func(t *testing.T) {
			a := &scriptedAssistant{reply: func(ids []string) string {
				stops := map[string]string{}
				for _, id := range ids {
					stops[id] = bad
				}
				b, _ := json.Marshal(narrateResponse{Stops: stops})
				return string(b)
			}}

			root := newFixtureMap(t)
			res, md := generate(t, root, Options{
				Narrate: &NarrateOptions{Assistant: a, Root: root, WorkDir: t.TempDir()},
			})

			if res.Narrated != 0 {
				t.Errorf("prose containing a directive must be rejected, narrated %d", res.Narrated)
			}
			if strings.Contains(md, "Smuggled") || strings.Contains(md, "Injected") {
				t.Error("directive-bearing prose reached the document")
			}
			// Rejected stops keep their placeholder, so the draft stays honest.
			if !strings.Contains(md, "TODO:") {
				t.Error("a rejected stop should fall back to its TODO")
			}
			// And the result must still be a parseable tour.
			assertParses(t, md)
		})
	}
}

// TestNarrateSurvivesAssistantFailure keeps one bad request from losing the
// whole draft.
func TestNarrateSurvivesAssistantFailure(t *testing.T) {
	a := &failingAssistant{}
	var warnings []string
	root := newFixtureMap(t)

	res, md := generate(t, root, Options{
		Narrate: &NarrateOptions{Assistant: a, Root: root, WorkDir: t.TempDir()},
		Warnf:   func(f string, args ...any) { warnings = append(warnings, fmt.Sprintf(f, args...)) },
	})

	if res.Narrated != 0 {
		t.Errorf("narrated = %d, want 0", res.Narrated)
	}
	if !strings.Contains(md, "TODO:") {
		t.Error("stops should fall back to TODO placeholders")
	}
	if !containsSubstring(warnings, "failed") {
		t.Errorf("the failure should be reported, got %v", warnings)
	}
	assertParses(t, md)
}

// TestNarrateToleratesFencedJSON — assistants wrap JSON in code fences despite
// being asked not to; that is not worth discarding a response over.
func TestNarrateToleratesFencedJSON(t *testing.T) {
	a := &scriptedAssistant{reply: func(ids []string) string {
		return "Here is the JSON you asked for:\n\n```json\n" + answerAll(ids) + "\n```\n"
	}}
	res, md := narrateFixture(t, a, NarrateOptions{})
	if res.Narrated != res.Stops {
		t.Errorf("narrated %d of %d, want all", res.Narrated, res.Stops)
	}
	if strings.Contains(md, "```json") {
		t.Error("the fence leaked into the document")
	}
}

// TestNarrateBatchesLargeTours checks the batching contract: with a tiny budget
// the tour is split across several requests, and every stop is still covered
// exactly once.
func TestNarrateBatchesLargeTours(t *testing.T) {
	a := &scriptedAssistant{reply: answerAll}
	res, _ := narrateFixture(t, a, NarrateOptions{MaxPromptBytes: 400})

	if len(a.prompts) < 2 {
		t.Fatalf("a 400-byte budget should force several requests, got %d", len(a.prompts))
	}
	seen := map[string]int{}
	for _, p := range a.prompts {
		for _, id := range stopIDsIn(p) {
			seen[id]++
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("stop %q appeared in %d batches, want exactly 1", id, n)
		}
	}
	if len(seen) != res.Stops {
		t.Errorf("batches covered %d stops, want %d", len(seen), res.Stops)
	}
}

// TestNarratePromptCarriesCodeAndGrounding checks the assistant is actually
// given what it needs: the anchored source, the task, and the ranking evidence.
func TestNarratePromptCarriesCodeAndGrounding(t *testing.T) {
	a := &scriptedAssistant{reply: answerAll}
	narrateFixture(t, a, NarrateOptions{})

	all := strings.Join(a.prompts, "\n")
	for _, want := range []string{
		"### stop id: ",                        // addressable ids
		"- Anchor: ",                           // what it points at
		"- Write about: ",                      // the task
		"Do NOT write any line beginning with", // the directive prohibition
		"\"stops\"",                            // the required output shape
	} {
		if !strings.Contains(all, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	// The fixture's map points at real files, so excerpts should be present.
	if !strings.Contains(all, "no source excerpt available") && !strings.Contains(all, "```") {
		t.Error("prompt carried neither a code excerpt nor an explicit absence marker")
	}
}

type failingAssistant struct{}

func (failingAssistant) Ask(context.Context, orchestration.Request) ([]byte, error) {
	return nil, fmt.Errorf("assistant exploded")
}
func (failingAssistant) Close() error { return nil }

func containsSubstring(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

func equalAnchors(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBannerReflectsNarration keeps the document honest about what it is. A
// narrated draft has no TODOs, and a banner that says otherwise trains readers
// to ignore banners — including the part warning that the prose is unreviewed.
func TestBannerReflectsNarration(t *testing.T) {
	root := newFixtureMap(t)

	_, plain := generate(t, root, Options{})
	if !strings.Contains(plain, "Prose marked\nTODO is not written yet") {
		t.Error("an unnarrated draft should say its prose is still TODO")
	}

	a := &scriptedAssistant{reply: answerAll}
	_, narrated := generate(t, root, Options{
		Narrate: &NarrateOptions{Assistant: a, Root: root, WorkDir: t.TempDir()},
	})
	if strings.Contains(narrated, "TODO is not written yet") {
		t.Error("a narrated draft must not claim its prose is TODO")
	}
	if !strings.Contains(narrated, "has not been reviewed") {
		t.Error("a narrated draft must warn that the prose is unreviewed")
	}
}
